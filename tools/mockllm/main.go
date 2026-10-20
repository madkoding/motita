// Command mockllm is a simulated LLM for the end-to-end tests.
//
// It behaves like a model that gets it wrong first and then corrects itself:
//
//	Analysis  -> always understandable, no subtasks
//	Plan      -> one step
//	Execution -> attempt 1: writes INVALID content (the anchor must fail)
//	             attempt 2: writes valid-content (the anchor must pass)
//
// It is pure Go and cross-compiled for linux/386, so it runs inside the same
// 32-bit container as the agent: the test needs no external network and no keys.
// It implements the three dialects (openai, anthropic, gemini) so any of them can
// be exercised.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	executionAttempts int32

	// replyDelayMS slows every reply down, in milliseconds. Zero is the default and means no
	// change at all; the e2e gateway script raises it so a run can be caught in flight.
	replyDelayMS int64
)

// phase works out which part of the flow the agent is in from the prompt it
// received.
func phase(text string) string {
	switch {
	case strings.Contains(text, "## CRITERIA AUDIT"):
		return "audit"
	case strings.Contains(text, "## FINAL ANSWER"):
		return "synthesize"
	case strings.Contains(text, "## ANALYSIS OF THE TASK") || strings.Contains(text, "success_criteria"):
		return "analyze"
	case strings.Contains(text, "## ACTION PLAN") || strings.Contains(text, "expected_result"):
		return "plan"
	case strings.Contains(text, "## ACTION") || strings.Contains(text, "reasoning"):
		return "execute"
	default:
		return "unknown"
	}
}

// bigOutputCommand is the execution command this mock proposes when MOCK_BIG_OUTPUT is set: it
// prints many matching lines, which is the shape of a `grep` over a tree. It exists so a check can
// drive a real command whose output is far larger than any cap on the way to the interface.
const bigOutputCommand = `grep -n "match line" big.txt`

// contentJSON returns the JSON the "model" would put in the response.
func contentJSON(phaseName string, n int) string {
	if os.Getenv("MOCK_BIG_OUTPUT") != "" && phaseName != "analyze" && phaseName != "plan" {
		response := map[string]any{
			"reasoning": fmt.Sprintf("attempt %d", n),
			"actions": []map[string]string{
				{"kind": "command", "description": "grep the tree", "command": bigOutputCommand},
			},
			"final_action": map[string]string{"description": "notify", "command": ""},
			"done":         true,
		}
		data, _ := json.Marshal(response)
		return string(data)
	}
	switch phaseName {
	case "synthesize":
		// The structured report the final answer is asked to be (agent.Report), with every
		// section filled, so a front end can be driven against the whole shape. With
		// MOCK_SUGGESTIONS set it also proposes follow-ups, so the suggestions flow can be
		// driven end to end.
		if os.Getenv("MOCK_SUGGESTIONS") != "" {
			return synthesizeJSON[:len(synthesizeJSON)-1] + suggestionsJSON
		}
		return synthesizeJSON
	case "audit":
		return auditJSON
	case "analyze":
		return analyzeJSON
	case "plan":
		return planJSON
	default:
		return executeJSON(n)
	}
}

// headerContentType is the header that names the type of a body.
const headerContentType = "Content-Type"

// The fixed replies of the synthesize, analyze and plan phases.
const (
	synthesizeJSON = `{"status":"done","summary":"I wrote **report.txt** and checked its content.",` +
		`"changes":[{"path":"report.txt","kind":"added","description":"holds the requested content"}],` +
		`"verification":[{"check":"anchor: content is valid","result":"pass","evidence":"ANCHOR_OK"},` +
		`{"check":"browser screenshot","result":"skipped","evidence":"nothing visual to look at"}],` +
		`"risks":["the file is overwritten on every run"],"next_steps":["commit report.txt"]}`
	suggestionsJSON = `,"suggestions":[{"title":"test the empty report","detail":"nothing checks it"},` +
		`{"title":"log the overwrite","detail":"it is silent today"}]}`
	analyzeJSON = `{"understandable":true,"summary":"leave the report with the requested content",` +
		`"success_criteria":["report.txt holds content-valid"],"risks":[],"needs_subtasks":false}`
	auditJSON = `{"criteria":[{"criterion":"report.txt holds content-valid","met":true,` +
		`"evidence":"the anchor read it"}]}`
	planJSON = `{"plan":[{"step":1,"action":"write the report","command":"write report.txt"}],` +
		`"subtasks":[],"expected_result":"report.txt with content-valid"}`
)

// executeJSON is the reply of execution attempt n: the first one gets it wrong on purpose.
func executeJSON(n int) string {
	command := `printf 'content-invalid' > report.txt`
	if n >= 2 {
		command = `printf 'content-valid' > report.txt`
	}
	return commandJSON(command, n)
}

// commandJSON is an execution reply that runs one command.
func commandJSON(command string, n int) string {
	response := map[string]any{
		"reasoning": fmt.Sprintf("attempt %d", n),
		"actions": []map[string]string{
			{"kind": "command", "description": "write the report", "command": command},
		},
		"final_action": map[string]string{"description": "notify", "command": ""},
	}
	data, _ := json.Marshal(response)
	return string(data)
}

// evalSolutions are the commands a model that does the model check's fixed tasks right proposes
// (internal/evals), found by a phrase of the task in the prompt. regress marks the ones MOCK_EVAL=regress
// gets wrong, so a check can be driven from a good run to a worse one.
var evalSolutions = []struct {
	phrase, command string
	regress         bool
}{
	{"named hello.txt", `printf hello > hello.txt`, false},
	{"misspelled word", `printf 'I receive your mail\n' > letter.txt`, true},
	{"lines of data.txt", `printf 7 > count.txt`, false},
	{"Rename old.txt", `mv old.txt new.txt`, false},
	{"names.txt in alphabetical", `sort names.txt > sorted.txt`, true},
}

// evalCommand is the command to propose for the model check's task named in the prompt: "" when
// MOCK_EVAL is unset, the prompt is not one of those tasks, or (regress) the mock is to get it wrong.
func evalCommand(text string) string {
	mode := os.Getenv("MOCK_EVAL")
	if mode == "" {
		return ""
	}
	for _, s := range evalSolutions {
		if strings.Contains(text, s.phrase) && !(s.regress && mode == "regress") {
			return s.command
		}
	}
	return ""
}

func respond(w http.ResponseWriter, dialect, content string) {
	w.Header().Set(headerContentType, "application/json")
	json.NewEncoder(w).Encode(replyBody(dialect, content))
}

// replyBody wraps content in the response shape of the dialect (openai unless named).
func replyBody(dialect, content string) map[string]any {
	switch dialect {
	case "anthropic":
		return map[string]any{
			"content": []map[string]string{{"type": "text", "text": content}},
		}
	case "gemini":
		return geminiBody(content)
	default:
		return openAIBody(content)
	}
}

func geminiBody(content string) map[string]any {
	return map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []map[string]string{{"text": content}}},
			"finishReason": "STOP",
		}},
	}
}

func openAIBody(content string) map[string]any {
	body := map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]string{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
	}
	if os.Getenv("MOCK_EVAL") != "" {
		// The model check measures what a run spent, so its mock reports tokens like a real provider.
		body["usage"] = map[string]int{"prompt_tokens": 1200, "completion_tokens": 300, "total_tokens": 1500}
	}
	return body
}

func handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		fmt.Fprint(w, "ok")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/audio/speech") {
		// The speech API: a short silence, enough for the browser to play something.
		w.Header().Set(headerContentType, "audio/wav")
		_, _ = w.Write(silence(300))
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	text := string(body)
	f := phase(text)

	// The delay makes a run OBSERVABLE while it is in flight. It defaults to zero, so every
	// existing caller behaves exactly as before; the concurrency check is the one that needs it,
	// because a run that finishes in microseconds cannot be seen running and a test that sleeps
	// instead would be guessing at the machine's speed.
	if d := atomic.LoadInt64(&replyDelayMS); d > 0 {
		time.Sleep(time.Duration(d) * time.Millisecond)
	}

	dialect := "openai"
	switch {
	case strings.Contains(r.URL.Path, "messages"):
		dialect = "anthropic"
	case strings.Contains(r.URL.Path, "generateContent"):
		dialect = "gemini"
	default:
		// Every other path speaks the OpenAI dialect.
	}

	n := int(atomic.LoadInt32(&executionAttempts))
	if f == "execute" {
		n = int(atomic.AddInt32(&executionAttempts, 1))
	}
	log.Printf("phase=%s dialect=%s execution_attempt=%d prompt_bytes=%d", f, dialect, n, len(text))

	content := contentJSON(f, n)
	if cmd := evalCommand(text); cmd != "" && f == "execute" {
		content = commandJSON(cmd, n)
	}
	if msg := scriptedReply(text); msg != "" {
		// The configuration may ask for fixed replies (not used here).
		content = msg
	}
	if dialect == "openai" && wantsStream(body) {
		if planWantsCommand(text) {
			streamCommand(w)
			return
		}
		if strings.Contains(text, `"execute_command"`) {
			// Plan mode's second round: the command ran, and the answer is plain prose.
			content = planAnswer
		}
		stream(w, f, content)
		return
	}
	respond(w, dialect, content)
}

// silence is a WAV of ms milliseconds of silence: 8 kHz, 8-bit mono, the smallest a browser plays.
func silence(ms int) []byte {
	const rate = 8000
	n := rate * ms / 1000
	out := make([]byte, 44+n)
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+n))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1) // PCM
	binary.LittleEndian.PutUint16(out[22:], 1) // mono
	binary.LittleEndian.PutUint32(out[24:], rate)
	binary.LittleEndian.PutUint32(out[28:], rate) // bytes per second
	binary.LittleEndian.PutUint16(out[32:], 1)    // block align
	binary.LittleEndian.PutUint16(out[34:], 8)    // bits per sample
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(n))
	for i := 44; i < len(out); i++ {
		out[i] = 128 // the zero of 8-bit PCM
	}
	return out
}

// wantsStream reports whether the request asked for a streamed reply.
func wantsStream(body []byte) bool {
	var req struct {
		Stream bool `json:"stream"`
	}
	return json.Unmarshal(body, &req) == nil && req.Stream
}

// streamChunk is how long the mock waits between two streamed fragments, so a browser check
// can catch the reply being written. It is a fraction of the reply delay: zero when there is
// none, which keeps every other caller as fast as before.
func streamChunk() time.Duration {
	return time.Duration(atomic.LoadInt64(&replyDelayMS)) * time.Millisecond / 20
}

// stream answers as an OpenAI-compatible provider streams: the model's reasoning tokens first
// (reasoning_content), then the reply in small fragments, then [DONE]. The agent shows both
// while they arrive.
func stream(w http.ResponseWriter, phaseName, content string) {
	w.Header().Set(headerContentType, "text/event-stream")
	flusher, _ := w.(http.Flusher)
	frame := func(delta map[string]string) {
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(streamChunk())
	}
	frame(map[string]string{"reasoning_content": "Considering the " + phaseName + " step. "})
	frame(map[string]string{"reasoning_content": "The report has to hold the requested content."})
	for len(content) > 0 {
		n := min(12, len(content))
		frame(map[string]string{"content": content[:n]})
		content = content[n:]
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// planCommand is the command the mock runs when it is asked in plan mode, before it answers.
const planCommand = "ls -la"

// planAnswer is plan mode's answer once the command has run.
const planAnswer = "The workspace is a git repository with one empty commit."

// planWantsCommand reports whether a request is plan mode's first round: plan mode offers its tools
// (execute_command among them) and, until a command has run, there is no tool result in the
// conversation. Plan mode is the one path that runs commands through tools, so a mock that never
// called one could not show what plan mode puts in the terminal and thinking drawers.
func planWantsCommand(text string) bool {
	return strings.Contains(text, `"execute_command"`) && !strings.Contains(text, `"role":"tool"`)
}

// streamCommand answers plan mode's first round as a provider that thinks, says what it is about to
// do and calls execute_command does: reasoning tokens, a sentence, then the tool call.
func streamCommand(w http.ResponseWriter) {
	w.Header().Set(headerContentType, "text/event-stream")
	flusher, _ := w.(http.Flusher)
	frame := func(delta map[string]any, finish string) {
		choice := map[string]any{"delta": delta}
		if finish != "" {
			choice["finish_reason"] = finish
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{choice}})
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(streamChunk())
	}
	frame(map[string]any{"reasoning_content": "The question is about the workspace. "}, "")
	frame(map[string]any{"reasoning_content": "Listing it is the quickest way to answer."}, "")
	frame(map[string]any{"content": "Let me look at the workspace first."}, "")
	args, _ := json.Marshal(map[string]string{"command": planCommand})
	frame(map[string]any{"tool_calls": []any{map[string]any{
		"index": 0, "id": "call_1", "type": "function",
		"function": map[string]string{"name": "execute_command", "arguments": string(args)},
	}}}, "tool_calls")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// scriptedReply allows fixed replies through the prompt, used by other tests in
// the repository.
func scriptedReply(text string) string {
	const marker = "RESPOND_WITH:"
	if i := strings.Index(text, marker); i >= 0 {
		return strings.TrimSpace(text[i+len(marker):])
	}
	return ""
}

// listenAndServe is replaceable so the bootstrap can be tested: the real one
// blocks until the server is stopped, which cannot be observed from a test. The
// environment marker lets a test make the bind fail on purpose.
var listenAndServe = func(srv *http.Server) error {
	if os.Getenv("MOCKLLM_FORCE_LISTEN_FAILURE") == "1" {
		return errors.New("forced listen failure for the test")
	}
	return srv.ListenAndServe()
}

// mainBody is the real bootstrap, separated so a test can call it with the server
// replaced (the real one blocks until stopped).
func mainBody(port *int, host *string, exit func(int)) {
	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", *host, *port),
		Handler:           http.HandlerFunc(handle),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "mockllm listening on http://%s:%d/v1\n", *host, *port)
	if err := listenAndServe(srv); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		exit(1)
	}
}

func main() {
	port := flag.Int("port", 8210, "listening port")
	host := flag.String("host", "0.0.0.0", "interface")
	// The delay is settable from the command line as well as the environment, because the e2e
	// scripts start this process inside a container and passing a flag is clearer than relying on
	// the environment surviving that boundary.
	delay := flag.Int64("delay-ms", envDelayMS(), "delay every reply by this many milliseconds")
	flag.Parse()
	atomic.StoreInt64(&replyDelayMS, *delay)

	mainBody(port, host, os.Exit)
}

// envDelayMS reads MOCKLLM_DELAY_MS, or reports zero.
func envDelayMS() int64 {
	v, err := strconv.ParseInt(os.Getenv("MOCKLLM_DELAY_MS"), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

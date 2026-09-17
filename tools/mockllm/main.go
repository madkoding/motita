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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

var (
	executionAttempts int32
)

// phase works out which part of the flow the agent is in from the prompt it
// received.
func phase(text string) string {
	switch {
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

// contentJSON returns the JSON the "model" would put in the response.
func contentJSON(phaseName string, n int) string {
	switch phaseName {
	case "analyze":
		return `{"understandable":true,"summary":"leave the report with the requested content","success_criteria":["report.txt holds content-valid"],"risks":[],"needs_subtasks":false}`
	case "plan":
		return `{"plan":[{"step":1,"action":"write the report","command":"write report.txt"}],"subtasks":[],"expected_result":"report.txt with content-valid"}`
	default:
		// The first execution attempt gets it wrong on purpose.
		command := `printf 'content-invalid' > report.txt`
		if n >= 2 {
			command = `printf 'content-valid' > report.txt`
		}
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
}

func respond(w http.ResponseWriter, dialect, content string) {
	w.Header().Set("Content-Type", "application/json")
	switch dialect {
	case "anthropic":
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": content}},
		})
	case "gemini":
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"parts": []map[string]string{{"text": content}}},
				"finishReason": "STOP",
			}},
		})
	default:
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": content},
				"finish_reason": "stop",
			}},
		})
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		fmt.Fprint(w, "ok")
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	text := string(body)
	f := phase(text)

	dialect := "openai"
	switch {
	case strings.Contains(r.URL.Path, "messages"):
		dialect = "anthropic"
	case strings.Contains(r.URL.Path, "generateContent"):
		dialect = "gemini"
	}

	n := int(atomic.LoadInt32(&executionAttempts))
	if f == "execute" {
		n = int(atomic.AddInt32(&executionAttempts, 1))
	}
	log.Printf("phase=%s dialect=%s execution_attempt=%d prompt_bytes=%d", f, dialect, n, len(text))

	if msg := scriptedReply(text); msg != "" {
		// The configuration may ask for fixed replies (not used here).
		respond(w, dialect, msg)
		return
	}
	respond(w, dialect, contentJSON(f, n))
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
	flag.Parse()

	mainBody(port, host, os.Exit)
}

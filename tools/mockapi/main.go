// Command mockapi is a minimal OpenAI-compatible server to verify motita
// without spending tokens or needing a key.
//
// It is pure Go and also cross-compiled for linux/386, so it can run INSIDE the
// same 32-bit machine as motita: that makes the end-to-end test self-contained
// (no dependency on the host's network).
//
// The script it implements:
//
//  1. Facing a user message it replies with two tool calls: run_command ("arch")
//     and read_file ("/etc/os-release").
//  2. Facing the tool results it returns a final text that cites them, preceded by
//     the E2E-RESULT marker.
//
// Usage:
//
//	mockapi -port 8099 &
//	export OPENAI_API_KEY=test
//	export OPENAI_BASE_URL=http://127.0.0.1:8099/v1
//	motita -p "tell me the architecture"
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
	"time"
)

type message struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
}

type request struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Tools    []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	ToolChoice string `json:"tool_choice"`
	// Stream is what the client actually asks for: the plan loop streams, so the
	// mock has to answer in Server-Sent Events. A mock that replies with a plain
	// JSON body to a streaming request is a mock that does not speak the protocol,
	// and the end-to-end test then fails for a reason that has nothing to do with
	// the code under test.
	Stream bool `json:"stream"`
}

// toolCall builds a tool_call with `arguments` as a JSON string, which is exactly
// the format the OpenAI spec mandates (and the one that historically broke
// clients expecting an object).
func toolCall(id, name string, arguments map[string]string) map[string]any {
	raw, _ := json.Marshal(arguments)
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": string(raw),
		},
	}
}

func toolCallsResponse() map[string]any {
	return map[string]any{
		"id":     "chatcmpl-mock-1",
		"object": "chat.completion",
		"model":  "mock",
		"choices": []map[string]any{{
			"index":         0,
			"finish_reason": "tool_calls",
			"message": map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{
					toolCall("call_1", "run_command", map[string]string{"cmd": "arch; getconf LONG_BIT"}),
					toolCall("call_2", "read_file", map[string]string{"path": "/etc/os-release"}),
				},
			},
		}},
	}
}

func finalResponse(results []message) map[string]any {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, fmt.Sprintf("%s=%s", r.Name, strings.Join(strings.Fields(r.Content), " ")))
	}
	return map[string]any{
		"id":     "chatcmpl-mock-2",
		"object": "chat.completion",
		"model":  "mock",
		"choices": []map[string]any{{
			"index":         0,
			"finish_reason": "stop",
			"message": map[string]any{
				"role":    "assistant",
				"content": "E2E-RESULT " + strings.Join(parts, " | "),
			},
		}},
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.Error(w, "unknown path: "+r.URL.Path, http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "unreadable request", http.StatusBadRequest)
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Function.Name)
	}
	last := "none"
	if len(req.Messages) > 0 {
		last = req.Messages[len(req.Messages)-1].Role
	}
	log.Printf("request: messages=%d last_role=%s tool_choice=%q tools=%v",
		len(req.Messages), last, req.ToolChoice, names)

	var out map[string]any
	if last == "tool" {
		var results []message
		for _, m := range req.Messages {
			if m.Role == "tool" {
				results = append(results, m)
			}
		}
		out = finalResponse(results)
	} else {
		out = toolCallsResponse()
	}

	if req.Stream {
		writeSSE(w, out)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// writeSSE renders a completion as the event stream a real provider sends: one
// chunk per piece, a final chunk carrying the finish reason, and the [DONE]
// sentinel. Content is delivered as text deltas; tool calls are delivered as a
// single delta, which is what a provider does for a call that is not split across
// frames.
func writeSSE(w http.ResponseWriter, out map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)

	choices, _ := out["choices"].([]map[string]any)
	if len(choices) == 0 {
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	choice := choices[0]
	message, _ := choice["message"].(map[string]any)

	send := func(delta map[string]any, finish string) {
		chunk := map[string]any{
			"id":      out["id"],
			"object":  "chat.completion.chunk",
			"model":   out["model"],
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	if calls, ok := message["tool_calls"].([]map[string]any); ok && len(calls) > 0 {
		send(map[string]any{"role": "assistant", "tool_calls": calls}, "tool_calls")
	} else {
		text, _ := message["content"].(string)
		// Two frames: a provider never sends the whole answer as one chunk.
		//
		// The split is by rune, not by byte: cutting a multi-byte character in half
		// produces two chunks that are not valid UTF-8, and a client that assembles
		// them back does not necessarily end up with the original text. The E2E
		// answer contains accented characters, so this is not hypothetical.
		if text != "" {
			runes := []rune(text)
			half := len(runes) / 2
			send(map[string]any{"content": string(runes[:half])}, "")
			send(map[string]any{"content": string(runes[half:])}, "stop")
		} else {
			send(map[string]any{"content": ""}, "stop")
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// listenAndServe is replaceable so the bootstrap can be tested: the real one
// blocks until the server is stopped, which cannot be observed from a test. The
// environment marker lets a test make the bind fail on purpose.
var listenAndServe = func(srv *http.Server) error {
	if os.Getenv("MOCKAPI_FORCE_LISTEN_FAILURE") == "1" {
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
	fmt.Fprintf(os.Stderr, "mockapi listening on http://%s:%d/v1\n", *host, *port)
	if err := listenAndServe(srv); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		exit(1)
	}
}

func main() {
	port := flag.Int("port", 8099, "listening port")
	host := flag.String("host", "0.0.0.0", "listening interface")
	flag.Parse()

	mainBody(port, host, os.Exit)
}

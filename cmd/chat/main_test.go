package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	// No ANSI colours in the tests: the output is more readable.
	colorEnabled = false
}

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("could not serialise the arguments: %v", err)
	}
	return raw
}

// --- Tools ------------------------------------------------------------------

func TestReadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	content := "hello starlight\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := toolReadFile(args(t, readFileArgs{Path: path})); got != content {
		t.Fatalf("content = %q, expected %q", got, content)
	}

	if got := toolReadFile(args(t, readFileArgs{})); !strings.Contains(got, "the 'path' parameter is missing") {
		t.Fatalf("without a path an error was expected, got %q", got)
	}

	if got := toolReadFile(args(t, readFileArgs{Path: path + ".missing"})); !strings.Contains(got, "Error accessing the file") {
		t.Fatalf("missing file: got %q", got)
	}

	if got := toolReadFile(args(t, readFileArgs{Path: t.TempDir()})); !strings.Contains(got, "is a directory") {
		t.Fatalf("directory: got %q", got)
	}
}

func TestReadFileRejectsLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.bin")
	// One byte above the allowed limit.
	if err := os.WriteFile(path, make([]byte, maxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	got := toolReadFile(args(t, readFileArgs{Path: path}))
	if !strings.Contains(got, "exceeds the") {
		t.Fatalf("a size rejection was expected, got %q", got)
	}
}

func TestRunCommand(t *testing.T) {
	got := toolRunCommand(args(t, runCommandArgs{Cmd: "echo starlight-ok"}))
	if strings.TrimSpace(got) != "starlight-ok" {
		t.Fatalf("output = %q", got)
	}

	// Pipes and redirections must work (sh -c).
	got = toolRunCommand(args(t, runCommandArgs{Cmd: "printf 'a\\nb\\n' | wc -l"}))
	if strings.TrimSpace(got) != "2" {
		t.Fatalf("pipe not supported, output = %q", got)
	}

	got = toolRunCommand(args(t, runCommandArgs{Cmd: "exit 3"}))
	if !strings.Contains(got, "Execution error") {
		t.Fatalf("an execution error was expected, got %q", got)
	}

	if got := toolRunCommand(args(t, runCommandArgs{Cmd: "   "})); !strings.Contains(got, "the 'cmd' parameter is missing") {
		t.Fatalf("empty command: %q", got)
	}
}

func TestRunCommandTimeout(t *testing.T) {
	start := time.Now()
	got := toolRunCommand(args(t, runCommandArgs{Cmd: "sleep 30", TimeoutSeconds: 1}))
	if !strings.Contains(got, "exceeded the 1 s limit") {
		t.Fatalf("a timeout was expected, got %q", got)
	}
	// The cut must be real: if only `sh` died and not its `sleep` child, Wait
	// would keep waiting with the pipe open (a regression that already happened).
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the timeout did not kill the process group in time: %s", elapsed)
	}
}

// TestDecodeArgsFormats covers the real bug found when testing against a gateway
// that sends `arguments` as a JSON string instead of an object.
func TestDecodeArgsFormats(t *testing.T) {
	var dst readFileArgs

	if err := decodeArgs(json.RawMessage(`{"path":"/tmp/x"}`), &dst); err != nil || dst.Path != "/tmp/x" {
		t.Fatalf("JSON object: path=%q err=%v", dst.Path, err)
	}

	dst = readFileArgs{}
	if err := decodeArgs(json.RawMessage(`"{\"path\":\"/tmp/y\"}"`), &dst); err != nil || dst.Path != "/tmp/y" {
		t.Fatalf("string containing JSON: path=%q err=%v", dst.Path, err)
	}

	dst = readFileArgs{Path: "intact"}
	if err := decodeArgs(json.RawMessage(``), &dst); err != nil || dst.Path != "intact" {
		t.Fatalf("empty arguments: path=%q err=%v", dst.Path, err)
	}
	if err := decodeArgs(json.RawMessage(`null`), &dst); err != nil || dst.Path != "intact" {
		t.Fatalf("null arguments: path=%q err=%v", dst.Path, err)
	}
}

func TestUnknownTool(t *testing.T) {
	a := newAgent(config{baseURL: "http://127.0.0.1:1", model: "test", maxLoops: 1})
	got := a.runTool(toolCall{Function: functionCall{Name: "delete_everything"}})
	if !strings.Contains(got, "unknown tool") {
		t.Fatalf("an unknown-tool error was expected, got %q", got)
	}
}

// --- HTTP client ------------------------------------------------------------

func TestCompleteErrors(t *testing.T) {
	cases := []struct {
		name     string
		response string
		status   int
		contains string
	}{
		{"empty choices does not panic", `{"choices":[]}`, http.StatusOK, "empty choices"},
		{"API error", `{"error":{"message":"invalid key"}}`, http.StatusUnauthorized, "invalid key"},
		{"non-JSON body", `<html>bad gateway</html>`, http.StatusBadGateway, "HTTP 502"},
		{"broken json", `{"choices":`, http.StatusOK, "unreadable response"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.response)
			}))
			defer srv.Close()

			a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
			if _, err := a.complete(""); err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("an error containing %q was expected, got %v", tc.contains, err)
			}
		})
	}
}

// --- Agent loop -------------------------------------------------------------

// TestTurnRunsToolAndAnswers verifies the full cycle: the model asks for a tool,
// the agent runs it, sends the result back and receives the final text.
func TestTurnRunsToolAndAnswers(t *testing.T) {
	var (
		requests      int
		secondRequest chatRequest
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("unreadable request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")

		if requests == 1 {
			// The model asks to run a command.
			if len(req.Tools) != 2 {
				t.Errorf("2 tools were expected, %d were sent", len(req.Tools))
			}
			fmt.Fprint(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"run_command",
				"arguments":"{\"cmd\":\"echo starlight-alive\"}"}}]}}]}`)
			return
		}

		secondRequest = req
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"The command returned starlight-alive."}}]}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 5, timeout: 5 * time.Second})
	if err := a.turn("check that the system responds"); err != nil {
		t.Fatalf("turn returned an error: %v", err)
	}

	if requests != 2 {
		t.Fatalf("2 requests were expected, there were %d", requests)
	}

	// The tool result must travel back to the model.
	var result string
	for _, m := range secondRequest.Messages {
		if m.Role == "tool" {
			result = m.Content
			if m.ToolCallID != "call_1" {
				t.Errorf("tool_call_id = %q, expected call_1", m.ToolCallID)
			}
		}
	}
	if !strings.Contains(result, "starlight-alive") {
		t.Fatalf("the model did not receive the tool's real output: %q", result)
	}
}

// TestTurnForcesFinalAnswer checks that once the iterations run out, the answer
// is requested with tool_choice="none".
func TestTurnForcesFinalAnswer(t *testing.T) {
	var lastToolChoice string
	requests := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)
		lastToolChoice = req.ToolChoice
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant",
			"tool_calls":[{"id":"c","type":"function","function":{"name":"run_command","arguments":"{\"cmd\":\"true\"}"}}]}}]}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 2, timeout: 5 * time.Second})
	if err := a.turn("keep going"); err != nil {
		t.Fatalf("turn returned an error: %v", err)
	}

	if requests != 3 { // 2 iterations + 1 forced
		t.Fatalf("3 requests were expected, there were %d", requests)
	}
	if lastToolChoice != "none" {
		t.Fatalf("the last request had to carry tool_choice=none, it carried %q", lastToolChoice)
	}
}

func TestTurnRevertsHistoryOnNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"boom"}}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 2, timeout: 5 * time.Second})
	before := len(a.hist)
	if err := a.turn("something"); err == nil {
		t.Fatal("a network error was expected")
	}
	if len(a.hist) != before {
		t.Fatalf("the history was left inconsistent: %d messages (before %d)", len(a.hist), before)
	}
}

func TestTrimHistory(t *testing.T) {
	a := newAgent(config{})
	a.hist = append(a.hist, message{Role: "tool", Content: "orphan", ToolCallID: "x"})
	for i := 0; i < maxHistory+10; i++ {
		a.hist = append(a.hist, message{Role: "user", Content: fmt.Sprintf("m%d", i)})
	}
	a.trimHistory()

	if len(a.hist) > maxHistory {
		t.Fatalf("history not trimmed: %d messages", len(a.hist))
	}
	if a.hist[0].Role != "system" {
		t.Fatalf("the system prompt must stay first, there is %q", a.hist[0].Role)
	}
	if a.hist[1].Role == "tool" {
		t.Fatal("an orphaned tool result was left at the start of the history")
	}
}

// --- Configuration ----------------------------------------------------------

func TestGetEnv(t *testing.T) {
	t.Setenv("STARLIGHT_TEST_ENV", "  value  ")
	if got := getEnv("STARLIGHT_TEST_ENV", "fallback"); got != "value" {
		t.Fatalf("getEnv = %q", got)
	}
	t.Setenv("STARLIGHT_TEST_ENV", "   ")
	if got := getEnv("STARLIGHT_TEST_ENV", "fallback"); got != "fallback" {
		t.Fatalf("a blank value must fall back to the default, got %q", got)
	}
}

func TestToolDefinitions(t *testing.T) {
	tools := toolDefinitions()
	if len(tools) != 2 {
		t.Fatalf("2 tools were expected, there are %d", len(tools))
	}
	names := []string{tools[0].Function.Name, tools[1].Function.Name}
	if names[0] != "read_file" || names[1] != "run_command" {
		t.Fatalf("unexpected names: %v", names)
	}
	for _, h := range tools {
		if h.Type != "function" || h.Function.Description == "" || h.Function.Parameters["type"] != "object" {
			t.Fatalf("incomplete definition for %s", h.Function.Name)
		}
	}
}

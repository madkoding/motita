package llm

// The text path must not throw away a reply that arrived as tool calls.
//
// Measured on a real run: every phase of the agent asks for a JSON object as TEXT
// (`Complete`, not `CompleteTools`), and the provider answered one of those calls with a
// tool call and no text. `callOpenAI` guarded only on the TEXT being empty:
//
//	if strings.TrimSpace(choice.Message.Content) == "" {
//
// so the reply was reported as "no usable content (finish_reason=\"tool_calls\" with 1 tool
// call(s) ...)" — the message counted a call it had just discarded — and the run failed
// with "could not obtain the action from the LLM" after three pointless retries.
//
// The contract these tests fix:
//
//  1. A reply that carries tool calls is NOT empty, whatever the text says. It is reported
//     with its calls intact, so a caller can decide what to do with them.
//  2. A reply that carries NEITHER text nor calls is still an error, and its reason still
//     names the cause.
//  3. The count in that reason is the count that was there. "1 tool call(s)" must never
//     describe a reply with zero.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// openAIAnswerServer answers every completion with the given message body, verbatim.
func openAIAnswerServer(t *testing.T, message map[string]any, finishReason string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"choices": []any{map[string]any{
				"message":       message,
				"finish_reason": finishReason,
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

// textClient builds a client pointed at srv, with ONE attempt so a failure is immediate.
func textClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	l, _ := logx.New(logx.Options{Level: logx.Error, Console: false})
	cfg := config.LLM{
		Provider:       "openai",
		Model:          "test",
		APIKey:         "key",
		BaseURL:        srv.URL,
		MaxTokens:      64,
		MaxAttempts:    1,
		BackoffInitial: time.Millisecond,
		BackoffMax:     time.Millisecond,
		Timeout:        5 * time.Second,
	}
	c, err := New(cfg, l)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestATextCallWithToolCallsAndNoTextIsNotReportedAsEmpty: the defect. The provider sent a
// call; the text path must not call that "no usable content" and discard it.
//
// This is the contract that stops the reported failure from recurring: whatever the agent
// does with the calls, the reply itself has to survive the parser.
func TestATextCallWithToolCallsAndNoTextIsNotReportedAsEmpty(t *testing.T) {
	srv := openAIAnswerServer(t, map[string]any{
		"content": nil,
		"tool_calls": []any{map[string]any{
			"id":   "call_1",
			"type": "function",
			"function": map[string]any{
				"name":      "lookup",
				"arguments": `{"q":"x"}`,
			},
		}},
	}, "tool_calls")
	defer srv.Close()

	_, err := textClient(t, srv).Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("the text path cannot honour a tool call, so it must say so — but it must NOT say the reply was empty")
	}
	// The message must be TRUE about what arrived. "no usable content" is the shape that
	// sent a real run into three retries; the calls were there and the parser dropped them.
	if strings.Contains(err.Error(), "1 tool call(s) and 0 byte(s) of text: the provider reported calls that did not arrive") {
		t.Errorf("the reply carried a call; the message must not claim the calls did not arrive: %v", err)
	}
	if !strings.Contains(err.Error(), "lookup") && !strings.Contains(err.Error(), "tool call") {
		t.Errorf("the message must name what actually arrived, got: %v", err)
	}
}

// TestATextCallWithNoTextAndNoCallsIsStillAnError: the honest empty reply keeps failing,
// and its reason still names the cause.
func TestATextCallWithNoTextAndNoCallsIsStillAnError(t *testing.T) {
	srv := openAIAnswerServer(t, map[string]any{"content": "", "tool_calls": []any{}}, "stop")
	defer srv.Close()

	_, err := textClient(t, srv).Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("a reply with no text and no calls is an error")
	}
	if !strings.Contains(err.Error(), "no usable content") {
		t.Errorf("the reason must say the reply carried nothing usable, got: %v", err)
	}
	if !strings.Contains(err.Error(), `"stop"`) {
		t.Errorf("the reason must quote what the provider said, got: %v", err)
	}
}

// TestTheReasonCountsWhatWasThere: the count in the message is the count that arrived.
// A message saying "1 tool call(s)" about a reply with zero is a message that cannot be
// acted on, and it is how the original defect read.
func TestTheReasonCountsWhatWasThere(t *testing.T) {
	cases := []struct {
		name    string
		message map[string]any
		want    string
		notWant string
	}{
		{
			name:    "no calls at all",
			message: map[string]any{"content": "", "tool_calls": []any{}},
			want:    "0 tool call(s)",
			notWant: "1 tool call(s)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := openAIAnswerServer(t, tc.message, "tool_calls")
			defer srv.Close()
			_, err := textClient(t, srv).Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the reason must say %q, got: %v", tc.want, err)
			}
			if strings.Contains(err.Error(), tc.notWant) {
				t.Errorf("the reason must NOT say %q — that is a count that never arrived: %v", tc.notWant, err)
			}
		})
	}
}

// TestATextCallWithARealAnswerStillWorks: the guard must not have been loosened past
// usefulness. Normal text comes back unchanged.
func TestATextCallWithARealAnswerStillWorks(t *testing.T) {
	srv := openAIAnswerServer(t, map[string]any{"content": "the answer"}, "stop")
	defer srv.Close()

	got, err := textClient(t, srv).Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("a normal answer must come back: %v", err)
	}
	if got != "the answer" {
		t.Errorf("got %q, want %q", got, "the answer")
	}
}

// TestDescribeCallsNamesWhatItHas: the message names the tool, which is the actionable fact.
// The two edge cases are covered because both produce a DIFFERENT sentence, and an empty
// name in particular must not print a blank where a tool should be.
func TestDescribeCallsNamesWhatItHas(t *testing.T) {
	cases := []struct {
		name  string
		calls []ToolCall
		want  string
	}{
		{"a named call", []ToolCall{{Function: FunctionCall{Name: "search_skills"}}}, "search_skills"},
		{"several", []ToolCall{
			{Function: FunctionCall{Name: "a"}},
			{Function: FunctionCall{Name: "b"}},
		}, "a, b"},
		// A provider that sends a call without a name must not produce a blank in the
		// middle of the sentence: "(unnamed)" says what it is.
		{"no name at all", []ToolCall{{}}, "(unnamed)"},
		{"nothing", nil, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeCalls(tc.calls); got != tc.want {
				t.Errorf("describeCalls() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestATextCallWhoseOnlyCallHasNoNameStillReportsIt: the unnamed path reaches the error
// message rather than being dropped, so the reply is never silently lost.
func TestATextCallWhoseOnlyCallHasNoNameStillReportsIt(t *testing.T) {
	srv := openAIAnswerServer(t, map[string]any{
		"content":    nil,
		"tool_calls": []any{map[string]any{"id": "call_1", "type": "function"}},
	}, "tool_calls")
	defer srv.Close()

	_, err := textClient(t, srv).Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("a call with no text must still be reported, named or not")
	}
	if !strings.Contains(err.Error(), "(unnamed)") {
		t.Errorf("an unnamed call must say so rather than print a blank, got: %v", err)
	}
	if !strings.Contains(err.Error(), "1 tool call(s)") {
		t.Errorf("the count must be right, got: %v", err)
	}
}

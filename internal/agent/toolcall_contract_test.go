package agent

// The contract that stops the reported failure from coming back.
//
// The user's run died with:
//
//	could not obtain the action from the LLM: all 3 attempts were exhausted: OpenAI returned
//	no usable content (finish_reason="tool_calls" with 1 tool call(s) and 0 byte(s) of text:
//	the provider reported calls that did not arrive, which is a parsing failure on this side
//
// Three things were wrong with that, and each one has a test here:
//
//  1. The reply CARRIED a call. The parser counted it and then discarded it, and the message
//     said the calls "did not arrive" — the opposite of what the same sentence reported.
//  2. It was retried three times. A provider that routes its answer through a tool returns
//     the same thing on the second and third ask, so the two extra calls were pure waste.
//  3. The reader was told to look for a parsing bug on our side. The actionable fact is
//     WHICH tool the provider used, because that is what the prompt or the provider's
//     routing has to answer for.
//
// The agent's own message is asserted too, because that is where the user read "could not
// obtain the action" with no cause attached.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// toolCallAnswerServer answers every completion with a tool call and no text, counting the
// calls so a test can prove how many were made.
func toolCallAnswerServer(t *testing.T, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		body := map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": nil,
					"tool_calls": []any{map[string]any{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      "search_skills",
							"arguments": `{"q":"the work"}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

// TestAReplyThatIsAToolCallNamesItAndIsNotRetried: the contract. One attempt, the tool
// named, and never the phrase that blamed a parse defect.
func TestAReplyThatIsAToolCallNamesItAndIsNotRetried(t *testing.T) {
	var calls int32
	srv := toolCallAnswerServer(t, &calls)
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "exit 0"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, func(c *config.Config) {
		// Three attempts, which is what the reported run had. The fix must spend ONE.
		c.LLM.MaxAttempts = 3
		c.LLM.BackoffInitial = time.Millisecond
		c.LLM.BackoffMax = time.Millisecond
	})

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	err := e.agent.Run(context.Background())
	if err == nil {
		t.Fatal("a provider that only sends tool calls cannot complete the task, so the run must fail")
	}
	if result == nil {
		t.Fatal("no result")
	}

	// 1. The cause is in the user's message, and it names the tool.
	if !strings.Contains(result.Reason, "search_skills") {
		t.Errorf("the reason must name the tool the provider used, so a reader can act on it.\n"+
			"got: %s", result.Reason)
	}
	// 2. The false claim is gone. This exact sentence is what the user reported.
	if strings.Contains(result.Reason, "did not arrive") {
		t.Errorf("the reply carried the call; the message must not say the calls did not arrive.\n"+
			"got: %s", result.Reason)
	}
	if strings.Contains(result.Reason, "parsing failure on this side") {
		t.Errorf("the failure is not a parse defect on our side — the reply was well-formed and "+
			"carried a call.\ngot: %s", result.Reason)
	}
	// 3. Bounded: the ask, plus ONE corrective ask that says what went wrong. Not the three
	//    identical retries the reported run spent. The analysis phase fails first.
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("the provider was asked %d times; want the ask and ONE corrective retry", got)
	}
}

// TestAToolCallForTextIsAnsweredOnceTheModelIsTold: the reported "I ask and the agent does
// nothing". A model that answers a text phase with a tool call is told so, once, and the task
// then runs. Before, every subtask died on round 1 with no work done.
func TestAToolCallForTextIsAnsweredOnceTheModelIsTold(t *testing.T) {
	fake := &fakeLLMServer{actionsPerAttempt: [][]string{{"echo hello > ok.txt"}}}
	inner := fake.handler(t)
	var toolCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if !bytes.Contains(raw, []byte("Your last reply was a tool call")) {
			atomic.AddInt32(&toolCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"content":null,"tool_calls":` +
				`[{"id":"c","type":"function","function":{"name":"search_skills","arguments":"{}"}}]}}]}`))
			return
		}
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", "test -s ok.txt"},
		Timeout: 10 * time.Second, ExpectExit: 0}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err != nil || result == nil || !result.Pass {
		t.Fatalf("the task must complete once the model is told; err=%v result=%+v", err, result)
	}
	if atomic.LoadInt32(&toolCalls) == 0 {
		t.Fatal("the fixture never answered with a tool call, so nothing was proven")
	}
}

// TestAWellFormedTextReplyStillRunsTheTask: the guard must not have been tightened past
// usefulness. An ordinary reply completes the task exactly as before.
func TestAWellFormedTextReplyStillRunsTheTask(t *testing.T) {
	fake := &fakeLLMServer{actionsPerAttempt: [][]string{{"echo hello > ok.txt"}}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "test -s ok.txt"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, nil)

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil || !result.Pass {
		t.Fatalf("a normal reply must still complete the task: %+v", result)
	}
}

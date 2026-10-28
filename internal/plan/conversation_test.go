package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/session"
)

// The history used to be trimmed by COUNT: the oldest messages were dropped and nothing
// replaced them, so an agent that had filled its context forgot the task it was given. It
// is now compacted: what is removed is summarised and carried forward, and the tests below
// assert that property rather than the arithmetic of a slice.

// TestTheConversationOutlivesOneRun: two runs against the same planner must see each
// other. Without this the agent cannot be asked a follow-up question, which is the
// difference between a chat and a command line.
func TestTheConversationOutlivesOneRun(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{content: "the first answer"},
		{content: "the second answer"},
	})
	defer srv.Close()

	p := New(newClient(t, srv), a)
	if _, err := p.Run(context.Background(), "first question"); err != nil {
		t.Fatal(err)
	}
	before := p.Session().Len()
	if _, err := p.Run(context.Background(), "second question"); err != nil {
		t.Fatal(err)
	}

	// The second run continues the same conversation: the first exchange is still there.
	if got := p.Session().Len(); got <= before {
		t.Errorf("the conversation must grow across runs: %d messages, was %d", got, before)
	}
	history := p.Session().Messages()
	found := false
	for _, m := range history {
		if strings.Contains(m.Content, "first question") {
			found = true
		}
	}
	if !found {
		t.Errorf("the second run must still see the first question, got %+v", history)
	}
}

// TestATurnCompactsBeforeTheWindowOverflows: the compaction happens inside the run, at the
// boundary, so the agent never reaches the provider with a context it will refuse.
func TestATurnCompactsBeforeTheWindowOverflows(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{content: "an answer"},
		{content: "the summary of everything before"},
		{content: "another answer"},
	})
	defer srv.Close()

	p := New(newClient(t, srv), a).
		WithSessionPolicy("gpt-4o", 400, 50, 0.5, 2)

	// A conversation big enough to cross the trigger, then one more run.
	sess := p.Session()
	for i := 0; i < 60; i++ {
		sess.Append(llm.Message{Role: "user", Content: strings.Repeat("word ", 30)})
	}
	if !sess.NeedsCompaction() {
		t.Fatalf("the fixture must cross the trigger, at %.0f%%", sess.Used()*100)
	}

	if _, err := p.Run(context.Background(), "and now this"); err != nil {
		t.Fatal(err)
	}
	if sess.Compactions() == 0 {
		t.Error("the run must have compacted the context before calling the model")
	}
	if sess.Summary() == "" {
		t.Error("the compaction must carry a summary forward")
	}
}

// TestTheCompactionIsReportedToTheInterface: a conversation that gets shorter with no
// explanation looks like an agent that lost its place.
func TestTheCompactionIsReportedToTheInterface(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{content: "the summary"},
		{content: "an answer"},
	})
	defer srv.Close()

	var trace strings.Builder
	p := New(newClient(t, srv), a).
		WithSessionPolicy("gpt-4o", 400, 50, 0.5, 2).
		WithTrace(func(format string, args ...any) { fmt.Fprintf(&trace, format, args...) })

	sess := p.Session()
	for i := 0; i < 60; i++ {
		sess.Append(llm.Message{Role: "user", Content: strings.Repeat("word ", 30)})
	}
	if _, err := p.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(trace.String(), "context compacted") {
		t.Errorf("the interface must be told, got:\n%s", trace.String())
	}
}

// TestCompactionCarriesTheTaskNotJustTheTail: the whole reason for summarising instead of
// truncating. What the user asked for must survive the fold.
func TestCompactionCarriesTheTaskNotJustTheTail(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{content: "the account: the user asked to count .txt files; the answer was 0"},
		{content: "an answer"},
	})
	defer srv.Close()

	p := New(newClient(t, srv), a).
		WithSessionPolicy("gpt-4o", 400, 50, 0.5, 2)
	sess := p.Session()

	// The task is at the START of the conversation, which is what gets folded.
	sess.Append(llm.Message{Role: "user", Content: "count the .txt files in the home directory"})
	for i := 0; i < 60; i++ {
		sess.Append(llm.Message{Role: "user", Content: strings.Repeat("word ", 30)})
	}
	if _, err := p.Run(context.Background(), "and the .md?"); err != nil {
		t.Fatal(err)
	}

	// The summary is in the request the model receives for the NEXT turn.
	msgs := sess.Messages()
	carried := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "count .txt files") {
			carried = true
		}
	}
	if !carried {
		t.Errorf("the task must survive the compaction, got:\n%+v", msgs)
	}
}

// TestAFailedCompactionStopsTheRunRatherThanLosingContext: carrying on with a context that
// is about to be refused turns a recoverable condition into a failed task mid-answer.
func TestAFailedCompactionStopsTheRunRatherThanLosingContext(t *testing.T) {
	a, _ := makeAgent(t, true)
	// The summariser is the engine, and this server refuses every request, so the
	// compaction — which is a model call like any other — cannot be done. The mock used by
	// the other tests always answers something, which would make this test vacuous.
	srv := llmServerStatus(t, http.StatusInternalServerError)
	defer srv.Close()

	p := New(newClient(t, srv), a).
		WithSessionPolicy("gpt-4o", 400, 50, 0.5, 2)
	sess := p.Session()
	for i := 0; i < 60; i++ {
		sess.Append(llm.Message{Role: "user", Content: strings.Repeat("word ", 30)})
	}
	before := sess.Len()

	_, err := p.Run(context.Background(), "continue")
	if err == nil {
		t.Fatal("a context that cannot be compacted must stop the run, not proceed")
	}
	if !strings.Contains(err.Error(), "could not be compacted") {
		t.Errorf("the error must say what happened, got %v", err)
	}
	if sess.Len() < before {
		t.Error("the history must not be shortened by a failed compaction")
	}
}

// TestThePlannerHonoursTheConfiguredWindow: the operator knows a local server configured
// lower than the model's published window, so the explicit value wins.
func TestThePlannerHonoursTheConfiguredWindow(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, nil)
	defer srv.Close()
	p := New(newClient(t, srv), a).WithSessionPolicy("gpt-4o", 3000, 100, 0.7, 4)
	sess := p.Session()

	if sess.Window != 3000 {
		t.Errorf("window = %d, want the configured 3000 rather than gpt-4o's published one", sess.Window)
	}
	if sess.Reserve != 100 || sess.CompactAt != 0.7 || sess.KeepRecent != 4 {
		t.Errorf("the policy must come from the configuration: reserve=%d at=%.1f keep=%d",
			sess.Reserve, sess.CompactAt, sess.KeepRecent)
	}
}

// TestWithoutAPolicyTheWindowComesFromTheModel: an unconfigured planner still compacts at a
// sensible place rather than not at all.
func TestWithoutAPolicyTheWindowComesFromTheModel(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, nil)
	defer srv.Close()
	p := New(newClient(t, srv), a).WithSessionPolicy("llama3.1:70b", 0, 0, 0, 0)
	sess := p.Session()

	if sess.Window != 128000 {
		t.Errorf("window = %d, want the model's 128000", sess.Window)
	}
	if sess.Reserve <= 0 || sess.CompactAt <= 0 {
		t.Errorf("the defaults must apply: reserve=%d at=%.1f", sess.Reserve, sess.CompactAt)
	}
}

// TestTheContextIsCompactedBetweenToolRounds: the tool results are what makes a plan run
// grow fastest, so the check has to happen again after each round. Checking only at the
// start of a run means a single task with many tools can overflow in the middle of it —
// which is the moment the agent is least able to recover.
func TestTheContextIsCompactedBetweenToolRounds(t *testing.T) {
	a, fake := makeAgent(t, true)
	// The output is large enough that three tool rounds cross the 45% trigger
	// even after the system prompt (which includes the embedded soul, ~1900 tokens)
	// is counted. The window is sized so the prompt alone stays below the trigger
	// and the tool output pushes it over — the same relationship the original
	// fixture had with its smaller prompt.
	fake.defaultOutput = strings.Repeat("the quick brown fox jumps over the lazy dog. ", 300)

	// The steps alternate: a tool round, then an answer, then a tool round again. The
	// summariser is the same engine, so its calls are interleaved with the loop's — and the
	// server answers every request from the same queue, in order.
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "true"})}},
		{content: "account of the first round"},
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c2", map[string]string{"command": "true"})}},
		{content: "account of the second round"},
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c3", map[string]string{"command": "true"})}},
		{content: "account of the third round"},
		{content: "the final answer"},
	})
	defer srv.Close()

	// The window is MEASURED, not chosen. Two conditions have to hold at once for this branch
	// to run at all, and they pull in opposite directions:
	//
	//   - large enough to hold the system prompt plus the two messages the policy keeps
	//     verbatim. Below that floor compaction cannot run — there is nothing to fold — and
	//     the run is correctly reported as unable to compact. That is a real answer, and it
	//     is what a window that is simply too small produces, but it is not this branch.
	//   - small enough that the tool rounds alone cross the trigger, or the check is never
	//     reached.
	//
	// Measured on this test's own fixture: the original 3200 window with a ~500-token
	// prompt satisfied both conditions. The soul (embedded personality) enlarged the
	// prompt from ~500 to ~1900 tokens, so the window was raised to 8000 (8400 once the memory-saving rule joined the prompt) and the tool
	// output scaled to ~3400 tokens per round, keeping the same proportional relationship
	// that let the tool rounds cross the 45% trigger while the prompt alone stayed below it.
	p := New(newClient(t, srv), a).
		WithLoops(8).
		WithSessionPolicy("gpt-4o", 8400, 100, 0.45, 2)

	out, err := p.Run(context.Background(), "a task that uses several tools")
	if err != nil {
		t.Fatalf("the run must survive the compaction: %v", err)
	}
	if out == "" {
		t.Error("the run must still produce an answer")
	}
	if p.Session().Compactions() == 0 {
		t.Error("the conversation must have been compacted during the tool rounds")
	}
}

// TestWithSessionContinuesInAnExistingConversation: the caller owns the session, so a
// conversation can be carried across planners — which is what the interface does, since it
// builds a planner per turn.
func TestWithSessionContinuesInAnExistingConversation(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{{content: "an answer"}})
	defer srv.Close()

	// A conversation that already has history, as a second turn would find it.
	existing := session.New("gpt-4o", SystemPrompt, 128000)
	existing.Append(llm.Message{Role: "user", Content: "the first question"})
	existing.Append(llm.Message{Role: "assistant", Content: "the first answer"})

	p := New(newClient(t, srv), a).WithSession(existing)
	if p.Session() != existing {
		t.Fatal("WithSession must adopt the session it was given")
	}

	if _, err := p.Run(context.Background(), "the second question"); err != nil {
		t.Fatal(err)
	}

	// The first exchange is still there, and the second was added to it.
	msgs := p.Session().Messages()
	found := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, "first question") || strings.Contains(m.Content, "second question") {
			found++
		}
	}
	if found != 2 {
		t.Errorf("both questions must be in the conversation, found %d in %+v", found, msgs)
	}
}

// TestAFailedCompactionBetweenRoundsStopsTheRun: the same rule as at the start of a run. A
// context that cannot be compacted must stop the agent rather than be carried into a request
// the provider will refuse — and mid-task is exactly where that would be least recoverable.
func TestAFailedCompactionBetweenRoundsStopsTheRun(t *testing.T) {
	a, fake := makeAgent(t, true)
	// The tool output is what fills the window, so the compaction is needed after one round.
	fake.defaultOutput = strings.Repeat("output that fills the window ", 60)

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := ioMustRead(r.Body)
		// The summariser is the engine, so its call arrives on the same endpoint. It is the
		// one that fails: failing the tool round instead would abort the run before the
		// compaction was ever attempted, and the branch under test would never execute.
		isSummary := bytes.Contains(body, []byte("working memory of an autonomous agent"))
		if isSummary {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"the summariser is down"}}`)
			return
		}
		stream := bytes.Contains(body, []byte(`"stream":true`))
		_ = stream
		call := toolCall("execute_command", "c1", map[string]string{"command": "true"})
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"delta":         map[string]any{"tool_calls": []map[string]any{call}},
				"finish_reason": "tool_calls",
			}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"finish_reason": "tool_calls",
			"message":       map[string]any{"role": "assistant", "tool_calls": []map[string]any{call}},
		}}})
	}))
	defer srv.Close()

	// The window is sized so the seeded history sits BELOW the trigger and the tool output
	// pushes it over: the check at the start of the run then passes, and the one after the
	// tool round is the first to fire. A window too small for the seed makes the opening
	// check fail first, and the branch under test then never executes. The window must also
	// accommodate the system prompt, which includes the soul (the embedded personality is
	// larger than the old fixed prompt, so the window is sized to leave room for it).
	p := New(newClient(t, srv), a).
		WithLoops(6).
		WithSessionPolicy("gpt-4o", 8000, 100, 0.5, 2)

	sess := p.Session()
	for i := 0; i < 4; i++ {
		sess.Append(llm.Message{Role: "user", Content: "earlier turn"})
		sess.Append(llm.Message{Role: "assistant", Content: "earlier answer"})
	}
	if sess.NeedsCompaction() {
		t.Fatalf("the fixture must start below the trigger, it is at %.0f%%", sess.Used()*100)
	}

	_, err := p.Run(context.Background(), "a task whose tool output fills the window")
	if err == nil {
		t.Fatal("a compaction that fails between rounds must stop the run")
	}
	if !strings.Contains(err.Error(), "could not be compacted") {
		t.Errorf("the error must say what happened, got %v", err)
	}
	if sess.Compactions() != 0 {
		t.Error("a failed compaction must not be counted")
	}
}

// TestTheForcedAnswerIsKeptInTheSession: the answer produced after the tool-call limit is
// part of the conversation. Without it the session ended on the limit instruction and the
// next turn opened with two user messages in a row.
func TestTheForcedAnswerIsKeptInTheSession(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bytes.Contains(ioMustRead(r.Body), []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			calls := []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "true"})}
			delta := map[string]any{"tool_calls": calls}
			chunk, _ := json.Marshal(
				map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "tool_calls"}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"the forced answer"}}]}`)
	}))
	defer srv.Close()
	p := New(newClient(t, srv), a).WithLoops(1)
	answer, err := p.Run(context.Background(), "ask")
	if err != nil || answer != "the forced answer" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	msgs := p.Session().Messages()
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" || last.Content != "the forced answer" {
		t.Errorf("the session must end on the answer the user read, got %s: %q", last.Role, last.Content)
	}
}

package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
)

// Summariser stubs, so the compaction path — the one that must not lose the task — is
// testable without a provider.

type stubSummariser struct {
	out    string
	err    error
	calls  int
	lastIn []llm.Message
}

func (s *stubSummariser) Complete(_ context.Context, messages []llm.Message) (string, error) {
	s.calls++
	s.lastIn = messages
	return s.out, s.err
}

// user and assistant shortcuts: the tests are about what happens to the history, not
// about the shape of a message.
func user(text string) llm.Message      { return llm.Message{Role: "user", Content: text} }
func assistant(text string) llm.Message { return llm.Message{Role: "assistant", Content: text} }
func toolResult(text string) llm.Message {
	return llm.Message{Role: "tool", Content: text, ToolCallID: "c1"}
}

// pad makes a message big enough to matter against a small window.
func pad(n int) string { return strings.Repeat("word ", n) }

// TestAnUnrecognisedModelGetsTheSafeFloor: guessing too high is what makes an agent fail
// mid-task, so a model nobody has heard of gets the smallest window rather than a
// generous one.
func TestAnUnrecognisedModelGetsTheSafeFloor(t *testing.T) {
	for _, model := range []string{"", "mystery-model-9000", "   ", "acme/unknown"} {
		if got := ContextWindow(model); got != DefaultContextWindow {
			t.Errorf("ContextWindow(%q) = %d, want the floor %d", model, got, DefaultContextWindow)
		}
	}
}

// TestProviderVariantsMatchTheirFamily: providers append suffixes and prefix vendors, and
// an exact-match table would miss every one of them and fall back to the floor.
func TestProviderVariantsMatchTheirFamily(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  int
	}{
		{"gpt-4o-mini", 128000},
		{"gpt-4o", 128000},
		{"openai/gpt-4o", 128000},
		{"llama3.1:70b", 128000},
		{"qwen2.5-72b", 32768},
		{"deepseek-v4.1-flash", 65536},
		{"claude-3-5-sonnet", 200000},
		// The Claude Code aliases the claude-code provider uses.
		{"sonnet", 200000},
		{"opus", 200000},
		{"haiku", 200000},
		{"fable", 200000},
		{"sonnet[1m]", 200000},
	} {
		if got := ContextWindow(tc.model); got != tc.want {
			t.Errorf("ContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

// TestTheLongestPrefixWins: llama3.1 and llama3 have different windows, so a model named
// llama3.1-8b must take the more specific one. Taking the shorter prefix would compact a
// 128k model as if it were 8k.
func TestTheLongestPrefixWins(t *testing.T) {
	if got := ContextWindow("llama3.1-8b"); got != 128000 {
		t.Errorf("ContextWindow(llama3.1-8b) = %d, want 128000 (the specific prefix, not llama3's 8192)", got)
	}
}

// TestTokenEstimateGrowsWithContent: the estimate is the trigger for compaction, so it has
// to be monotonic — a longer history must never measure smaller.
func TestTokenEstimateGrowsWithContent(t *testing.T) {
	small := []llm.Message{user("hello")}
	large := []llm.Message{user(pad(1000))}

	if EstimateTokens(small) >= EstimateTokens(large) {
		t.Errorf("a longer message must estimate higher: %d vs %d",
			EstimateTokens(small), EstimateTokens(large))
	}
}

// TestTokenEstimateCountsToolCalls: an agent's history is mostly tool traffic, and a count
// that ignored the calls and their arguments would under-report exactly the material that
// fills the window.
func TestTokenEstimateCountsToolCalls(t *testing.T) {
	plain := []llm.Message{assistant("done")}
	withCall := []llm.Message{{
		Role:    "assistant",
		Content: "done",
		ToolCalls: []llm.ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: llm.FunctionCall{
				Name:      "read_file",
				Arguments: []byte(`{"path":"/home/madkoding/proyectos/motita/internal/tui/render.go"}`),
			},
		}},
	}}

	if EstimateTokens(withCall) <= EstimateTokens(plain) {
		t.Errorf("a tool call must add to the estimate: %d vs %d",
			EstimateTokens(withCall), EstimateTokens(plain))
	}
}

// TestTokenEstimateIsConservative: dense material — JSON, paths, code — costs closer to
// three characters per token than four, and the estimate must not pretend otherwise or the
// session would compact too late.
func TestTokenEstimateIsConservative(t *testing.T) {
	dense := `{"path":"/a/b/c.go","flags":["-n","-v"],"count":127}`
	prose := strings.Repeat("a", len(dense))

	if EstimateTokens([]llm.Message{{Role: "user", Content: dense}}) <=
		EstimateTokens([]llm.Message{{Role: "user", Content: prose}}) {
		t.Error("dense material must be estimated as more expensive than prose of the same length")
	}
}

// TestAnEmptyMessageCostsSomething: the framing overhead is real, and a list of empty
// messages measuring as zero would let the history grow forever.
func TestAnEmptyMessageCostsSomething(t *testing.T) {
	if n := EstimateTokens([]llm.Message{{Role: "user"}, {Role: "assistant"}}); n <= 0 {
		t.Errorf("messages cost the protocol overhead even when empty, got %d", n)
	}
}

// TestMessagesOpensWithTheSystemPromptAndTheSummary: the request the model receives has a
// fixed shape, and the summary comes before the conversation so it reads as background.
func TestMessagesOpensWithTheSystemPromptAndTheSummary(t *testing.T) {
	s := New("gpt-4o", "you are an agent", 0)
	s.Append(user("hello"))
	s.summary = "earlier: the user asked about files"

	msgs := s.Messages()
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (system, summary, conversation)", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "you are an agent" {
		t.Errorf("the first message must be the system prompt, got %+v", msgs[0])
	}
	if msgs[1].Role != "system" || !strings.Contains(msgs[1].Content, "earlier: the user asked") {
		t.Errorf("the summary must follow the prompt and carry its content, got %+v", msgs[1])
	}
	if msgs[2].Content != "hello" {
		t.Errorf("the conversation must come last, got %+v", msgs[2])
	}
}

// TestAnEmptySessionStillSendsThePrompt: a session with nothing in it must not send an
// empty request.
func TestAnEmptySessionStillSendsThePrompt(t *testing.T) {
	s := New("gpt-4o", "the contract", 0)
	msgs := s.Messages()
	if len(msgs) != 1 || msgs[0].Content != "the contract" {
		t.Errorf("got %+v, want only the system prompt", msgs)
	}
}

// TestTheReserveIsKeptOutOfTheBudget: the session must compact while the model still has
// room to answer, so the usable budget is the window minus the reserve.
func TestTheReserveIsKeptOutOfTheBudget(t *testing.T) {
	s := New("gpt-4o", "", 0)
	if s.Usable() >= s.Window {
		t.Errorf("the usable budget must be smaller than the window: %d vs %d", s.Usable(), s.Window)
	}
	if got, want := s.Usable(), s.Window-DefaultReserve; got != want {
		t.Errorf("usable = %d, want %d", got, want)
	}

	// A window smaller than the reserve is a misconfiguration, and a negative budget would
	// make the trigger arithmetic meaningless.
	tiny := New("gpt-4o", "", 100)
	if tiny.Usable() < 1 {
		t.Errorf("the budget must stay positive even when the window is smaller than the reserve, got %d", tiny.Usable())
	}
}

// TestCompactionTriggersBeforeTheWindowIsFull: the whole point. Compacting at the limit
// means compacting after the provider has already refused.
func TestCompactionTriggersBeforeTheWindowIsFull(t *testing.T) {
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 2

	if s.NeedsCompaction() {
		t.Error("an empty session must not need compaction")
	}
	// Fill past the trigger.
	for i := 0; i < 40; i++ {
		s.Append(user(pad(20)))
	}
	if !s.NeedsCompaction() {
		t.Errorf("a session at %.0f%% of its budget must need compaction", s.Used()*100)
	}
	if s.Used() < 0.5 {
		t.Errorf("the fixture must actually cross the trigger, got %.0f%%", s.Used()*100)
	}
}

// TestAnInvalidTriggerFallsBackToTheDefault: a configuration of zero or a nonsense
// fraction must not disable compaction, which is the failure that would let the context
// overflow in silence.
func TestAnInvalidTriggerFallsBackToTheDefault(t *testing.T) {
	for _, at := range []float64{0, -1, 2, 1.5} {
		s := New("gpt-4o", "", 500)
		s.CompactAt = at
		for i := 0; i < 200; i++ {
			s.Append(user(pad(20)))
		}
		if !s.NeedsCompaction() {
			t.Errorf("CompactAt=%v disabled compaction at %.0f%% of the budget", at, s.Used()*100)
		}
	}
}

// TestCompactionKeepsTheTailVerbatim: the user is still talking about the last exchange, so
// summarising it is exactly the wrong thing to do.
func TestCompactionKeepsTheTailVerbatim(t *testing.T) {
	sum := &stubSummariser{out: "the account"}
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 3
	s.Summariser = sum

	for i := 0; i < 30; i++ {
		s.Append(user(fmt.Sprintf("message %d %s", i, pad(20))))
	}
	before := s.Len()
	if err := s.Compact(context.Background()); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}

	if s.Len() != 3 {
		t.Errorf("after compaction %d messages are held, want the 3 kept verbatim", s.Len())
	}
	if s.Len() >= before {
		t.Error("compaction must actually free room")
	}
	if s.Compactions() != 1 {
		t.Errorf("compactions = %d, want 1", s.Compactions())
	}
	if s.Summary() != "the account" {
		t.Errorf("summary = %q, want the summariser's output", s.Summary())
	}
	// The tail that stayed is the LAST of the conversation, not an arbitrary slice.
	last := s.Messages()[len(s.Messages())-1]
	if !strings.Contains(last.Content, "message 29") {
		t.Errorf("the kept tail must be the most recent messages, got %q", last.Content)
	}
}

// TestCompactionReplacesWhatItRemoved: the summary has to reach the request. Losing the
// removed messages AND the summary is the amnesia this exists to prevent.
func TestCompactionReplacesWhatItRemoved(t *testing.T) {
	sum := &stubSummariser{out: "the user asked to count .txt files and the answer was 0"}
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 2
	s.Summariser = sum

	for i := 0; i < 30; i++ {
		s.Append(user(fmt.Sprintf("message %d %s", i, pad(20))))
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}

	msgs := s.Messages()
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "count .txt files") {
			found = true
		}
	}
	if !found {
		t.Errorf("the summary must be part of the request, got:\n%+v", msgs)
	}
}

// TestTheSummaryCarriesForwardCumulatively: a second compaction must fold the earlier
// account into the new one, or the session becomes a stack of disconnected notes and the
// first summary is lost.
func TestTheSummaryCarriesForwardCumulatively(t *testing.T) {
	sum := &stubSummariser{out: "second account"}
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 2
	s.Summariser = sum
	s.summary = "first account"

	for i := 0; i < 30; i++ {
		s.Append(user(pad(20)))
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}

	// What was sent to the summariser must contain the previous account.
	sent := ""
	for _, m := range sum.lastIn {
		sent += m.Content
	}
	if !strings.Contains(sent, "first account") {
		t.Errorf("the previous summary must be folded in, not dropped:\n%s", sent)
	}
	if s.Summary() != "second account" {
		t.Errorf("summary = %q, want the new account", s.Summary())
	}
}

// TestCompactionLeavesTheHistoryAloneWhenItCannotSummarise: an over-long context is a
// provider error the caller can act on; a silently truncated one is a wrong answer nobody
// can trace. So a failed compaction must change nothing.
func TestCompactionLeavesTheHistoryAloneWhenItCannotSummarise(t *testing.T) {
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 2
	for i := 0; i < 30; i++ {
		s.Append(user(pad(20)))
	}
	before := s.Len()

	if err := s.Compact(context.Background()); !errors.Is(err, ErrNoSummariser) {
		t.Fatalf("compaction without a summariser must report it, got %v", err)
	}
	if s.Len() != before {
		t.Errorf("a failed compaction must not shorten the history: %d then %d", before, s.Len())
	}
	if !errors.Is(s.LastError(), ErrNoSummariser) {
		t.Errorf("LastError = %v, want the summariser error", s.LastError())
	}
	if s.Compactions() != 0 {
		t.Error("a failed compaction must not be counted")
	}
}

// TestAProviderFailureAlsoLeavesTheHistoryAlone: the same rule when the summariser exists
// but cannot answer.
func TestAProviderFailureAlsoLeavesTheHistoryAlone(t *testing.T) {
	sum := &stubSummariser{err: errors.New("connection reset")}
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 2
	s.Summariser = sum
	for i := 0; i < 30; i++ {
		s.Append(user(pad(20)))
	}
	before := s.Len()

	err := s.Compact(context.Background())
	if err == nil || !strings.Contains(err.Error(), "could not compact") {
		t.Fatalf("a provider failure must be reported, got %v", err)
	}
	if s.Len() != before {
		t.Errorf("the history must be untouched, %d then %d", before, s.Len())
	}
}

// TestAnEmptySummaryCountsAsAFailure: installing an empty account would delete the history
// it was supposed to preserve, which is strictly worse than not compacting at all.
func TestAnEmptySummaryCountsAsAFailure(t *testing.T) {
	sum := &stubSummariser{out: "   \n  "}
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 2
	s.Summariser = sum
	for i := 0; i < 30; i++ {
		s.Append(user(pad(20)))
	}
	before := s.Len()

	if err := s.Compact(context.Background()); err == nil {
		t.Fatal("an empty summary must be refused")
	}
	if s.Len() != before || s.Summary() != "" {
		t.Error("nothing must change when the summary is empty")
	}
}

// TestCompactionDropsAnOrphanedToolResult: a tool result whose assistant call was
// summarised away cannot open the remaining history, or the provider rejects the request.
func TestCompactionDropsAnOrphanedToolResult(t *testing.T) {
	sum := &stubSummariser{out: "account"}
	s := New("gpt-4o", "system", 0)
	s.KeepRecent = 2
	s.Summariser = sum

	// ...assistant call, tool result, then the tail. Cutting two from the end leaves the
	// tool result first.
	s.AppendAll(
		user("count the files"),
		assistant(""),
		toolResult("12 files"),
		assistant("there are 12"),
		user("and the .txt ones?"),
	)
	if err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(s.messages) > 0 && s.messages[0].Role == "tool" {
		t.Errorf("the history must not open with an orphaned tool result: %+v", s.messages[0])
	}
}

// TestCompactingAnEmptyConversationReportsIt: a window smaller than the tail that must stay
// verbatim cannot be fixed by summarising, and looping on it would be worse than saying so.
func TestCompactingAnEmptyConversationReportsIt(t *testing.T) {
	sum := &stubSummariser{out: "account"}
	s := New("gpt-4o", "system", 1)
	s.Reserve = 0
	s.CompactAt = 0.1
	s.KeepRecent = 6
	s.Summariser = sum
	s.Append(user("only one"))

	err := s.Compact(context.Background())
	if err == nil || !strings.Contains(err.Error(), "fewer than") {
		t.Errorf("a conversation shorter than the verbatim tail must be reported, got %v", err)
	}
}

// TestNoCompactionWhenThereIsRoom: the check must be cheap and must not rewrite a history
// that fits, because every summary costs a model call.
func TestNoCompactionWhenThereIsRoom(t *testing.T) {
	sum := &stubSummariser{out: "account"}
	s := New("gpt-4o", "system", 100000)
	s.Summariser = sum
	s.Append(user("a short question"))

	if err := s.Compact(context.Background()); err != nil {
		t.Errorf("nothing to do must not be an error, got %v", err)
	}
	if sum.calls != 0 {
		t.Error("a session with room must not call the summariser")
	}
	if s.Compactions() != 0 {
		t.Error("nothing was compacted")
	}
}

// TestTheTranscriptGivenToTheSummariserIncludesToolTraffic: the discovered facts live in
// the tool calls and their results, and a transcript that flattens them to their text loses
// the very thing being preserved.
func TestTheTranscriptGivenToTheSummariserIncludesToolTraffic(t *testing.T) {
	sum := &stubSummariser{out: "account"}
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 2
	s.Summariser = sum

	s.AppendAll(
		user("count the .txt files in the home directory"),
		llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID:       "c1",
				Type:     "function",
				Function: llm.FunctionCall{Name: "execute_command", Arguments: []byte(`{"command":"find . -name '*.txt' | wc -l"}`)},
			}},
		},
		toolResult("0"),
		assistant("there are none"),
	)
	for i := 0; i < 30; i++ {
		s.Append(user(pad(20)))
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}

	sent := ""
	for _, m := range sum.lastIn {
		sent += m.Content
	}
	for _, want := range []string{"execute_command", "find . -name", "tool result", "count the .txt files"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the transcript must carry %q, or the summary loses it:\n%s", want, sent)
		}
	}
}

// TestTheSummariserIsInstructedNotToInvent: the prompt is the only control over what the
// account contains, so the properties that matter are asserted against it rather than left
// to the text drifting.
func TestTheSummariserIsInstructedNotToInvent(t *testing.T) {
	for _, want := range []string{
		"What the user asked for", // the task
		"Decisions already taken", // so they are not revisited
		"Facts discovered",        // paths, numbers, errors
		"tried and failed",        // so it is not repeated
		"remains to be done",      // the thread to continue
		"Do not invent anything",  // the hallucination guard
	} {
		if !strings.Contains(compactPrompt, want) {
			t.Errorf("the compaction prompt must require %q", want)
		}
	}
}

// TestTheSummaryNoteFramesItAsEstablished: the model must treat the account as background,
// not as something the user just said, or it will answer the summary instead of the question.
func TestTheSummaryNoteFramesItAsEstablished(t *testing.T) {
	s := New("gpt-4o", "system", 0)
	s.summary = "the user asked about files"

	note := s.summaryNote()
	if !strings.Contains(note, "carried from earlier") || !strings.Contains(note, "established") {
		t.Errorf("the summary must be framed as established context:\n%s", note)
	}
	if !strings.Contains(note, "the user asked about files") {
		t.Error("the note must carry the account itself")
	}
}

// TestKeepRecentFallsBackToTheDefault: a session built by hand may carry a zero, and a
// zero would mean "summarise everything, every time" — including the exchange the user is
// in the middle of.
func TestKeepRecentFallsBackToTheDefault(t *testing.T) {
	sum := &stubSummariser{out: "account"}
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 0 // as a zero-value struct would have it
	s.Summariser = sum

	for i := 0; i < 40; i++ {
		s.Append(user(fmt.Sprintf("message %d %s", i, pad(20))))
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Len() != DefaultKeepRecent {
		t.Errorf("after compaction %d messages are held, want the default %d", s.Len(), DefaultKeepRecent)
	}
}

// TestANegativeKeepRecentIsAlsoTheDefault: a hand-built session may carry any nonsense, and
// the policy must not become "keep nothing".
func TestANegativeKeepRecentIsAlsoTheDefault(t *testing.T) {
	sum := &stubSummariser{out: "account"}
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = -3
	s.Summariser = sum

	for i := 0; i < 40; i++ {
		s.Append(user(pad(20)))
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Len() != DefaultKeepRecent {
		t.Errorf("%d messages held, want the default %d", s.Len(), DefaultKeepRecent)
	}
}

// TestAnUnknownRoleIsRenderedByItsName: the transcript is what the summariser reads, so a
// role this code does not know about must still appear rather than being dropped.
func TestAnUnknownRoleIsRenderedByItsName(t *testing.T) {
	got := renderMessage(llm.Message{Role: "developer", Content: "be terse"})
	if !strings.Contains(got, "developer:") || !strings.Contains(got, "be terse") {
		t.Errorf("an unknown role must be rendered by name, got %q", got)
	}
}

// TestAnEmptyAssistantTurnStillAppearsInTheTranscript: an assistant turn that only carried
// tool calls has no text, and skipping it would lose the fact that it decided to call a tool.
func TestAnEmptyAssistantTurnStillAppearsInTheTranscript(t *testing.T) {
	got := renderMessage(llm.Message{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{{
			ID: "c1", Type: "function",
			Function: llm.FunctionCall{Name: "list_directory", Arguments: []byte(`{"path":"."}`)},
		}},
	})
	if !strings.Contains(got, "called list_directory") {
		t.Errorf("the call must be in the transcript even with no text, got %q", got)
	}
}

// TestAnUnknownRoleWithNoText: the transcript must not produce a bare prefix with nothing
// after it, which the summariser would read as a broken line.
func TestAnUnknownRoleWithNoText(t *testing.T) {
	got := renderMessage(llm.Message{Role: "system"})
	if !strings.Contains(got, "system:") {
		t.Errorf("the role must still be named, got %q", got)
	}
}

// TestTheOrphanLoopEmptiesTheTailWhenItIsAllToolResults: keeping N messages verbatim can
// leave a tail made entirely of tool results, none of which has an assistant call in front
// of it. Every one must move into the summary rather than opening the remaining history,
// which is a request the provider rejects outright.
func TestTheOrphanLoopEmptiesTheTailWhenItIsAllToolResults(t *testing.T) {
	sum := &stubSummariser{out: "account"}
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 2
	s.Summariser = sum

	// The tail is padded first so the trigger is genuinely crossed — a session that does
	// not NEED compaction returns early and this test would silently assert nothing — and
	// then the last two messages are tool results, which are the orphans the loop consumes.
	for i := 0; i < 40; i++ {
		s.AppendAll(user(fmt.Sprintf("turn %d %s", i, pad(30))), assistant("noted"))
	}
	s.AppendAll(toolResult("file one"), toolResult("file two"))

	if err := s.Compact(context.Background()); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if len(s.messages) != 0 {
		t.Errorf("both kept messages were orphaned tool results, so the tail must be empty, got %+v", s.messages)
	}
	if s.Summary() != "account" {
		t.Errorf("the summary must still be installed, got %q", s.Summary())
	}
	// The orphaned results went into the transcript rather than being lost.
	sent := ""
	for _, m := range sum.lastIn {
		sent += m.Content
	}
	if !strings.Contains(sent, "file one") || !strings.Contains(sent, "file two") {
		t.Errorf("the orphaned results must be summarised, not dropped:\n%s", sent)
	}
}

// TestSnapshotReportsEveryFigureTheInterfaceNeeds: Snapshot is the only way a concurrent
// reader may look at a session — the fields are rewritten by compaction — so it has to carry
// every number a status bar shows. A missing field is a zero on screen.
func TestSnapshotReportsEveryFigureTheInterfaceNeeds(t *testing.T) {
	s := New("gpt-4o", "you are a careful agent", 1000)
	s.Append(llm.Message{Role: "user", Content: "count the files in this directory"})

	got := s.Snapshot()

	if got.Model != "gpt-4o" {
		t.Errorf("Model = %q", got.Model)
	}
	if got.Window != 1000 {
		t.Errorf("Window = %d, want the window it was built with", got.Window)
	}
	if got.Tokens <= 0 {
		t.Error("Tokens must count what the model would receive right now")
	}
	if got.Used <= 0 {
		t.Error("Used must be a fraction of the usable window")
	}
	if got.CompactAt != DefaultCompactAt {
		t.Errorf("CompactAt = %v, want the default", got.CompactAt)
	}
	if got.KeepRecent != DefaultKeepRecent {
		t.Errorf("KeepRecent = %d, want the default", got.KeepRecent)
	}
	if got.Messages != 1 {
		t.Errorf("Messages = %d, want 1: the system prompt is not a message", got.Messages)
	}
	if got.Folds != 0 {
		t.Errorf("Folds = %d, want 0 before any compaction", got.Folds)
	}
	if got.Summary != "" {
		t.Errorf("Summary = %q, want empty before any compaction", got.Summary)
	}
}

// TestSnapshotCarriesTheSummaryAfterAFold: the carried summary is what tells a user their
// earlier conversation is still known to the agent, in condensed form.
func TestSnapshotCarriesTheSummaryAfterAFold(t *testing.T) {
	// The window and the reserve are MEASURED against this fixture rather than chosen: with
	// 24 messages of these lengths and a 200-token reserve the trigger sits at 0.38, below the
	// 0.5 threshold, and the compaction correctly does nothing. A narrow window with a small
	// reserve is what actually crosses it.
	s := New("gpt-4o", "system", 400)
	s.Reserve = 50
	s.CompactAt = 0.5
	s.KeepRecent = 3
	s.Summariser = &stubSummariser{out: "the earlier conversation, summarised"}

	for i := 0; i < 12; i++ {
		s.Append(llm.Message{Role: "user", Content: "a message long enough to take up room"})
		s.Append(llm.Message{Role: "assistant", Content: "an answer of comparable length here"})
	}
	if !s.NeedsCompaction() {
		t.Fatalf("the fixture must cross the trigger: used=%.3f", s.Used())
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := s.Snapshot()
	if got.Folds != 1 {
		t.Errorf("Folds = %d, want 1", got.Folds)
	}
	if !strings.Contains(got.Summary, "summarised") {
		t.Errorf("Summary = %q, want the carried account", got.Summary)
	}
	if got.Messages >= 24 {
		t.Errorf("Messages = %d, want the folded messages gone", got.Messages)
	}
}

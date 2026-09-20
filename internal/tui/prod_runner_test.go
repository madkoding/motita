package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/plan"
	"github.com/madkoding/starlight/internal/sandbox"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

// These tests exercise the production runner through the paths the interactive
// session uses: the configuration the status line reads, the reasoning switch, and
// the mapping from a task result to the sentence shown in the chat.

// TestAppRunnerConfigReflectsTheReasoningSwitch: the status bar reads Config(), so
// the switch has to be visible there and not only inside the engine.
func TestAppRunnerConfigReflectsTheReasoningSwitch(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), nil, nil, logx.Global())
	if got := r.Config().LLM.Provider; got != config.Default().LLM.Provider {
		t.Errorf("Config() = %q, want the runner's own configuration", got)
	}

	r.SetReasoning("high")
	cfg := r.Config()
	if cfg.LLM.Reasoning.Level != "high" || !cfg.LLM.Reasoning.Enabled {
		t.Errorf("SetReasoning(high) left %+v", cfg.LLM.Reasoning)
	}
	// Turning it off also clears the enabled flag, so the request builder does not
	// keep sending a level the user asked to stop using.
	r.SetReasoning("off")
	cfg = r.Config()
	if cfg.LLM.Reasoning.Level != "off" || cfg.LLM.Reasoning.Enabled {
		t.Errorf("SetReasoning(off) left %+v", cfg.LLM.Reasoning)
	}
}

// TestAppRunnerEngineIsLazy: the TUI can start with no configuration file, which
// means no engine. Choosing plan or task then has to build one from the current
// configuration instead of dereferencing a nil client.
func TestAppRunnerEngineIsLazy(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())

	if _, err := r.engine(); err == nil {
		t.Error("an engine cannot be built without a key, and that must be an error")
	}

	// With a key it is built on demand, and an injected one is preferred.
	r.Cfg.LLM.APIKey = "k"
	built, err := r.engine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if built == nil {
		t.Fatal("engine returned nil without an error")
	}
	injected := &llm.Client{}
	r.Engine = injected
	got, err := r.engine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if got != injected {
		t.Error("an injected engine must be reused instead of building a second one")
	}
}

// TestAppRunnerRunTaskMapsEveryOutcome: the summary shown in the chat is built
// from the task result, and each shape of result has to produce a sentence a user
// can read.
func TestAppRunnerRunTaskMapsEveryOutcome(t *testing.T) {
	cases := []struct {
		name string
		tr   agent.TaskResult
		want string
	}{
		{
			name: "a synthesised summary wins",
			tr:   agent.TaskResult{Pass: true, Summary: "hay 20 archivos .txt"},
			want: "hay 20 archivos .txt",
		},
		{
			name: "a pass without a summary names its reason",
			tr:   agent.TaskResult{Pass: true, Reason: "2 checks passed"},
			want: "completed: 2 checks passed",
		},
		{
			name: "a pass also reports the final action",
			tr:   agent.TaskResult{Pass: true, Reason: "1 check passed", FinalAction: "echo done"},
			want: "final action: echo done",
		},
		{
			name: "the word none is not an action",
			tr:   agent.TaskResult{Pass: true, Reason: "1 check passed", FinalAction: "none"},
			want: "completed: 1 check passed",
		},
		{
			name: "a failure is reported as such",
			tr:   agent.TaskResult{Pass: false, Reason: "the check failed"},
			want: "failed: the check failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
			r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
				return &resultAgent{tr: tc.tr}
			}
			got, err := r.RunTask(context.Background(), "a task", func(string, ...any) {})
			if err != nil {
				t.Fatalf("RunTask: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("result = %q, want it to contain %q", got, tc.want)
			}
			if tc.name == "the word none is not an action" && strings.Contains(got, "none") {
				t.Errorf("the placeholder action must not be shown: %q", got)
			}
		})
	}
}

// TestAppRunnerRunTaskWithoutAResult: an agent that never reports anything still
// has to produce a sentence, because an empty chat message looks like a bug.
func TestAppRunnerRunTaskWithoutAResult(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return &resultAgent{silent: true}
	}
	got, err := r.RunTask(context.Background(), "a task", func(string, ...any) {})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got == "" {
		t.Error("a task that reports nothing must still yield an explanation")
	}
}

// TestAppRunnerRunTaskPropagatesAnAgentFailure: an error from the agent has to
// reach the chat, not be swallowed.
func TestAppRunnerRunTaskPropagatesAnAgentFailure(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return &resultAgent{err: errors.New("the agent could not start")}
	}
	_, err := r.RunTask(context.Background(), "a task", func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "could not start") {
		t.Errorf("err = %v, want the agent's failure", err)
	}
}

// TestAppRunnerRunTaskRejectsAnEmptyTask: the text source refuses an empty task,
// and the runner has to surface that instead of running the agent on nothing.
func TestAppRunnerRunTaskRejectsAnEmptyTask(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		t.Error("the agent must not be built for an empty task")
		return &resultAgent{}
	}
	if _, err := r.RunTask(context.Background(), "   ", func(string, ...any) {}); err == nil {
		t.Error("an empty task must be rejected")
	}
}

// TestAppRunnerRunTaskForwardsProgress: the phase lines the agent emits are what
// the chat shows while a task runs, so they must reach the caller's callback.
func TestAppRunnerRunTaskForwardsProgress(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return &resultAgent{tr: agent.TaskResult{Pass: true, Reason: "ok"}, phases: []string{"analysing…", "running: ls"}}
	}
	var seen []string
	if _, err := r.RunTask(context.Background(), "a task", func(format string, args ...any) {
		seen = append(seen, format)
	}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(seen) != 2 {
		t.Errorf("the progress lines must be forwarded, got %v", seen)
	}
}

// TestSummariseIsTheOnlyMapping: the sentence the chat shows is produced in one
// place, so a change to the wording cannot drift between callers.
func TestSummariseIsTheOnlyMapping(t *testing.T) {
	if got := summarise(agent.TaskResult{Pass: true, Summary: "s", Reason: "r"}); got != "s" {
		t.Errorf("the summary must win over the reason, got %q", got)
	}
	if got := summarise(agent.TaskResult{Pass: false, Reason: "why"}); got != "failed: why" {
		t.Errorf("got %q", got)
	}
}

// resultAgent is an AgentRunner that reports a fixed outcome, so the runner's
// mapping can be tested without a real agent.
type resultAgent struct {
	tr        agent.TaskResult
	err       error
	silent    bool
	phases    []string
	observer  func(agent.TaskResult)
	progress  func(string, ...any)
	runCalled bool
	// transcript is what the runner handed in, and what the run left behind. It is kept so a
	// test can assert the round trip: what the previous turn said must arrive, and what this
	// turn said must be carried out.
	transcript []agent.DialogueTurn
}

func (a *resultAgent) SetTranscript(turns []agent.DialogueTurn) {
	a.transcript = append([]agent.DialogueTurn(nil), turns...)
}

func (a *resultAgent) Transcript() []agent.DialogueTurn { return a.transcript }

func (a *resultAgent) Run(context.Context) error {
	a.runCalled = true
	if a.silent {
		return a.err
	}
	for _, p := range a.phases {
		if a.progress != nil {
			a.progress("%s", p)
		}
	}
	if a.observer != nil {
		a.observer(a.tr)
	}
	return a.err
}

func (a *resultAgent) RunCommand(context.Context, string) (string, int, error) { return "", 0, nil }

func (a *resultAgent) SetObserver(fn func(agent.TaskResult)) { a.observer = fn }

func (a *resultAgent) SetProgress(fn func(format string, args ...any)) { a.progress = fn }

// TestRealAgentSatisfiesTheObserverInterface: the runner installs its hooks
// through an interface, so the production agent must keep satisfying it. A change
// that removes either method would otherwise silently stop the chat from ever
// showing a result.
func TestRealAgentSatisfiesTheObserverInterface(t *testing.T) {
	var _ taskObserver = (*agent.Agent)(nil)
}

// The conversation belongs to the RUNNER, not to the planner.
//
// A planner is built per turn — that is how the interface has always done it — so a session
// created inside the planner would be a session per question: the amnesia this feature
// exists to remove. These tests pin the session to the runner's lifetime.

// TestTheConversationIsCreatedOnceAndReused: asking twice must return the same session, or
// every turn would start from nothing.
func TestTheConversationIsCreatedOnceAndReused(t *testing.T) {
	r := &AppRunner{Cfg: config.Default()}
	r.Cfg.LLM.Model = "gpt-4o"

	first := r.conversation(nil)
	if first == nil {
		t.Fatal("a conversation must be created on first use")
	}
	second := r.conversation(nil)
	if first != second {
		t.Error("the same conversation must be returned on the second call")
	}
}

// TestTheConversationHonoursTheConfiguration: the operator knows their server may be
// configured lower than the model's published window, so a value in the configuration wins
// over the built-in table.
func TestTheConversationHonoursTheConfiguration(t *testing.T) {
	r := &AppRunner{Cfg: config.Default()}
	r.Cfg.LLM.Model = "gpt-4o"
	r.Cfg.LLM.Session.ContextWindow = 2500
	r.Cfg.LLM.Session.Reserve = 300
	r.Cfg.LLM.Session.CompactAt = 0.55
	r.Cfg.LLM.Session.KeepRecent = 5

	s := r.conversation(nil)
	if s.Window != 2500 {
		t.Errorf("window = %d, want the configured 2500", s.Window)
	}
	if s.Reserve != 300 || s.CompactAt != 0.55 || s.KeepRecent != 5 {
		t.Errorf("policy = (%d, %.2f, %d), want the configured (300, 0.55, 5)",
			s.Reserve, s.CompactAt, s.KeepRecent)
	}
	// The system prompt is the planner's own: a conversation opened with a different
	// instruction than the requests use would let the two drift.
	if s.System != plan.SystemPrompt {
		t.Error("the conversation must open with the planner's system prompt")
	}
}

// TestAnUnconfiguredSessionKeepsTheDefaults: zeros in the configuration mean "unset", and
// they must not disable the reserve or the trigger — that is the failure that lets a context
// overflow in silence.
func TestAnUnconfiguredSessionKeepsTheDefaults(t *testing.T) {
	r := &AppRunner{Cfg: config.Default()}
	r.Cfg.LLM.Model = "gpt-4o"
	// Default() carries zeroes for the session block, which is the case being tested.

	s := r.conversation(nil)
	if s.Window <= 0 {
		t.Error("the window must come from the model when it is not configured")
	}
	if s.Reserve <= 0 {
		t.Error("a zero reserve must fall back to the default, not disable it")
	}
	if s.CompactAt <= 0 {
		t.Error("a zero trigger must fall back to the default, not disable compaction")
	}
	if s.KeepRecent <= 0 {
		t.Error("a zero tail must fall back to the default")
	}
}

// TestResetConversationStartsAFresh: leaving a subject behind is something the user asks
// for, not something the compaction is hoped to forget.
func TestResetConversationStartsAFresh(t *testing.T) {
	r := &AppRunner{Cfg: config.Default()}
	r.Cfg.LLM.Model = "gpt-4o"

	first := r.conversation(nil)
	first.Append(llm.Message{Role: "user", Content: "the previous subject"})

	r.ResetConversation()
	second := r.conversation(nil)
	if second == first {
		t.Fatal("a new session must be a different one")
	}
	if second.Len() != 0 {
		t.Errorf("the new session must be empty, got %d messages", second.Len())
	}
}

// TestTheReportBeforeAnyConversation: the report is reachable from the menu before anything
// has been asked, and it must answer rather than panic or return an empty line.
func TestTheReportBeforeAnyConversation(t *testing.T) {
	r := &AppRunner{Cfg: config.Default()}

	got := r.ConversationReport()
	if !strings.Contains(got, "not started") {
		t.Errorf("an empty session must say so, got %q", got)
	}
}

// TestTheReportDescribesTheSession: the window, the usage and the policy. A conversation the
// user cannot inspect is one they cannot trust.
func TestTheReportDescribesTheSession(t *testing.T) {
	r := &AppRunner{Cfg: config.Default()}
	r.Cfg.LLM.Model = "gpt-4o"
	s := r.conversation(nil)
	s.Append(llm.Message{Role: "user", Content: "a question"})

	got := r.ConversationReport()
	for _, want := range []string{"gpt-4o", "context", "in use", "compaction", "messages"} {
		if !strings.Contains(got, want) {
			t.Errorf("the report must mention %q:\n%s", want, got)
		}
	}
	// No summary yet, so no summary section.
	if strings.Contains(got, "carried summary") {
		t.Errorf("a session that has not compacted must not claim a summary:\n%s", got)
	}
}

// TestTheReportShowsWhatTheCompactionCarried: after a fold, the account is the most useful
// thing the report can show — it is what the agent believes about the earlier conversation.
func TestTheReportShowsWhatTheCompactionCarried(t *testing.T) {
	r := &AppRunner{Cfg: config.Default()}
	r.Cfg.LLM.Model = "gpt-4o"
	r.Cfg.LLM.Session.ContextWindow = 1000
	r.Cfg.LLM.Session.Reserve = 100
	r.Cfg.LLM.Session.CompactAt = 0.5
	r.Cfg.LLM.Session.KeepRecent = 2
	r.Cfg.LLM.Session.ContextWindow = 1000

	s := r.conversation(&summariserStub{out: "the user asked to count .txt files"})
	for i := 0; i < 60; i++ {
		s.Append(llm.Message{Role: "user", Content: strings.Repeat("word ", 30)})
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := r.ConversationReport()
	if !strings.Contains(got, "carried summary") {
		t.Errorf("a compacted session must show its summary:\n%s", got)
	}
	if !strings.Contains(got, "count .txt files") {
		t.Errorf("the report must carry the account itself:\n%s", got)
	}
	if !strings.Contains(got, "compaction(s)") {
		t.Errorf("the report must say the conversation was folded:\n%s", got)
	}
}

// summariserStub stands in for the engine in the compaction path.
type summariserStub struct{ out string }

func (s *summariserStub) Complete(context.Context, []llm.Message) (string, error) {
	return s.out, nil
}

// TestRunPlanHandsTheConversationToThePlanner is the wiring test that was missing.
//
// Everything else passed while the two halves were disconnected: the session existed on the
// runner, and the planner could hold one, but nothing joined them — so every turn started
// from nothing. Neither half can see that on its own, which is exactly why the wiring needs
// its own test rather than being assumed from the pieces.
func TestRunPlanHandsTheConversationToThePlanner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "the plan"}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"the plan"}}]}`)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.BackoffInitial = time.Millisecond
	cfg.LLM.BackoffMax = time.Millisecond
	cfg.Sandbox.Kind = "none"
	cfg.LLM.Model = "gpt-4o"

	r := NewAppRunner(io.Discard, io.Discard, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine

	if _, err := r.RunPlan(context.Background(), "the first question", func(string, ...any) {}); err != nil {
		t.Fatalf("the run failed: %v", err)
	}

	// The runner's conversation must have grown. If RunPlan built a planner without handing
	// it the session, this stays empty and the next turn is amnesiac.
	if got := r.conversation(nil).Len(); got == 0 {
		t.Error("RunPlan must give the planner the runner's conversation, or every turn starts from nothing")
	}
}

// TestTwoTurnsOfPlanShareTheConversation: the property the user actually experiences — a
// follow-up question can refer to what came before.
func TestTwoTurnsOfPlanShareTheConversation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "ok"}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.BackoffInitial = time.Millisecond
	cfg.LLM.BackoffMax = time.Millisecond
	cfg.Sandbox.Kind = "none"
	cfg.LLM.Model = "gpt-4o"

	r := NewAppRunner(io.Discard, io.Discard, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine

	if _, err := r.RunPlan(context.Background(), "my name is Madkoding", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunPlan(context.Background(), "what is my name?", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}

	// The first question must still be in the conversation the second turn is given.
	sess := r.conversation(nil)
	found := false
	for _, m := range sess.Messages() {
		if strings.Contains(m.Content, "my name is Madkoding") {
			found = true
		}
	}
	if !found {
		t.Errorf("the second turn must see the first, got %+v", sess.Messages())
	}
	if sess.Len() < 3 {
		t.Errorf("two turns must have left at least three messages, got %d", sess.Len())
	}
}

// TestANewSessionClearsTheConversationFromTheNextTurn: /new is a promise, and the promise is
// about the NEXT turn rather than about a field.
func TestANewSessionClearsTheConversationFromTheNextTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "ok"}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.BackoffInitial = time.Millisecond
	cfg.LLM.BackoffMax = time.Millisecond
	cfg.Sandbox.Kind = "none"
	cfg.LLM.Model = "gpt-4o"

	r := NewAppRunner(io.Discard, io.Discard, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine

	if _, err := r.RunPlan(context.Background(), "the old subject", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	r.ResetConversation()
	if _, err := r.RunPlan(context.Background(), "a fresh start", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}

	sess := r.conversation(nil)
	for _, m := range sess.Messages() {
		if strings.Contains(m.Content, "the old subject") {
			t.Errorf("the new session must not carry the old subject, got %+v", sess.Messages())
		}
	}
}

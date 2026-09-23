package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/session"
)

// The client is a tui.Runner, checked by the compiler rather than by a comment. This is the
// assertion that keeps the text interface able to use it: the day a method changes shape here or
// there, the build says so instead of the interface breaking at runtime.
var _ interface {
	Config() config.Config
	SetReasoning(string)
	ConversationSummary() session.Snapshot
	ConversationReport() string
	ResetConversation()
	RunModels(context.Context) (string, error)
	RecordVerdict(bool, string) string
	RewardReport() string
	TakePendingQuestions() ([]agent.AskItem, string)
	RunConfig(context.Context) error
	RunPlan(context.Context, string, func(string, ...any)) (string, error)
	RunTask(context.Context, string, func(string, ...any)) (string, error)
	SetApprover(agent.Approver)
} = (*Client)(nil)

// clientFor starts a real gateway around svc and returns a client for it, plus a counter of the
// requests the client made. The counter is how the caching rules are asserted: a cache that is
// claimed and not held shows up as requests, and nothing else on this interface shows it.
func clientFor(t *testing.T, svc Service) (*Client, *Server, *int) {
	t.Helper()
	srv := newTestServer(t, svc)
	hits := new(int)
	var mu sync.Mutex
	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*hits++
		mu.Unlock()
		srv.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(counting.Close)
	return NewClient(counting.URL, testToken), srv, hits
}

func requestsTo(t *testing.T, hits *int) int {
	t.Helper()
	return *hits
}

// --- the repaint path ---

// THE rule of this file: the repaint path never touches the network. tui/render.go calls Config()
// and ConversationSummary() on every frame - statusLines, stateGlyph, contextLabel - so a round
// trip there is a stutter the user feels while typing. Both are cached.
func TestTheRepaintPathIsCached(t *testing.T) {
	cfg := config.Default()
	svc := &fakeService{cfg: cfg, summary: session.Snapshot{Window: 100, Tokens: 40}}

	c, _, hits := clientFor(t, svc)
	for i := 0; i < 20; i++ {
		_ = c.Config()
		_ = c.ConversationSummary()
	}
	// One request for the configuration and one for the figures, for forty calls.
	if got := requestsTo(t, hits); got != 2 {
		t.Errorf("%d requests for 40 calls: the repaint path must be cached", got)
	}
}

// Config() cannot report a failure - tui.Runner says so - so an unreachable gateway answers with
// the default rather than with a lie. The calls that CAN report a failure are the ones that do.
func TestConfigFallsBackToTheDefaultWhenTheGatewayIsUnreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", testToken)
	got := c.Config()
	want := config.Default()
	if got.LLM.Model != want.LLM.Model {
		t.Errorf("model = %q, want the default %q", got.LLM.Model, want.LLM.Model)
	}
}

func TestConfigCarriesTheGatewaysSettings(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.Model = "gpt-4o-mini"
	cfg.LLM.APIKey = "sk-canary-0123456789abcdef"
	cfg.LLM.Reasoning.Level = "high"
	cfg.LLM.Reasoning.Enabled = true

	c, _, _ := clientFor(t, &fakeService{cfg: cfg})
	got := c.Config()
	if got.LLM.Provider != "openai" || got.LLM.Model != "gpt-4o-mini" {
		t.Errorf("provider/model = %q/%q", got.LLM.Provider, got.LLM.Model)
	}
	if got.LLM.Reasoning.Level != "high" || !got.LLM.Reasoning.Enabled {
		t.Errorf("reasoning = %q/%v", got.LLM.Reasoning.Level, got.LLM.Reasoning.Enabled)
	}
	// The key arrived as the sentinel, which is what turns the status dot on without the key ever
	// having crossed the wire.
	if got.LLM.APIKey != RedactedKey {
		t.Errorf("key = %q, want the sentinel %q", got.LLM.APIKey, RedactedKey)
	}
}

// A client against a gateway with no key must see NO key, or the status dot lies about being able
// to run anything.
func TestAServerWithoutAKeyTellsTheClientThereIsNoKey(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.APIKey = ""

	c, _, _ := clientFor(t, &fakeService{cfg: cfg})
	if got := c.Config(); got.LLM.APIKey != "" {
		t.Errorf("key = %q, want empty", got.LLM.APIKey)
	}
}

// reasoningService is a gateway whose level really changes when it is set. It exists because the
// assertion below is about the CLIENT's cache, and a fake that always answers the same thing would
// pass whether the client re-read the level or not.
type reasoningService struct {
	fakeService
	mu    sync.Mutex
	level string
}

func (r *reasoningService) Config() config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg := config.Default()
	cfg.LLM.Reasoning.Level = r.level
	return cfg
}

func (r *reasoningService) SetReasoning(level string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.level = level
}

// SetReasoning drops the cache instead of patching it: the gateway is the authority on what the
// level now is, and re-reading it is one request instead of a guess that can drift.
func TestSetReasoningRefreshesTheCachedConfig(t *testing.T) {
	c, _, _ := clientFor(t, &reasoningService{level: "low"})
	if got := c.Config().LLM.Reasoning.Level; got != "low" {
		t.Fatalf("level = %q before anything was set", got)
	}
	c.SetReasoning("high")
	if got := c.Config().LLM.Reasoning.Level; got != "high" {
		t.Errorf("level = %q after setting it to high: the cache was not dropped", got)
	}
}

// A zero Snapshot - no conversation has started - is re-read rather than trusted as a cache hit,
// because a cached zero and an uncached zero are indistinguishable in a struct.
func TestAZeroSummaryIsNotTreatedAsACacheHit(t *testing.T) {
	c, _, hits := clientFor(t, &fakeService{summary: session.Snapshot{}})
	_ = c.ConversationSummary()
	_ = c.ConversationSummary()
	// Two calls, two reads: the second is NOT served from a cache holding a zero.
	if got := requestsTo(t, hits); got != 2 {
		t.Errorf("%d requests for 2 calls with no figures, want 2", got)
	}
}

func TestSummaryIsEmptyWhenTheGatewayIsUnreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", testToken)
	if got := c.ConversationSummary(); got.Window != 0 {
		t.Errorf("window = %d, want 0", got.Window)
	}
}

// --- the one-shot reads ---

// A report that could not be read IS the report: tui.Runner says this cannot fail, so saying the
// gateway was unreachable is more useful than an empty panel.
func TestAnUnreadableReportSaysSoInsteadOfBeingEmpty(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", testToken)
	for name, got := range map[string]string{
		"report":  c.ConversationReport(),
		"reward":  c.RewardReport(),
		"verdict": c.RecordVerdict(true, "note"),
	} {
		if !strings.Contains(got, "could not be reached") {
			t.Errorf("%s = %q, want it to name the failure", name, got)
		}
	}
}

func TestTheOneShotReadsCarryTheTextThrough(t *testing.T) {
	svc := &fakeService{report: "the conversation so far", reward: "the reward line"}
	svc.verdict = func(bool, string) string { return "the verdict line" }
	svc.models = func(context.Context) (string, error) { return "the catalogue", nil }
	c, _, _ := clientFor(t, svc)

	if got := c.ConversationReport(); got != "the conversation so far" {
		t.Errorf("report = %q", got)
	}
	if got := c.RewardReport(); got != "the reward line" {
		t.Errorf("reward = %q", got)
	}
	if got := c.RecordVerdict(true, "a note"); got != "the verdict line" {
		t.Errorf("verdict = %q", got)
	}
	if got, err := c.RunModels(context.Background()); err != nil || got != "the catalogue" {
		t.Errorf("models = %q, %v", got, err)
	}
}

// Models can fail, and the failure is REPORTED rather than swallowed: the command's whole point is
// to tell the user what the gateway can reach.
func TestModelsReportsAFailure(t *testing.T) {
	svc := &fakeService{}
	svc.models = func(context.Context) (string, error) { return "", errors.New("the catalogue is unreachable") }
	c, _, _ := clientFor(t, svc)
	if _, err := c.RunModels(context.Background()); err == nil {
		t.Error("a failed catalogue read must be reported")
	}
}

func TestPendingQuestionsComeWithTheirOrigin(t *testing.T) {
	svc := &fakeService{questions: []agent.AskItem{{Text: "which branch?"}}, origin: "the last turn"}
	c, _, _ := clientFor(t, svc)

	items, origin := c.TakePendingQuestions()
	if len(items) != 1 || items[0].Text != "which branch?" {
		t.Errorf("items = %+v", items)
	}
	if origin != "the last turn" {
		t.Errorf("origin = %q", origin)
	}
	// And they were TAKEN: the gateway cleared them, which is what stops /answers from answering
	// the same question twice. Asserted by asking again rather than by reading the fake's fields,
	// which the handler goroutine also writes.
	again, _ := c.TakePendingQuestions()
	if len(again) != 0 {
		t.Errorf("the questions were not cleared: %+v", again)
	}
}

func TestPendingQuestionsAreEmptyWhenTheGatewayIsUnreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", testToken)
	items, origin := c.TakePendingQuestions()
	if items != nil || origin != "" {
		t.Errorf("items = %+v, origin = %q", items, origin)
	}
}

// ResetConversation drops the cached figures, so the next status redraw reads the gateway's empty
// conversation instead of the one from before the reset.
func TestResetConversationClearsTheCachedFigures(t *testing.T) {
	resettable := &resettableService{summary: session.Snapshot{Window: 100, Tokens: 40}}
	c, _, _ := clientFor(t, resettable)

	if got := c.ConversationSummary(); got.Window != 100 {
		t.Fatalf("window = %d", got.Window)
	}
	c.ResetConversation()
	if got := c.ConversationSummary(); got.Window != 0 {
		t.Errorf("window = %d after a reset, want 0: the cached figures survived", got.Window)
	}
}

// resettableService forgets its conversation when it is reset, like the real runner does.
type resettableService struct {
	fakeService
	mu      sync.Mutex
	summary session.Snapshot
}

func (r *resettableService) ConversationSummary() session.Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summary
}

func (r *resettableService) ResetConversation() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary = session.Snapshot{}
}

// --- the wizard ---

// Embedded, the wizard is handed over directly: it reads lines from the terminal this process was
// started from, which is the same machine as the gateway.
func TestTheEmbeddedClientRunsTheWizardItWasGiven(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", testToken)
	ran := false
	c.Wizard = func(context.Context) error {
		ran = true
		return nil
	}
	if err := c.RunConfig(context.Background()); err != nil {
		t.Fatalf("RunConfig: %v", err)
	}
	if !ran {
		t.Error("the wizard was not run")
	}
}

func TestTheEmbeddedClientReportsAWizardFailure(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", testToken)
	c.Wizard = func(context.Context) error { return errors.New("no terminal") }
	if err := c.RunConfig(context.Background()); err == nil {
		t.Error("a failed wizard must be reported")
	}
}

// Remote, there is nothing to hand over, and saying so is more useful than a wizard that cannot
// read its answers.
func TestARemoteClientSaysTheWizardNeedsATerminal(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", testToken)
	err := c.RunConfig(context.Background())
	if err == nil {
		t.Fatal("a client with no wizard must refuse")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("error = %q, want it to name the reason", err)
	}
}

// --- the streaming runs ---

func TestRunTaskStreamsProgressAndReturnsTheResult(t *testing.T) {
	svc := &fakeService{}
	svc.task = func(_ context.Context, task string, progress func(string, ...any)) (string, error) {
		progress("reading %s", "a file")
		progress("done")
		return "the answer to " + task, nil
	}
	c, _, _ := clientFor(t, svc)

	var lines []string
	got, err := c.RunTask(context.Background(), "the task", func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got != "the answer to the task" {
		t.Errorf("result = %q", got)
	}
	if len(lines) != 2 || lines[0] != "reading a file" || lines[1] != "done" {
		t.Errorf("progress = %q, want the agent's lines verbatim", lines)
	}
}

func TestRunPlanStreamsAndReturnsLikeRunTask(t *testing.T) {
	svc := &fakeService{}
	svc.plan = func(_ context.Context, prompt string, progress func(string, ...any)) (string, error) {
		progress("planning")
		return "the plan for " + prompt, nil
	}
	c, _, _ := clientFor(t, svc)

	var lines []string
	got, err := c.RunPlan(context.Background(), "ship it", func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	if err != nil {
		t.Fatalf("RunPlan: %v", err)
	}
	if got != "the plan for ship it" {
		t.Errorf("result = %q", got)
	}
	if len(lines) != 1 || lines[0] != "planning" {
		t.Errorf("progress = %q", lines)
	}
}

// A failure mid-stream arrives as an error EVENT, because the 200 and the headers are already on
// the wire. The client turns it back into the error the interface shows.
func TestAFailureMidStreamBecomesAnError(t *testing.T) {
	svc := &fakeService{}
	svc.task = func(context.Context, string, func(string, ...any)) (string, error) {
		return "", errors.New("the model refused")
	}
	c, _, _ := clientFor(t, svc)

	_, err := c.RunTask(context.Background(), "x", func(string, ...any) {})
	if err == nil {
		t.Fatal("a failed run must be reported")
	}
	if !strings.Contains(err.Error(), "the model refused") {
		t.Errorf("error = %q, want the agent's reason", err)
	}
}

// A refusal BEFORE the stream - the second client of a busy gateway - is a status code with a
// reason, and that reason is what the user can act on. The slot is held by a REAL run here rather
// than by an injected error, so the refusal the client sees is the one a busy gateway sends.
func TestARefusalBeforeTheStreamKeepsItsReason(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	svc := &fakeService{}
	svc.task = func(context.Context, string, func(string, ...any)) (string, error) {
		once.Do(func() { close(started) })
		<-release
		return "the first run", nil
	}
	c, _, _ := clientFor(t, svc)

	first := make(chan error, 1)
	go func() {
		_, err := c.RunTask(context.Background(), "one", func(string, ...any) {})
		first <- err
	}()
	<-started

	_, err := c.RunTask(context.Background(), "two", func(string, ...any) {})
	close(release)
	<-first
	if err == nil {
		t.Fatal("a refused run must be reported")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("error = %q, want the gateway's reason", err)
	}
}

// The figures that came WITH the answer are cached by the client, so the status bar is right the
// instant the turn ends without a request of its own.
func TestTheDoneEventRefreshesTheSummaryTheClientReports(t *testing.T) {
	svc := &fakeService{summary: session.Snapshot{Window: 100, Tokens: 42}}
	svc.task = func(context.Context, string, func(string, ...any)) (string, error) { return "done", nil }
	c, _, hits := clientFor(t, svc)

	if _, err := c.RunTask(context.Background(), "x", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	before := requestsTo(t, hits)
	got := c.ConversationSummary()
	if got.Tokens != 42 {
		t.Errorf("tokens = %d, want the 42 the done event carried", got.Tokens)
	}
	// And no request was made for it: the figures came with the answer.
	if after := requestsTo(t, hits); after != before {
		t.Errorf("reading the figures made %d requests", after-before)
	}
}

// --- the approval round trip, from the client's side ---

func TestTheClientAnswersAnApprovalOverTheWire(t *testing.T) {
	approved := make(chan bool, 1)
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		ok, err := svc.approver(ctx, agent.ApprovalRequest{Command: "rm -rf ./build", Rule: "destructive"})
		if err != nil {
			return "", err
		}
		approved <- ok
		return "carried on", nil
	}
	c, _, _ := clientFor(t, svc)

	var saw agent.ApprovalRequest
	c.SetApprover(func(_ context.Context, req agent.ApprovalRequest) (bool, error) {
		saw = req
		return true, nil
	})

	if _, err := c.RunTask(context.Background(), "clean", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	select {
	case ok := <-approved:
		if !ok {
			t.Error("the approval did not arrive as a yes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the gateway never received the answer")
	}
	// The EXACT command line reached the human, never a summary of it.
	if saw.Command != "rm -rf ./build" || saw.Rule != "destructive" {
		t.Errorf("the approver saw %+v", saw)
	}
}

// A client with NO approver answers NO. It is the same rule the agent follows: a consequential
// command with nobody to ask is refused, and silence is not consent.
func TestAClientWithNoApproverRefuses(t *testing.T) {
	approved := make(chan bool, 1)
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		ok, err := svc.approver(ctx, agent.ApprovalRequest{Command: "rm -rf ./build"})
		if err != nil {
			return "", err
		}
		approved <- ok
		return "did not run it", nil
	}
	c, _, _ := clientFor(t, svc)

	if _, err := c.RunTask(context.Background(), "clean", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	select {
	case ok := <-approved:
		if ok {
			t.Error("a client with nobody to ask approved a command")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the gateway never received the answer")
	}
}

// An approver that FAILS is a refusal too: an error is not consent.
func TestAnApproverThatFailsRefuses(t *testing.T) {
	approved := make(chan bool, 1)
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		ok, err := svc.approver(ctx, agent.ApprovalRequest{Command: "rm -rf ./build"})
		if err != nil {
			return "", err
		}
		approved <- ok
		return "did not run it", nil
	}
	c, _, _ := clientFor(t, svc)
	c.SetApprover(func(context.Context, agent.ApprovalRequest) (bool, error) {
		return true, errors.New("the prompt could not be drawn")
	})

	if _, err := c.RunTask(context.Background(), "clean", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	select {
	case ok := <-approved:
		if ok {
			t.Error("an approver that failed approved the command")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the gateway never received the answer")
	}
}

// --- the wire format ---

// The reader and the writer are asserted against EACH OTHER, not against a fixture: a fixture
// would keep passing after the writer changed, which is the failure mode that matters here.
func TestTheEventReaderUnderstandsTheEventWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	rc := http.NewResponseController(rec)
	if err := startStream(rec, rc); err != nil {
		t.Fatalf("startStream: %v", err)
	}
	// A payload with a RAW newline in it: JSON escapes it, so the framing's one-line-per-event
	// invariant survives content that would otherwise break it.
	if err := writeEvent(rec, rc, EventProgress, progressEvent{Text: "a line\nwith a newline in it"}); err != nil {
		t.Fatalf("writeEvent: %v", err)
	}
	if err := writeEvent(rec, rc, EventDone, doneEvent{Result: "the result"}); err != nil {
		t.Fatalf("writeEvent: %v", err)
	}

	var events []string
	var result string
	err := streamEvents(context.Background(), strings.NewReader(rec.Body.String()), func(event string, data []byte) error {
		events = append(events, event)
		if event == EventDone {
			var d doneEvent
			if err := json.Unmarshal(data, &d); err != nil {
				return err
			}
			result = d.Result
		}
		return nil
	})
	if err != nil {
		t.Fatalf("streamEvents: %v", err)
	}
	if len(events) != 2 || events[0] != EventProgress || events[1] != EventDone {
		t.Errorf("events = %q", events)
	}
	if result != "the result" {
		t.Errorf("result = %q", result)
	}
}

// A line that is neither an event name nor data is ignored: the ": connected" comment the server
// sends to get the headers on the wire, and anything a newer gateway adds.
func TestTheEventReaderIgnoresWhatItDoesNotKnow(t *testing.T) {
	body := ": connected\n\nevent: something-new\ndata: {\"x\":1}\n\n"
	var seen []string
	if err := streamEvents(context.Background(), strings.NewReader(body), func(event string, _ []byte) error {
		seen = append(seen, event)
		return nil
	}); err != nil {
		t.Fatalf("streamEvents: %v", err)
	}
	// The unknown event reached the callback, which is where the client ignores it: a reader that
	// refused to carry it would break an older client on a newer gateway.
	if len(seen) != 1 || seen[0] != "something-new" {
		t.Errorf("seen = %q", seen)
	}
}

// A cancelled context stops the read: the run is over and the goroutine must not sit on a socket
// waiting for lines nobody will send.
func TestACancelledContextStopsTheRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	body := "event: progress\ndata: {\"text\":\"x\"}\n\n"
	err := streamEvents(ctx, strings.NewReader(body), func(string, []byte) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// An error from the callback stops the stream: the client decided the run is over.
func TestACallbackErrorStopsTheRead(t *testing.T) {
	body := "event: error\ndata: {\"error\":\"the model refused\"}\n\nevent: progress\ndata: {\"text\":\"more\"}\n\n"
	calls := 0
	err := streamEvents(context.Background(), strings.NewReader(body), func(string, []byte) error {
		calls++
		return errors.New("stop")
	})
	if err == nil || !strings.Contains(err.Error(), "stop") {
		t.Errorf("err = %v", err)
	}
	if calls != 1 {
		t.Errorf("the reader kept going after the error: %d calls", calls)
	}
}

// A line past the default scanner buffer is carried whole: a progress line can hold a long tool
// result, and a transport that silently truncated it would corrupt the display.
func TestALongLineIsNotTruncated(t *testing.T) {
	long := strings.Repeat("x", 100_000)
	rec := httptest.NewRecorder()
	rc := http.NewResponseController(rec)
	if err := startStream(rec, rc); err != nil {
		t.Fatalf("startStream: %v", err)
	}
	if err := writeEvent(rec, rc, EventProgress, progressEvent{Text: long}); err != nil {
		t.Fatalf("writeEvent: %v", err)
	}

	var got string
	if err := streamEvents(context.Background(), strings.NewReader(rec.Body.String()), func(_ string, data []byte) error {
		var p progressEvent
		if err := json.Unmarshal(data, &p); err != nil {
			return err
		}
		got = p.Text
		return nil
	}); err != nil {
		t.Fatalf("streamEvents: %v", err)
	}
	if len(got) != len(long) {
		t.Errorf("carried %d bytes of %d", len(got), len(long))
	}
}

// A malformed data line is reported rather than skipped: the client cannot render what it cannot
// parse, and going quiet would look like a run with no output.
func TestAMalformedEventPayloadIsReported(t *testing.T) {
	body := "event: progress\ndata: not json\n\n"
	err := streamEvents(context.Background(), strings.NewReader(body), func(_ string, data []byte) error {
		var p progressEvent
		return json.Unmarshal(data, &p)
	})
	if err == nil {
		t.Error("a malformed payload must be reported")
	}
}

// rawGateway is a gateway that answers a run with exactly the bytes given, so the client's
// behaviour on a stream that does not match the contract has a test. No Service is involved: the
// point is the CLIENT, and what it does with a line it cannot parse.
//
// POSTs to the approval endpoint fail, which is the failing send-back path.
func rawGateway(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sessions/"+DefaultSession+"/runs/approval" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, testToken)
}

// Every payload the client parses gets a test with a body it cannot parse, because a stream that
// is not understood must end the run with a reason instead of going quiet. Going quiet would look
// like a run with no output, which is the one thing a user cannot tell from a hang.
func TestAnUnparseableEventEndsTheRun(t *testing.T) {
	cases := map[string]string{
		"progress": "event: progress\ndata: not json\n\n",
		"done":     "event: done\ndata: not json\n\n",
		"error":    "event: error\ndata: not json\n\n",
		"approval": "event: approval\ndata: not json\n\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c := rawGateway(t, body)
			if _, err := c.RunTask(context.Background(), "x", func(string, ...any) {}); err == nil {
				t.Errorf("a malformed %s event must end the run with a reason", name)
			}
		})
	}
}

// A question whose answer cannot be sent back fails the RUN rather than being ignored: an
// unanswered approval is a yes-or-no the agent is still waiting on, and pretending it was
// delivered would leave the run waiting forever.
func TestAnApprovalThatCannotBeSentBackFailsTheRun(t *testing.T) {
	c := rawGateway(t, "event: approval\ndata: {\"id\":\"abc\",\"command\":\"rm -rf ./build\"}\n\n")
	c.SetApprover(func(context.Context, agent.ApprovalRequest) (bool, error) { return true, nil })

	_, err := c.RunTask(context.Background(), "x", func(string, ...any) {})
	if err == nil {
		t.Fatal("a failed send-back must fail the run")
	}
	if !strings.Contains(err.Error(), "could not be sent back") {
		t.Errorf("error = %q, want it to name the failure", err)
	}
}

// An address that cannot even be turned into a request is reported, on both paths: the streaming
// one and the one-shot one. This is the client's own failure, not the gateway's.
func TestAnAddressThatCannotBeUsedIsReported(t *testing.T) {
	c := NewClient("://a malformed address", testToken)

	if _, err := c.RunTask(context.Background(), "x", func(string, ...any) {}); err == nil {
		t.Error("a run against an unusable address must be reported")
	}
	if got := c.ConversationReport(); !strings.Contains(got, "could not be reached") {
		t.Errorf("report = %q, want it to name the failure", got)
	}
}

// A body that cannot be encoded is reported before anything is sent. The gateway's payloads are
// always encodable, so this is driven directly - the same rule the rest of the repository follows:
// an error path nothing can provoke is an error path that rots.
func TestAnUnencodableBodyIsReported(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", testToken)
	bad := map[string]any{"x": make(chan int)}

	if _, err := c.run(context.Background(), c.scoped("/task"), bad, nil); err == nil {
		t.Error("a run with an unencodable body must be reported")
	}
	if err := c.do(context.Background(), http.MethodPost, c.scoped("/verdict"), bad, nil); err == nil {
		t.Error("a one-shot request with an unencodable body must be reported")
	}
}

// A run whose connection fails is reported as the failure it is. A closed port is used rather
// than an injected error so the failure is a real one from the transport.
func TestARunAgainstAClosedPortIsReported(t *testing.T) {
	// A listener that is closed immediately: the address is well-formed and nothing is there.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close()

	c := NewClient(addr, testToken)
	if _, err := c.RunTask(context.Background(), "x", func(string, ...any) {}); err == nil {
		t.Error("a run against a closed port must be reported")
	}
}

// A refusal with no JSON body still produces a message a user can read.
func TestARefusalWithoutAReasonStillSaysSomething(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(strings.NewReader("<html>a proxy said no</html>")),
	}
	err := refusalError(resp)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v, want it to name the status", err)
	}
}

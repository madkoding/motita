package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/task"
)

// The two hook setters and the synthesis phase. The setters exist so an embedder
// can install the hooks through an interface without touching the struct, and the
// synthesis phase is what turns a validated run into the sentence the chat shows.

func TestSetObserverAndSetProgress(t *testing.T) {
	var lines []string
	var got TaskResult

	a := New(config.Default(), nil, nil, nil, nil)
	a.SetProgress(func(format string, args ...any) { lines = append(lines, format) })
	a.SetObserver(func(tr TaskResult) { got = tr })

	if a.Progress == nil || a.Observer == nil {
		t.Fatal("the setters must install the hooks")
	}
	a.report("running: %s", "ls")
	a.Observer(TaskResult{Pass: true, Reason: "ok"})

	if len(lines) != 1 || lines[0] != "running: %s" {
		t.Errorf("the progress hook received %v", lines)
	}
	if !got.Pass || got.Reason != "ok" {
		t.Errorf("the observer received %+v", got)
	}
}

// TestReportWithoutAProgressHook: the agent runs without any interface attached
// (the cron and one-shot paths), and report must simply do nothing then.
func TestReportWithoutAProgressHook(t *testing.T) {
	a := New(config.Default(), nil, nil, nil, nil)
	if a.Progress != nil {
		t.Fatal("a fresh agent must have no progress hook")
	}
	a.report("nothing should happen") // must not panic
}

// synthesisAgent builds an agent whose engine answers every request with the given
// body, which is enough to drive the synthesis phase on its own.
func synthesisAgent(t *testing.T, answer string) *Agent {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, answer)
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.BackoffInitial = time.Millisecond
	cfg.LLM.Timeout = 5 * time.Second
	engine, err := llm.New(cfg.LLM, logx.Global())
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	return New(cfg, logx.Global(), engine, &sandbox.Sandbox{}, nil)
}

// synthesisTask is the task the synthesis prompt is built from.
func synthesisTask(t *testing.T) task.Task {
	t.Helper()
	src, err := task.NewText("count the files", "test")
	if err != nil {
		t.Fatalf("task.NewText: %v", err)
	}
	first, err := src.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return first
}

// TestSynthesizePhaseReturnsTheSummary: the happy path — the model answers with
// the documented JSON object and the summary is what the chat will show.
func TestSynthesizePhaseReturnsTheSummary(t *testing.T) {
	a := synthesisAgent(t, `{"summary":"there are 20 .txt files in /home"}`)
	got := a.synthesizePhase(context.Background(), synthesisTask(t), "20", anchor.Result{Reason: "1 check passed"})
	if got != "there are 20 .txt files in /home" {
		t.Errorf("summary = %q", got)
	}
}

// TestSynthesizePhaseTrimsTheSummary: a summary padded with whitespace or newlines
// would render as blank lines inside the panel, so it is trimmed.
func TestSynthesizePhaseTrimsTheSummary(t *testing.T) {
	a := synthesisAgent(t, `{"summary":"  hay 20  \n"}`)
	got := a.synthesizePhase(context.Background(), synthesisTask(t), "20", anchor.Result{Reason: "ok"})
	if got != "hay 20" {
		t.Errorf("summary = %q, want it trimmed", got)
	}
}

// TestSynthesizePhaseWithABlankSummary: a well-formed reply that carries no text
// means "no summary", not the literal spaces between the quotes. The caller falls
// back to the validation reason then.
func TestSynthesizePhaseWithABlankSummary(t *testing.T) {
	a := synthesisAgent(t, `{"summary":"   "}`)
	if got := a.synthesizePhase(context.Background(), synthesisTask(t), "20", anchor.Result{Reason: "ok"}); got != "" {
		t.Errorf("a blank summary must come back empty, got %q", got)
	}
}

// TestSynthesizePhaseOnAnUnexpectedReply: a reply that is not the expected JSON
// object yields no summary, so the caller uses its fallback instead of showing
// garbled text.
func TestSynthesizePhaseOnAnUnexpectedReply(t *testing.T) {
	a := synthesisAgent(t, `not JSON at all`)
	if got := a.synthesizePhase(context.Background(), synthesisTask(t), "20", anchor.Result{Reason: "ok"}); got != "" {
		t.Errorf("an unparsable reply must yield no summary, got %q", got)
	}
	// A JSON value that decodes but is not an object is equally unusable.
	b := synthesisAgent(t, `[1, 2, 3]`)
	if got := b.synthesizePhase(context.Background(), synthesisTask(t), "20", anchor.Result{Reason: "ok"}); got != "" {
		t.Errorf("a non-object reply must yield no summary, got %q", got)
	}
}

// TestSynthesizePhaseWithAFailingEngine: when the engine is unreachable the phase
// gives up quietly and the caller's fallback is used. It must not turn a validated
// run into a failure.
func TestSynthesizePhaseWithAFailingEngine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.BackoffInitial = time.Millisecond
	cfg.LLM.Timeout = 2 * time.Second
	engine, err := llm.New(cfg.LLM, logx.Global())
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	a := New(cfg, logx.Global(), engine, &sandbox.Sandbox{}, nil)

	if got := a.synthesizePhase(context.Background(), synthesisTask(t), "20", anchor.Result{Reason: "ok"}); got != "" {
		t.Errorf("a failing engine must yield no summary, got %q", got)
	}
}

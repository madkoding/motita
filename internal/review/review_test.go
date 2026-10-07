package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/usage"
)

// fakeRunner is a SkillRunner the tests drive: it answers with a fixed result
// or error, and can block until the test releases it or the context is
// cancelled.
type fakeRunner struct {
	mu       sync.Mutex
	result   string
	err      error
	calls    int
	lastIn   string
	block    chan struct{}
	canceled bool
}

func (f *fakeRunner) Run(ctx context.Context, input string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.lastIn = input
	block := f.block
	f.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			f.mu.Lock()
			f.canceled = true
			f.mu.Unlock()
			return "", ctx.Err()
		}
	}
	return f.result, f.err
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeRunner) input() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastIn
}

func (f *fakeRunner) wasCanceled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canceled
}

// builder returns a RunnerBuilder that always hands back fr and records the
// soul and loop budget it was called with.
func builder(fr SkillRunner, soul *string, loops *int) RunnerBuilder {
	return func(_ *llm.Client, _ *procedures.Store, s string, maxLoops int) SkillRunner {
		if soul != nil {
			*soul = s
		}
		if loops != nil {
			*loops = maxLoops
		}
		return fr
	}
}

func newTestLogger(t *testing.T) *logx.Logger {
	t.Helper()
	l, err := logx.New(logx.Options{Path: filepath.Join(t.TempDir(), "review.log"), Level: logx.Debug})
	if err != nil {
		t.Fatalf("logx.New: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never became true")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNewStoresTheConfiguration(t *testing.T) {
	cfg := config.Review{Enabled: true, Interval: 3, Timeout: time.Second, MaxIterations: 5}
	procs := &procedures.Store{}
	r := New(cfg, nil, procs, nil, nil)
	if r == nil {
		t.Fatal("New returned nil")
	}
	if r.cfg.Interval != 3 || r.cfg.MaxIterations != 5 || !r.cfg.Enabled {
		t.Fatalf("New did not keep the configuration: %+v", r.cfg)
	}
	if r.procs != procs {
		t.Fatal("New did not keep the store")
	}
	if r.IsRunning() {
		t.Fatal("a freshly built review must not be running")
	}
}

func TestMaybeRunDisabledDoesNothing(t *testing.T) {
	built := 0
	r := New(config.Review{Enabled: false, Interval: 1, Timeout: time.Second}, nil, &procedures.Store{}, nil,
		func(*llm.Client, *procedures.Store, string, int) SkillRunner {
			built++
			return &fakeRunner{}
		})
	r.MaybeRun([]llm.Message{{Role: "user", Content: "hi"}}, 100)
	time.Sleep(50 * time.Millisecond)
	if built != 0 {
		t.Fatalf("a disabled review built a runner %d times", built)
	}
	if r.IsRunning() {
		t.Fatal("a disabled review must not run")
	}
}

func TestMaybeRunBelowIntervalDoesNothing(t *testing.T) {
	built := 0
	r := New(config.Review{Enabled: true, Interval: 5, Timeout: time.Second}, nil, &procedures.Store{}, nil,
		func(*llm.Client, *procedures.Store, string, int) SkillRunner {
			built++
			return &fakeRunner{}
		})
	r.MaybeRun([]llm.Message{{Role: "user", Content: "hi"}}, 4)
	time.Sleep(50 * time.Millisecond)
	if built != 0 {
		t.Fatalf("a review below the interval built a runner %d times", built)
	}
}

func TestMaybeRunRunsTheFork(t *testing.T) {
	fr := &fakeRunner{result: "patched the skill"}
	var soul string
	var loops int
	r := New(config.Review{Enabled: true, Interval: 1, Timeout: 2 * time.Second, MaxIterations: 7},
		nil, &procedures.Store{}, nil, builder(fr, &soul, &loops))

	r.MaybeRun([]llm.Message{{Role: "user", Content: "please fix the tone"}}, 1)
	r.Wait(2 * time.Second)

	if fr.callCount() != 1 {
		t.Fatalf("runner called %d times, want 1", fr.callCount())
	}
	if loops != 7 {
		t.Fatalf("runner built with maxLoops=%d, want 7", loops)
	}
	if !strings.Contains(soul, "skill review pass") {
		t.Fatalf("runner built with the wrong soul: %q", soul)
	}
	if !strings.Contains(fr.input(), "please fix the tone") {
		t.Fatalf("runner did not receive the transcript: %q", fr.input())
	}
	if r.IsRunning() {
		t.Fatal("the review is still marked running after Wait")
	}
}

func TestRunLogsAFailure(t *testing.T) {
	fr := &fakeRunner{err: errors.New("engine exploded")}
	r := New(config.Review{Enabled: true, Interval: 1, Timeout: time.Second},
		nil, &procedures.Store{}, newTestLogger(t), builder(fr, nil, nil))

	r.MaybeRun([]llm.Message{{Role: "user", Content: "x"}}, 1)
	r.Wait(2 * time.Second)

	if fr.callCount() != 1 {
		t.Fatalf("runner called %d times, want 1", fr.callCount())
	}
	if r.IsRunning() {
		t.Fatal("a failed review must clear the running flag")
	}
}

func TestRunSkipsTheLogWhenNothingToSave(t *testing.T) {
	fr := &fakeRunner{result: "Nothing to save."}
	r := New(config.Review{Enabled: true, Interval: 1, Timeout: time.Second},
		nil, &procedures.Store{}, newTestLogger(t), builder(fr, nil, nil))

	r.MaybeRun([]llm.Message{{Role: "user", Content: "x"}}, 1)
	r.Wait(2 * time.Second)

	if fr.callCount() != 1 {
		t.Fatalf("runner called %d times, want 1", fr.callCount())
	}
}

func TestRunTruncatesALongResult(t *testing.T) {
	fr := &fakeRunner{result: strings.Repeat("x", 500)}
	r := New(config.Review{Enabled: true, Interval: 1, Timeout: time.Second},
		nil, &procedures.Store{}, newTestLogger(t), builder(fr, nil, nil))

	r.MaybeRun([]llm.Message{{Role: "user", Content: "x"}}, 1)
	r.Wait(2 * time.Second)

	if fr.callCount() != 1 {
		t.Fatalf("runner called %d times, want 1", fr.callCount())
	}
}

func TestRunSavesTheUsageLedger(t *testing.T) {
	dir := t.TempDir()
	led, err := usage.Open(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatalf("usage.Open: %v", err)
	}
	// A clean ledger makes Save a no-op: mark it dirty so the write happens.
	led.BumpUse("x")
	procs := &procedures.Store{Usage: led}
	fr := &fakeRunner{result: "Nothing to save."}
	r := New(config.Review{Enabled: true, Interval: 1, Timeout: time.Second},
		nil, procs, nil, builder(fr, nil, nil))

	r.MaybeRun([]llm.Message{{Role: "user", Content: "x"}}, 1)
	r.Wait(2 * time.Second)

	if fr.callCount() != 1 {
		t.Fatalf("runner called %d times, want 1", fr.callCount())
	}
	if _, err := os.Stat(filepath.Join(dir, "usage.json")); err != nil {
		t.Fatalf("the usage ledger was not saved: %v", err)
	}
}

func TestRunLogsAUsageSaveFailure(t *testing.T) {
	dir := t.TempDir()
	led, err := usage.Open(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatalf("usage.Open: %v", err)
	}
	// Point the ledger at a directory: Save cannot write it, so the fork must
	// log the failure instead of panicking. The ledger must be dirty, or Save
	// returns before touching the disk.
	led.BumpUse("x")
	led.Path = dir
	procs := &procedures.Store{Usage: led}
	fr := &fakeRunner{result: "Nothing to save."}
	r := New(config.Review{Enabled: true, Interval: 1, Timeout: time.Second},
		nil, procs, newTestLogger(t), builder(fr, nil, nil))

	r.MaybeRun([]llm.Message{{Role: "user", Content: "x"}}, 1)
	r.Wait(2 * time.Second)

	if fr.callCount() != 1 {
		t.Fatalf("runner called %d times, want 1", fr.callCount())
	}
}

func TestMaybeRunCancelsTheInFlightReview(t *testing.T) {
	first := make(chan struct{})
	second := make(chan struct{})
	var mu sync.Mutex
	var built []*fakeRunner
	r := New(config.Review{Enabled: true, Interval: 1, Timeout: 5 * time.Second},
		nil, &procedures.Store{}, nil,
		func(*llm.Client, *procedures.Store, string, int) SkillRunner {
			mu.Lock()
			defer mu.Unlock()
			fr := &fakeRunner{}
			if len(built) == 0 {
				fr.block = first
			} else {
				fr.block = second
			}
			built = append(built, fr)
			return fr
		})

	r.MaybeRun([]llm.Message{{Role: "user", Content: "one"}}, 1)
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(built) == 1 })

	r.MaybeRun([]llm.Message{{Role: "user", Content: "two"}}, 1)
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(built) == 2 })

	mu.Lock()
	firstRunner := built[0]
	mu.Unlock()
	waitFor(t, func() bool { return firstRunner.wasCanceled() })

	close(second)
	r.Wait(3 * time.Second)
}

func TestWaitReturnsImmediatelyWhenIdle(t *testing.T) {
	r := New(config.Review{}, nil, &procedures.Store{}, nil, nil)
	start := time.Now()
	r.Wait(50 * time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Wait blocked on an idle review for %s", elapsed)
	}
}

func TestBuildReviewInput(t *testing.T) {
	long := strings.Repeat("a", 2500)
	msgs := []llm.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi", ToolCalls: []llm.ToolCall{
			{ID: "c1", Function: llm.FunctionCall{Name: "save_skill", Arguments: json.RawMessage(`{"name":"x"}`)}},
		}},
		{Role: "assistant", ToolCallID: "c1", Content: "tool output"},
		{Role: "user", Content: long},
	}
	got := buildReviewInput(msgs)

	for _, want := range []string{
		"## CONVERSATION TRANSCRIPT",
		"### user\nhello",
		"### assistant\nhi",
		"[tool call: save_skill(",
		"### tool\ntool output",
		"…[truncated]",
		"## YOUR TASK",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("buildReviewInput is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, long) {
		t.Fatal("buildReviewInput did not truncate the long message")
	}
}

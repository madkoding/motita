package tui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// Auto mode runs a question as a plan and an order as a task, and says which before it runs.
func TestAutoModeRoutesEachMessageAndSaysWhich(t *testing.T) {
	runner := &fakeRunner{planAnswer: "the plan"}
	tui := newFakeTUI("/auto\nhow does the cache work?\n\nq\n", runner)
	tui.Run(context.Background())
	if !runner.planCalled || runner.taskCalled {
		t.Fatalf("a question in auto mode: plan=%v task=%v", runner.planCalled, runner.taskCalled)
	}
	if out := outputOf(tui); !strings.Contains(out, "mode: plan (question)") {
		t.Errorf("the mode was not shown: %q", out)
	}

	runner = &fakeRunner{}
	tui = newFakeTUI("/auto\nfix the flaky test\n\nq\n", runner)
	tui.Run(context.Background())
	if runner.planCalled || !runner.taskCalled {
		t.Fatalf("an order in auto mode: plan=%v task=%v", runner.planCalled, runner.taskCalled)
	}
	if tui.screen != ScreenAuto {
		t.Errorf("auto mode must stay on after a message, got %v", tui.screen)
	}
}

func TestAutoModeIsNamedInTheBoxAndTheHints(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.screen = ScreenAuto
	if name, about := tui.modeTitle(); name != "Auto" || about == "" {
		t.Errorf("modeTitle = %q, %q", name, about)
	}
	if ScreenAuto.String() != "Auto" {
		t.Errorf("String() = %q", ScreenAuto.String())
	}
	nextModes := map[Screen]string{ScreenTask: "Plan mode", ScreenPlan: "Auto mode", ScreenAuto: "Task mode"}
	for screen, next := range nextModes {
		tui.screen = screen
		found := false
		for _, h := range tui.hintList() {
			found = found || (h[0] == "Tab" && h[1] == next)
		}
		if !found {
			t.Errorf("on %v the Tab hint must name %q: %v", screen, next, tui.hintList())
		}
	}
}

// CompleteIntent is one plain completion through the runner's engine.
func TestCompleteIntentAsksTheEngine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"plan\"}"}}]}`)
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.Timeout = 5 * time.Second
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	got, err := r.CompleteIntent(context.Background(), "system", "the cache")
	if err != nil || got != `{"kind":"plan"}` {
		t.Errorf("CompleteIntent = %q, %v", got, err)
	}

	cfg.LLM.APIKey = ""
	r = NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	if _, err := r.CompleteIntent(context.Background(), "system", "x"); err == nil {
		t.Error("CompleteIntent must fail without an engine")
	}
}

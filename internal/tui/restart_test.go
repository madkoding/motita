package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
)

// streamingLLM answers every streamed request with the next answer, and keeps each request body.
func streamingLLM(t *testing.T, answers ...string) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		answer := answers[min(len(bodies), len(answers))-1]
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": answer}}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

// planRunner is a runner on the fake model, bound to a conversation, as the gateway builds one.
func planRunner(t *testing.T, url, id string) *AppRunner {
	t.Helper()
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = url
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.BackoffInitial = time.Millisecond
	cfg.LLM.BackoffMax = time.Millisecond
	cfg.Sandbox.Kind = "none"
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine
	r.SetContextID(id)
	return r
}

// TestAPlanSurvivesAGatewayRestartMidway: a plan is given, the gateway restarts (a new runner on the
// same disk, with the transcript the gateway restores), and the next question reaches the model
// with the plan and the turn before it, and the plan is still the one kept.
func TestAPlanSurvivesAGatewayRestartMidway(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	plan := "1. pack the bags\n2. book the hotel\n3. take the train"
	srv, bodies := streamingLLM(t, plan, "Step 2 is the hotel.")

	before := planRunner(t, srv.URL, "conv-restart")
	if _, err := before.RunPlan(context.Background(), "plan my trip", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if before.PinnedPlan() != plan {
		t.Fatalf("the plan was not pinned: %q", before.PinnedPlan())
	}
	saved := before.Transcript()

	after := planRunner(t, srv.URL, "conv-restart")
	after.RestoreTranscript(saved)
	if got := after.PinnedPlan(); got != plan {
		t.Errorf("after the restart the plan reads %q", got)
	}
	if _, err := after.RunPlan(context.Background(), "what was step 2?", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	last := (*bodies)[len(*bodies)-1]
	for _, want := range []string{"plan my trip", "book the hotel", "what was step 2?"} {
		if !strings.Contains(last, want) {
			t.Errorf("the request after the restart lacks %q:\n%s", want, last)
		}
	}
	if after.PinnedPlan() != plan {
		t.Errorf("an answer that is not a plan replaced it: %q", after.PinnedPlan())
	}
}

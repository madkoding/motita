package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/task"
)

// The structured report of a finished task reaches the interface, is delivered once, and a turn
// without one does not inherit the previous turn's.
func TestTheStructuredReportIsHandedOverOnce(t *testing.T) {
	r := NewAppRunner(&strings.Builder{}, &strings.Builder{}, config.Default(),
		&llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	res := agent.TaskResult{Pass: true, Summary: "done", Report: &agent.Report{Version: 1, Status: "done", Summary: "done"}}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return &askingAgent{res: res}
	}
	if _, err := r.RunTask(context.Background(), "x", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	rep := r.TakeReport()
	if rep == nil || rep.Status != "done" {
		t.Fatalf("the report must reach the interface, got %+v", rep)
	}
	if again := r.TakeReport(); again != nil {
		t.Errorf("taking must clear, got %+v", again)
	}
	// A turn that produced none must not leave the last one behind.
	res.Report = nil
	res.Summary = "chat"
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return &askingAgent{res: res}
	}
	_, _ = r.RunTask(context.Background(), "y", func(string, ...any) {})
	if got := r.TakeReport(); got != nil {
		t.Errorf("a turn with no report returned %+v", got)
	}
}

// The terminal shows the report as plain text, and a result with no structured report is unchanged.
func TestSummariseDrawsTheReportAsText(t *testing.T) {
	tr := agent.TaskResult{Pass: true, Summary: "s", Report: &agent.Report{Summary: "s",
		Verification: []agent.ReportCheck{{Check: "go test", Result: "pass", Evidence: "3 passed"}}}}
	if got := summarise(tr); got != "s\n\nChecked:\n  ok   go test - 3 passed" {
		t.Errorf("summarise = %q", got)
	}
	tr.Report = nil
	if got := summarise(tr); got != "s" {
		t.Errorf("without a report the summary stands alone, got %q", got)
	}
}

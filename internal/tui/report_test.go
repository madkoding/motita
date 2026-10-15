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
	res := agent.TaskResult{Pass: true, Summary: "done",
		Report: &agent.Report{Version: 1, Status: "done", Summary: "done"}}
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

// The questions a turn asked reach the gateway's clients once, without emptying the window's own
// copy, and a turn that asked nothing does not hand over the previous turn's.
func TestTheQuestionsOfATurnAreHandedOverOnce(t *testing.T) {
	r := NewAppRunner(&strings.Builder{}, &strings.Builder{}, config.Default(),
		&llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	asked := []agent.AskItem{{Text: "which login?", Options: []string{"email", "GitHub"}}, {Text: "where?"}}
	res := agent.TaskResult{NeedsInput: true, Question: "which login?", Questions: asked}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return &askingAgent{res: res}
	}
	out, _ := r.RunTask(context.Background(), "add login", func(string, ...any) {})
	if !strings.Contains(out, "2. where?") || !strings.Contains(out, "   - GitHub") {
		t.Errorf("the answer text must list every question with its options, got %q", out)
	}
	if got := r.TakeQuestions(); len(got) != 2 || got[1].Text != "where?" {
		t.Fatalf("TakeQuestions = %+v", got)
	}
	if again := r.TakeQuestions(); again != nil {
		t.Errorf("taking must clear, got %+v", again)
	}
	if items, _ := r.TakePendingQuestions(); len(items) != 2 {
		t.Errorf("the window keeps its own copy, got %+v", items)
	}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return &askingAgent{res: agent.TaskResult{Pass: true, Summary: "done"}}
	}
	_, _ = r.RunTask(context.Background(), "y", func(string, ...any) {})
	if got := r.TakeQuestions(); got != nil {
		t.Errorf("a turn that asked nothing returned %+v", got)
	}
}

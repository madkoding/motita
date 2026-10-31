package tui

import (
	"bytes"
	"context"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/memory"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/skills"
	taskpkg "github.com/madkoding/motita/internal/task"
	"github.com/madkoding/motita/internal/usage"
)

// wiringAgent implements the optional interfaces RunTask type-asserts for, so the wiring can
// be asserted without a real agent.
type wiringAgent struct {
	lib     *skills.Library
	usage   *usage.Ledger
	vault   *memory.Store
	threads func(string) string
	recall  func(context.Context, string, int) (string, int)
}

func (w *wiringAgent) SetLibrary(l *skills.Library)     { w.lib = l }
func (w *wiringAgent) SetUsage(u *usage.Ledger)         { w.usage = u }
func (w *wiringAgent) SetMemory(v *memory.Store)        { w.vault = v }
func (w *wiringAgent) SetThreads(f func(string) string) { w.threads = f }
func (w *wiringAgent) SetRecall(f func(context.Context, string, int) (string, int)) {
	w.recall = f
}
func (w *wiringAgent) SetTranscript([]agent.DialogueTurn) {}
func (w *wiringAgent) Transcript() []agent.DialogueTurn   { return nil }
func (w *wiringAgent) Run(context.Context) error          { return nil }
func (w *wiringAgent) RunCommand(context.Context, string) (string, int, error) {
	return "", 0, nil
}

// Both modes reach ONE shelf of procedures, one memory and one archive of the conversation: two
// libraries would be two bodies of knowledge that drift apart.
func TestTaskModeGetsTheSameLibraryAndMemoryAsPlan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Default()
	cfg.Skills.Dir = t.TempDir()
	cfg.Agent.WorkspaceDir = t.TempDir()

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	wired := &wiringAgent{}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return wired
	}

	if _, err := r.RunTask(context.Background(), "a task", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if wired.lib == nil || wired.lib != r.library() {
		t.Error("Task mode must be given the same library instance Plan mode uses")
	}
	if wired.vault == nil {
		t.Error("Task mode must be given the memory")
	}
	if wired.recall == nil {
		t.Error("Task mode must be given the conversation's archive, or it cannot recall earlier turns")
	}
	if wired.threads != nil {
		t.Error("a runner nobody gave the thread action to must not hand one out")
	}
	r.SetThreads(func(string) string { return "routed" })
	if _, err := r.RunTask(context.Background(), "a task", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if wired.threads == nil || wired.threads("x") != "routed" {
		t.Error("Task mode must be given the thread action the gateway installed")
	}
	// And the same usage sidecar the library screen reads, so what Task mode consults is counted.
	if wired.usage == nil || wired.usage != r.procedures().Usage {
		t.Error("Task mode must be given the library screen's usage ledger")
	}
}

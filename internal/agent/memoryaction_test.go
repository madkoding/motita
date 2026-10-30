package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/memory"
	"github.com/madkoding/motita/internal/skills"
)

func TestMemoryActionsReachTheMemory(t *testing.T) {
	a := &Agent{cfg: config.Default(), log: logx.Global()}
	a.SetMemory(memory.Open(t.TempDir()))
	var trace []string
	a.SetProgress(func(format string, args ...any) { trace = append(trace, fmt.Sprintf(format, args...)) })
	rr := &roundRun{a: a, ctx: context.Background()}
	if rr.memoryAction("command", Command{}) {
		t.Fatal("only memory_* kinds are this action")
	}
	run := func(kind, command string) string {
		rr.sb.Reset()
		if !rr.memoryAction(kind, Command{Description: "why", Command: command}) {
			t.Fatalf("%s must be handled", kind)
		}
		return rr.sb.String()
	}
	out := run("memory_write", "trips/Japan :: Ramen in Tokyo")
	if !strings.Contains(out, "[memory_write] why") || !strings.Contains(out, "Created [[Japan]]") {
		t.Errorf("write = %q", out)
	}
	if out := run("memory_append", "Japan :: Go in May"); !strings.Contains(out, "Added to [[Japan]]") {
		t.Errorf("append = %q", out)
	}
	if out := run("memory_search", "ramen"); !strings.Contains(out, "[[Japan]] (trips/Japan.md)") {
		t.Errorf("search = %q", out)
	}
	if out := run("memory_read", "Japan"); !strings.Contains(out, "Go in May") {
		t.Errorf("read = %q", out)
	}
	if strings.Join(trace, "|") != "saved to memory: Japan (trips/Japan.md)|saved to memory: Japan (trips/Japan.md)" {
		t.Errorf("the saves were not reported: %q", trace)
	}
	for _, kind := range []string{"memory_write", "memory_append"} {
		if out := run(kind, "no separator"); !strings.Contains(out, "expects \"entry :: the Markdown\"") {
			t.Errorf("%s without :: = %q", kind, out)
		}
	}
}

func TestMemoryActionsSayWhenThereIsNoMemory(t *testing.T) {
	a := &Agent{cfg: config.Default(), log: logx.Global()}
	rr := &roundRun{a: a, ctx: context.Background()}
	rr.memoryAction("memory_search", Command{Command: "x"})
	if !strings.Contains(rr.sb.String(), "no memory") {
		t.Errorf("out = %q", rr.sb.String())
	}
}

// recall and remember reach the memory, the library, the project's lessons and the conversation
// from one action each, with the place and "open:" written in the command.
func TestRecallAndRememberActions(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.WorkspaceDir = t.TempDir()
	a := &Agent{cfg: cfg, log: logx.Global()}
	a.SetMemory(memory.Open(t.TempDir()))
	a.SetLibrary(skills.New(t.TempDir()))
	a.SetRecall(func(_ context.Context, q string, live int) (string, int) {
		return "Turn 1: the person picked " + q, 1
	})
	rr := &roundRun{a: a, ctx: context.Background()}
	run := func(kind, command string) string {
		rr.sb.Reset()
		if !rr.memoryAction(kind, Command{Description: "why", Command: command}) {
			t.Fatalf("%s must be handled", kind)
		}
		return rr.sb.String()
	}
	if out := run("remember", "Trips/Japan :: Ramen in Tokyo"); !strings.Contains(out, "Added to [[Japan]]") {
		t.Errorf("remember = %q", out)
	}
	if out := run("remember", "IN:procedures ramen-hunt :: # Ramen hunt\n1. ask"); !strings.Contains(out, "Saved \"ramen-hunt\"") {
		t.Errorf("remember a procedure = %q", out)
	}
	if out := run("remember", "in:project the menu is in Japanese"); !strings.Contains(out, "this project's lessons") {
		t.Errorf("remember a lesson = %q", out)
	}
	out := run("recall", "ramen japanese")
	for _, want := range []string{"[[Japan]]", "ramen-hunt", "the menu is in Japanese", "the person picked ramen"} {
		if !strings.Contains(out, want) {
			t.Errorf("recall lacks %q:\n%s", want, out)
		}
	}
	if out := run("recall", "in:procedures open: ramen-hunt"); !strings.Contains(out, "1. ask") {
		t.Errorf("open a procedure = %q", out)
	}
	if out := run("recall", "in:"); !strings.Contains(out, "say what to look for") {
		t.Errorf("an empty recall = %q", out)
	}
}

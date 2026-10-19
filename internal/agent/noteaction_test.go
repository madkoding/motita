package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/notes"
)

func TestNoteActionsReachTheVault(t *testing.T) {
	a := &Agent{cfg: config.Default(), log: logx.Global()}
	a.SetNotes(notes.Open(t.TempDir()))
	rr := &roundRun{a: a, ctx: context.Background()}
	if rr.noteAction("command", Command{}) {
		t.Fatal("only note_* kinds are this action")
	}
	run := func(kind, command string) string {
		rr.sb.Reset()
		if !rr.noteAction(kind, Command{Description: "why", Command: command}) {
			t.Fatalf("%s must be handled", kind)
		}
		return rr.sb.String()
	}
	out := run("note_write", "trips/Japan :: Ramen in Tokyo")
	if !strings.Contains(out, "[note_write] why") || !strings.Contains(out, "Created [[Japan]]") {
		t.Errorf("write = %q", out)
	}
	if out := run("note_append", "Japan :: Go in May"); !strings.Contains(out, "Added to [[Japan]]") {
		t.Errorf("append = %q", out)
	}
	if out := run("note_search", "ramen"); !strings.Contains(out, "[[Japan]] (trips/Japan.md)") {
		t.Errorf("search = %q", out)
	}
	if out := run("note_read", "Japan"); !strings.Contains(out, "Go in May") {
		t.Errorf("read = %q", out)
	}
	for _, kind := range []string{"note_write", "note_append"} {
		if out := run(kind, "no separator"); !strings.Contains(out, "expects \"note :: the Markdown\"") {
			t.Errorf("%s without :: = %q", kind, out)
		}
	}
}

func TestNoteActionsSayWhenThereIsNoVault(t *testing.T) {
	a := &Agent{cfg: config.Default(), log: logx.Global()}
	rr := &roundRun{a: a, ctx: context.Background()}
	rr.noteAction("note_search", Command{Command: "x"})
	if !strings.Contains(rr.sb.String(), "no note vault") {
		t.Errorf("out = %q", rr.sb.String())
	}
}

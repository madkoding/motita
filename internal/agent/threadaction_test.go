package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

func TestTheThreadActionGoesThroughTheRouter(t *testing.T) {
	a := &Agent{cfg: config.Default(), log: logx.Global()}
	rr := &roundRun{a: a, ctx: context.Background()}
	if rr.threadAction("command", Command{}) {
		t.Fatal("only the thread kind is this action")
	}
	if !rr.threadAction("thread", Command{Description: "why", Command: "list"}) ||
		!strings.Contains(rr.sb.String(), "cannot open threads") {
		t.Errorf("a session without a router says so: %q", rr.sb.String())
	}
	rr.sb.Reset()
	a.SetThreads(func(command string) string { return "routed " + command })
	rr.threadAction("thread", Command{Description: "why", Command: "red :: box"})
	if out := rr.sb.String(); !strings.Contains(out, "[thread] why") || !strings.Contains(out, "routed red :: box") {
		t.Errorf("out = %q", out)
	}
}

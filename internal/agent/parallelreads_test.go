package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

// TestReadsOfARoundRunSideBySide: consecutive reads run at the same time, and their output is
// reported in the order the model wrote them; the write after them runs on its own.
func TestReadsOfARoundRunSideBySide(t *testing.T) {
	a := agentWith(t, false)
	var inFlight, most atomic.Int32
	a.ExecCommand = func(_ context.Context, r execx.Request) (string, bool, int, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		line := r.Args[len(r.Args)-1]
		if strings.Contains(line, "a.txt") {
			time.Sleep(30 * time.Millisecond) // the first one ends last
		} else {
			time.Sleep(5 * time.Millisecond)
		}
		if strings.Contains(line, "broken") {
			return "", false, -1, errors.New("no executor")
		}
		return "out of " + line, false, 0, nil
	}
	out, err := a.runActions(context.Background(), []Command{
		{Command: "cat a.txt"}, {Command: "cat b.txt"}, {Command: "ls broken"}, {Command: "touch c.txt"},
	}, "")
	if most.Load() < 2 {
		t.Errorf("the reads must run at the same time, at most %d ran together", most.Load())
	}
	ia := strings.Index(out, "out of cat a.txt")
	ib := strings.Index(out, "out of cat b.txt")
	ic := strings.Index(out, "touch c.txt")
	if ia < 0 || ib < ia || ic < ib {
		t.Errorf("the output must keep the model's order:\n%s", out)
	}
	if err == nil || !strings.Contains(err.Error(), "action 3 (ls broken) could not run") {
		t.Errorf("a read that could not run must be the round's error, numbered in the round: %v", err)
	}
	if a.clock.commandRuns.Load() != 4 {
		t.Errorf("every command is counted once: %d", a.clock.commandRuns.Load())
	}
}

// TestAReadThatAsksOrIsHeldRunsOnItsOwn: a read the policy asks about, one the working memory
// already holds, and anything that is not a read do not join a batch.
func TestAReadThatAsksOrIsHeldRunsOnItsOwn(t *testing.T) {
	a := agentWith(t, false)
	rr := &roundRun{a: a, ctx: context.Background(), mem: &workMemory{}}
	rr.mem.remember("cat held.txt", 1, "kept")
	if rr.parallelRead(Command{Command: "cat held.txt"}) {
		t.Error("a read the memory holds is answered from it, not run")
	}
	if rr.parallelRead(Command{Command: "touch x"}) || rr.parallelRead(Command{Command: " "}) {
		t.Error("only a read may join a batch")
	}
	saved := confinementAvailable
	confinementAvailable = func() bool { return false }
	defer func() { confinementAvailable = saved }()
	if rr.parallelRead(Command{Command: "cat ~/.ssh/id_rsa"}) {
		t.Error("a read the policy asks about goes to the question on its own")
	}
	if n := rr.readBatch([]Command{{Command: "cat a"}, {Command: "touch b"}}); n != 1 {
		t.Errorf("a batch stops at the first action that is not a parallel read: %d", n)
	}
}

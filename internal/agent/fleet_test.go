package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/llm"
)

// lineSink collects published lines, safely: the fleet publishes from timers and from the agents'
// own goroutines.
type lineSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *lineSink) add(line string) {
	s.mu.Lock()
	s.lines = append(s.lines, line)
	s.mu.Unlock()
}

func (s *lineSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.lines)
}

func (s *lineSink) last(t *testing.T) []AgentInfo {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lines) == 0 {
		t.Fatal("nothing was published")
	}
	agents, ok := ParseAgentsLine(s.lines[len(s.lines)-1])
	if !ok {
		t.Fatalf("not an agents line: %q", s.lines[len(s.lines)-1])
	}
	return agents
}

// fleetTiming sets the fleet's timing for one test.
func fleetTiming(t *testing.T, throttle, tick time.Duration) {
	t.Helper()
	oldThrottle, oldTick := fleetThrottle, fleetTick
	fleetThrottle, fleetTick = throttle, tick
	t.Cleanup(func() { fleetThrottle, fleetTick = oldThrottle, oldTick })
}

// waitFor polls until cond holds or the test's patience runs out.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A run with no agents of its own and no counted tokens publishes nothing: a list with one silent
// row on every task would be noise.
func TestAFleetWithNothingToShowPublishesNothing(t *testing.T) {
	fleetTiming(t, 0, time.Millisecond)
	sink := &lineSink{}
	f := newFleet(sink.add)
	m := f.add("", "main", "the task", "")
	f.setActivity(m, "running: ls\nsecond line")
	f.setRound(m, 2)
	time.Sleep(20 * time.Millisecond)
	f.finish(m, AgentPassed, "")
	f.close()
	if sink.count() != 0 {
		t.Fatalf("published %d lines for a run with nothing to show", sink.count())
	}
}

// Tokens are something to show: the main agent's row is published once it has spent some.
func TestTheMainAgentsTokensArePublished(t *testing.T) {
	fleetTiming(t, 0, time.Hour)
	sink := &lineSink{}
	f := newFleet(sink.add)
	m := f.add("", "main", "the task", "")
	m.meter.Add(llm.Usage{Input: 100, Output: 20})
	f.setRound(m, 1)
	got := sink.last(t)
	if len(got) != 1 || got[0].Tokens.Input != 100 || got[0].Calls != 1 || got[0].Round != 1 {
		t.Fatalf("snapshot = %+v", got)
	}
	f.close()
}

// Changes are throttled: a burst produces one line now and one when the throttle allows, never one
// per change. Nothing is published after the fleet closes, and closing twice is harmless.
func TestTheFleetThrottlesAndStopsAtClose(t *testing.T) {
	fleetTiming(t, 80*time.Millisecond, time.Hour)
	sink := &lineSink{}
	f := newFleet(sink.add)
	main := f.add("", "main", "the task", "")
	child := f.add("main", "a1", "write the docs", "motita/sub/x-a1") // an agent of its own: worth showing
	if sink.count() != 1 {
		t.Fatalf("the first change publishes at once, got %d lines", sink.count())
	}
	for i := 0; i < 10; i++ {
		f.setActivity(child, "running: step")
	}
	if sink.count() != 1 {
		t.Fatalf("a burst inside the throttle must wait, got %d lines", sink.count())
	}
	waitFor(t, "the throttled line", func() bool { return sink.count() == 2 })
	if got := sink.last(t); len(got) != 2 || got[1].Activity != "running: step" || got[1].Branch != "motita/sub/x-a1" {
		t.Fatalf("snapshot = %+v", got)
	}
	f.finish(child, AgentPassed, "the docs are written")
	f.finish(main, AgentPassed, "")
	f.close()
	final := sink.last(t)
	if final[1].State != AgentPassed || final[1].Summary != "the docs are written" || final[1].Finished.IsZero() {
		t.Fatalf("final snapshot = %+v", final)
	}
	n := sink.count()
	f.setActivity(main, "after the end")
	f.close()
	time.Sleep(120 * time.Millisecond)
	if sink.count() != n {
		t.Fatalf("published after close: %d -> %d", n, sink.count())
	}
}

// While an agent runs the fleet ticks, so elapsed time and tokens move on screen; once nothing
// runs, it stops ticking.
func TestTheFleetTicksOnlyWhileAnAgentRuns(t *testing.T) {
	fleetTiming(t, 0, 10*time.Millisecond)
	sink := &lineSink{}
	f := newFleet(sink.add)
	main := f.add("", "main", "the task", "")
	child := f.add("main", "a1", "research", "")
	waitFor(t, "ticks", func() bool { return sink.count() >= 3 })
	f.finish(child, AgentFailed, "")
	f.finish(main, AgentPassed, "")
	n := sink.count()
	time.Sleep(50 * time.Millisecond)
	if sink.count() != n {
		t.Fatalf("the fleet kept ticking with nothing running: %d -> %d", n, sink.count())
	}
	f.close()
}

// The elapsed time of a finished agent is fixed at its end; a running one's is measured now.
func TestTheSnapshotMeasuresElapsedTime(t *testing.T) {
	fleetTiming(t, time.Hour, time.Hour)
	start := time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)
	now := start
	old := fleetNow
	fleetNow = func() time.Time { return now }
	t.Cleanup(func() { fleetNow = old })
	f := newFleet(nil) // no sink: publishing is a no-op
	a := f.add("", "main", "the task", "")
	b := f.add("main", "a1", "docs", "")
	now = start.Add(3 * time.Second)
	f.finish(b, AgentCancelled, "")
	now = start.Add(10 * time.Second)
	snap := f.Snapshot()
	if snap[0].ElapsedMS != 10000 || snap[1].ElapsedMS != 3000 || snap[1].State != AgentCancelled {
		t.Fatalf("snapshot = %+v", snap)
	}
	f.finish(a, AgentPassed, "")
	f.close()
}

// An agent outside a run has no fleet, and every fleet operation on it is a no-op.
func TestANilFleetIsANoOp(t *testing.T) {
	var f *Fleet
	if m := f.add("", "main", "x", ""); m != nil {
		t.Fatal("a nil fleet registers nothing")
	}
	f.setActivity(nil, "x")
	f.setRound(nil, 1)
	f.finish(nil, AgentPassed, "")
	if f.Snapshot() != nil {
		t.Fatal("a nil fleet has no snapshot")
	}
	f.close()
	(&Agent{}).noteRound(3)
}

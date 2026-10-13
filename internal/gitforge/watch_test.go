package gitforge

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeSource answers each reading from a list, one entry per reading; the last repeats.
type fakeSource struct {
	reads []fakeRead
	n     int
	now   fakeRead
}

type fakeRead struct {
	snap Snapshot
	err  error
	// failAt names the call that fails with err: "" (the state), "ci", "merge" or "activity".
	failAt string
}

func (f *fakeSource) fail(call string) error {
	if f.now.failAt == call {
		return f.now.err
	}
	return nil
}

func (f *fakeSource) PRState(context.Context, Remote, int) (PRState, error) {
	f.now = f.reads[min(f.n, len(f.reads)-1)]
	f.n++
	return f.now.snap.State, f.fail("")
}

func (f *fakeSource) CI(context.Context, Remote, int) (CIStatus, error) {
	return f.now.snap.CI, f.fail("ci")
}

func (f *fakeSource) MergeState(context.Context, Remote, int) (MergeState, error) {
	return f.now.snap.Merge, f.fail("merge")
}

func (f *fakeSource) Activity(context.Context, Remote, int) (Activity, error) {
	return f.now.snap.Activity, f.fail("activity")
}

func open(ci string, rev string, merge string, comments ...string) Snapshot {
	s := Snapshot{State: PRState{State: PROpen}, CI: CIStatus{State: ci, Rev: rev}, Merge: MergeState{merge}}
	if ci == StateFailure {
		s.CI.Checks = []Check{{Name: "test", State: StateFailure}}
	}
	for _, id := range comments {
		s.Activity.Comments = append(s.Activity.Comments, Comment{ID: id})
	}
	return s
}

func kinds(evs []Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func TestTrackerReportsEachThingOnce(t *testing.T) {
	var tr Tracker
	// The baseline: comments already there are not news, a running CI is not news.
	if evs := tr.Observe(open(StatePending, "a", MergeOK, "c:1")); len(evs) != 0 {
		t.Fatalf("baseline = %v", kinds(evs))
	}
	evs := tr.Observe(open(StateFailure, "a", MergeConflict, "c:1", "c:2"))
	if got := kinds(evs); len(got) != 3 || got[0] != EventReview || got[1] != EventConflict || got[2] != EventCIFailed {
		t.Fatalf("events = %v", got)
	}
	if len(evs[0].Comments) != 1 || evs[0].Comments[0].ID != "c:2" || evs[2].CI == nil || !evs[0].Actionable() {
		t.Errorf("events = %+v", evs)
	}
	// The same reading again is not news.
	if evs := tr.Observe(open(StateFailure, "a", MergeConflict, "c:1", "c:2")); len(evs) != 0 {
		t.Errorf("repeat = %v", kinds(evs))
	}
	// A new push that passes is; the conflict cleared and comes back.
	if got := kinds(tr.Observe(open(StateSuccess, "b", MergeOK))); len(got) != 1 || got[0] != EventCIPassed {
		t.Errorf("pass = %v", got)
	}
	if got := kinds(tr.Observe(open(StateSuccess, "b", MergeConflict))); len(got) != 1 || got[0] != EventConflict {
		t.Errorf("conflict again = %v", got)
	}
	if (Event{Kind: EventCIPassed}).Actionable() {
		t.Error("a pass asks nothing")
	}
}

func TestTrackerNoCIAndTheEnd(t *testing.T) {
	var tr Tracker
	for i := 1; i < NoCILimit; i++ {
		if evs := tr.Observe(open(StateNone, "", MergeOK)); len(evs) != 0 {
			t.Fatalf("reading %d: %v", i, kinds(evs))
		}
	}
	if got := kinds(tr.Observe(open(StateNone, "", MergeOK))); len(got) != 1 || got[0] != EventNoCI {
		t.Fatalf("no ci = %v", got)
	}
	if evs := tr.Observe(open(StateNone, "", MergeOK)); len(evs) != 0 {
		t.Error("no CI is said once")
	}
	if evs := tr.Observe(Snapshot{State: PRState{PRMerged, "s"}}); evs[0].Kind != EventMerged || evs[0].SHA != "s" {
		t.Errorf("merged = %+v", evs)
	}
	if evs := tr.Observe(Snapshot{State: PRState{State: PRClosed}}); evs[0].Kind != EventClosed {
		t.Errorf("closed = %+v", evs)
	}
}

func TestCIKeyWithoutRevision(t *testing.T) {
	ci := CIStatus{State: StateFailure, Checks: []Check{{Name: "b", State: StateFailure}, {Name: "a", State: StateFailure},
		{Name: "c", State: StateSuccess}}}
	if got := CIKey(ci); got != "a,b" {
		t.Errorf("key = %q", got)
	}
}

func TestPace(t *testing.T) {
	p := DefaultPace()
	s := open(StatePending, "a", MergeOK, "c:1")
	s.CI.Checks = []Check{{Name: "x", State: StatePending}}
	p.See(s)
	if p.Wait(0) != 20*time.Second {
		t.Errorf("moving = %v", p.Wait(0))
	}
	for range 20 {
		p.See(s)
	}
	if p.Wait(0) != 120*time.Second {
		t.Errorf("quiet = %v", p.Wait(0))
	}
	if p.Wait(1) != 40*time.Second || p.Wait(9) != 320*time.Second {
		t.Errorf("backoff = %v %v", p.Wait(1), p.Wait(9))
	}
}

func TestWatcherWaitsForNews(t *testing.T) {
	boom := errors.New("down")
	src := &fakeSource{reads: []fakeRead{
		{snap: open(StatePending, "a", MergeOK)},
		{err: boom},
		{snap: open(StatePending, "a", MergeOK), err: boom, failAt: "ci"},
		{snap: open(StatePending, "a", MergeOK), err: boom, failAt: "merge"},
		{snap: open(StatePending, "a", MergeOK), err: boom, failAt: "activity"},
		{snap: open(StateFailure, "a", MergeOK)},
	}}
	reads := 0
	w := &Watcher{Src: src, Number: 1, Sleep: instant, OnRead: func(Snapshot) { reads++ }}
	evs, err := w.Next(context.Background())
	if err != nil || len(evs) != 1 || evs[0].Kind != EventCIFailed {
		t.Fatalf("%v %v", kinds(evs), err)
	}
	if reads != 2 || w.Pace.Every != DefaultPace().Every {
		t.Errorf("reads = %d, pace = %+v", reads, w.Pace)
	}
	// Merged ends the next call at once.
	src.reads = []fakeRead{{snap: Snapshot{State: PRState{State: PRMerged}}}}
	src.n = 0
	if evs, err := w.Next(context.Background()); err != nil || evs[0].Kind != EventMerged {
		t.Errorf("%v %v", kinds(evs), err)
	}
}

func TestWatcherGivesUpOnAFailingHost(t *testing.T) {
	boom := errors.New("down")
	w := &Watcher{Src: &fakeSource{reads: []fakeRead{{err: boom}}}, Sleep: instant}
	if _, err := w.Next(context.Background()); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestWatcherStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	never := func(time.Duration) <-chan time.Time {
		cancel()
		return nil
	}
	w := &Watcher{Src: &fakeSource{reads: []fakeRead{{snap: open(StatePending, "a", MergeOK)}}}, Sleep: never}
	if _, err := w.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	// A reading that fails because the context ended is the context's error, not the host's.
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	w = &Watcher{Src: &fakeSource{reads: []fakeRead{{err: errors.New("x")}}}}
	if _, err := w.Next(ctx2); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestReadSnapshotOfAClosedPRReadsNothingElse(t *testing.T) {
	src := &fakeSource{reads: []fakeRead{{snap: Snapshot{State: PRState{State: PRClosed}}}}}
	s, err := ReadSnapshot(context.Background(), src, Remote{}, 1)
	if err != nil || s.State.State != PRClosed || src.n != 1 {
		t.Errorf("%+v %v %d", s, err, src.n)
	}
}

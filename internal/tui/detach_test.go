package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// creatorRunner is a gateway-backed runner that can also open sessions: the shape the interface has
// when it runs on a gateway. It counts the requests to STOP a run, because leaving a session must
// never be one.
type creatorRunner struct {
	*switcherRunner
	mu      sync.Mutex
	created []string
	stops   int
	// taskStarted, when set, makes RunTask a turn that holds until its context ends, without the
	// embedded fake's lock (which the repaint path also takes).
	taskStarted chan struct{}
	taskEnded   chan error
}

func (r *creatorRunner) RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
	if r.taskStarted == nil {
		return r.switcherRunner.RunTask(ctx, task, progress)
	}
	progress("%s", "step of "+task)
	close(r.taskStarted)
	<-ctx.Done()
	r.taskEnded <- ctx.Err()
	return "", ctx.Err()
}

func (r *creatorRunner) NewSession(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := "snew" + strings.Repeat("x", len(r.created))
	r.created = append(r.created, id)
	return id, nil
}

func (r *creatorRunner) CancelRun(ctx context.Context) (bool, error) {
	r.mu.Lock()
	r.stops++
	r.mu.Unlock()
	return r.switcherRunner.CancelRun(ctx)
}

func (r *creatorRunner) stopRequests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stops
}

// TestNewOpensAnotherSessionWhileATurnRuns is the bug as the user met it: with the agent working,
// /new was refused ("has to wait: motita is still working"), and behind a gateway it would only
// have reset the SAME session anyway. Now it opens a session of its own, the run stays in the one it
// belongs to, and nothing of that run reaches the new conversation.
func TestNewOpensAnotherSessionWhileATurnRuns(t *testing.T) {
	started := make(chan struct{})
	r := &creatorRunner{
		switcherRunner: &switcherRunner{fakeRunner: &fakeRunner{}, current: "default"},
		taskStarted:    started,
		taskEnded:      make(chan error, 1),
	}
	ui := newFakeTUI("", r)
	ui.charMode = true
	ctx := context.Background()

	ui.runTask(ctx, "long task in the first session")
	<-started
	if ui.current == nil {
		t.Fatal("the turn is not in flight, so the test measures nothing")
	}
	ui.queue("a follow-up for the first session")

	handled, quit := ui.handleShortcut(ctx, "/new")
	if !handled || quit {
		t.Fatalf("/new was not handled (handled=%v quit=%v)", handled, quit)
	}
	if ui.viewContains("has to wait") {
		t.Fatalf("/new was refused while a turn ran: %+v", ui.messagesSnapshot())
	}
	if r.current != "snew" {
		t.Fatalf("the interface is on %q, want the new session", r.current)
	}
	if ui.current != nil || ui.isBusy() {
		t.Error("the new session inherited the turn of the one that was left")
	}
	if ui.viewContains("long task in the first session") || ui.viewContains("cancelled.") {
		t.Errorf("the first session's turn reached the new one: %+v", ui.messagesSnapshot())
	}
	if n := r.stopRequests(); n != 0 {
		t.Errorf("leaving the session asked the gateway to stop its run %d time(s)", n)
	}
	// The message queued for the first session is not sent to the new one: it is back in the input.
	if ui.queued != "" || ui.draft != "a follow-up for the first session" {
		t.Errorf("queued=%q draft=%q: the queued message must go back to the input", ui.queued, ui.draft)
	}
	if !ui.viewContains("which has no turns yet") {
		t.Errorf("the new session was not entered visibly: %+v", ui.messagesSnapshot())
	}
	// The interface stopped WAITING for the turn; the run itself is the gateway's.
	if err := <-r.taskEnded; err != context.Canceled {
		t.Errorf("the local wait ended with %v, want it let go", err)
	}
}

// TestAttachLeavesAFollowedRunWithoutStoppingIt: the run followed on its own goroutine kept writing
// into the view after /attach moved to another session - its lines and its "cancelled." landed in
// the conversation entered next. Leaving now ends the follow, waits for it to let go of the view,
// and does not ask the gateway to stop the run.
func TestAttachLeavesAFollowedRunWithoutStoppingIt(t *testing.T) {
	sw := &switcherRunner{
		sessions: []SessionInfo{{ID: "sabc", Running: true}, {ID: "sdef"}},
		current:  "default",
		liveRun:  &liveRun{progress: []string{"working in sabc"}, release: make(chan struct{})},
	}
	r := &creatorRunner{switcherRunner: sw}
	if sw.fakeRunner == nil {
		sw.fakeRunner = &fakeRunner{}
	}
	ui := newFakeTUI("", r)
	ctx := context.Background()

	if err := ui.attachTo(ctx, "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	waitForBusy(t, sw, ui)
	waitUntil(t, func() bool { return ui.viewContains("working in sabc") }, "the followed run never drew")

	sw.liveRun = nil // sdef is idle
	handled, _ := ui.handleShortcut(ctx, "/attach sdef")
	if !handled {
		t.Fatal("/attach was not handled")
	}
	if sw.current != "sdef" {
		t.Fatalf("the interface is on %q, want sdef", sw.current)
	}
	if ui.isBusy() || ui.isFollowing() {
		t.Error("the session entered inherited the run of the one that was left")
	}
	if ui.viewContains("working in sabc") || ui.viewContains("cancelled.") {
		t.Errorf("the run of sabc reached sdef: %+v", ui.messagesSnapshot())
	}
	if n := r.stopRequests(); n != 0 {
		t.Errorf("leaving the session asked the gateway to stop its run %d time(s)", n)
	}
}

// TestWithoutAGatewayNewStillWaitsForTheTurn: a run that is this process's own writes into the one
// conversation there is, so /new still has to wait for it.
func TestWithoutAGatewayNewStillWaitsForTheTurn(t *testing.T) {
	started := make(chan struct{})
	r := &localBlockRunner{fakeRunner: &fakeRunner{}, started: started}
	ui := newFakeTUI("", r)
	ui.charMode = true
	ctx, cancel := context.WithCancel(context.Background())
	ui.runTask(ctx, "local task")
	<-started
	defer func() {
		cancel()
		ui.finishTurn(context.Background())
	}()

	ui.handleShortcut(ctx, "/new")
	if !ui.viewContains("has to wait") {
		t.Errorf("/new was not refused while a local turn ran: %+v", ui.messagesSnapshot())
	}
}

// localBlockRunner is a runner with no gateway whose turn holds until its context ends.
type localBlockRunner struct {
	*fakeRunner
	started chan struct{}
}

func (r *localBlockRunner) RunTask(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
	close(r.started)
	<-ctx.Done()
	return "", ctx.Err()
}

// TestANewSessionThatCannotBeOpenedIsReported: the gateway refusing (at its ceiling, say) leaves the
// user where they were, told why.
func TestANewSessionThatCannotBeOpenedIsReported(t *testing.T) {
	r := &failingCreator{switcherRunner: &switcherRunner{fakeRunner: &fakeRunner{}, current: "default"}}
	ui := newFakeTUI("", r)
	ui.handleShortcut(context.Background(), "/new")
	if r.current != "default" {
		t.Errorf("the interface moved to %q after a failed /new", r.current)
	}
	if !ui.viewContains("a new conversation could not be opened: full") {
		t.Errorf("the failure was not reported: %+v", ui.messagesSnapshot())
	}
}

type failingCreator struct{ *switcherRunner }

func (failingCreator) NewSession(context.Context) (string, error) {
	return "", errFull
}

var errFull = errorString("full")

type errorString string

func (e errorString) Error() string { return string(e) }

// TestANewSessionThatCannotBeEnteredIsReported: the session was opened but switching to it failed;
// the user is told, rather than left typing into the session they meant to leave.
func TestANewSessionThatCannotBeEnteredIsReported(t *testing.T) {
	r := &creatorRunner{switcherRunner: &switcherRunner{
		fakeRunner: &fakeRunner{}, current: "default", err: errorString("gateway gone"),
	}}
	ui := newFakeTUI("", r)
	ui.handleShortcut(context.Background(), "/new")
	if !ui.viewContains("gateway gone") {
		t.Errorf("the failed switch was not reported: %+v", ui.messagesSnapshot())
	}
}

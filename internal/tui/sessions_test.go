package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// switcherRunner is a Runner that ALSO offers conversations, which is the capability these tests
// are about. It is deliberately not the package's existing fakeRunner: whether a runner offers
// sessions is a property of the runner, and testing it with one that does not would test nothing.
type switcherRunner struct {
	*fakeRunner
	sessions []SessionInfo
	current  string
	err      error
	// transcript is the conversation of each session, keyed by id. It is what coming BACK to a
	// conversation reads, so a double without it can only exercise the empty case.
	transcript map[string][]Turn
	// conversationErr fails the READ alone, so "the switch happened but the conversation could
	// not be read" is a branch a test can reach without also failing the switch.
	conversationErr error
	// liveRun is the run in flight in the gateway, and nil when the conversation is idle. It is
	// not the embedded fakeRunner's block channels: those describe a run THIS interface started.
	liveRun *liveRun
	// liveErr fails the question "is a run in flight", so the branch that reports a gateway that
	// could not be asked is reachable.
	liveErr error
	// follows counts the follows this double was asked for, so a test can tell that the interface
	// went after a run without polling the interface's own flag.
	follows int
	mu      sync.Mutex
}

func (r *switcherRunner) Conversation(context.Context) ([]Turn, error) {
	if r.conversationErr != nil {
		return nil, r.conversationErr
	}
	return r.transcript[r.current], nil
}

// liveRun is a run in flight in the GATEWAY, belonging to nobody in this process: the interface
// did not start it and holds no local state for it. That is the case this whole task is about, so a
// double that could only describe a run the interface started would test nothing.
type liveRun struct {
	// progress is what the run says while it works, in order.
	progress []string
	// release holds the run open until the test lets it end. A run that ended instantly could not
	// show that the work is VISIBLE while it happens.
	release chan struct{}
	// result is what the run reports when it ends, and err fails the follow instead.
	result string
	err    error
	mu     sync.Mutex
	// cancels counts the requests to stop this run that reached the gateway.
	cancels int
}

// cancelRequests is how many times the run was asked to stop.
func (l *liveRun) cancelRequests() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cancels
}

// recordCancel counts one request to stop the run.
//
// It does NOT unblock the run: the blocked follow is released by the cancelled CONTEXT, which is
// what a real cancel does, and a test that wants the run to end sends on release itself.
func (l *liveRun) recordCancel() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancels++
}

// LiveRun reports the run in flight in the gateway, if there is one.
//
// A nil liveRun is a conversation that is simply sitting there, which is what most attaches meet;
// liveErr is the gateway that cannot be asked at all.
func (r *switcherRunner) LiveRun(context.Context) (LiveRun, bool, error) {
	if r.liveErr != nil {
		return LiveRun{}, false, r.liveErr
	}
	if r.liveRun == nil {
		return LiveRun{}, false, nil
	}
	return LiveRun{RunID: "run-1", LastSeq: 3}, true, nil
}

// FollowRun reports the run's progress until it ends, and asks the gateway to stop it when the
// follow is cancelled.
//
// That last part is the CLIENT's job in production - see gateway.Client.FollowRun, which arms a
// context.AfterFunc - and modelling it here is what lets a test measure the interface's half: that
// Escape cancels the follow.
func (r *switcherRunner) FollowRun(ctx context.Context, progress func(string, ...any)) (string, error) {
	live := r.liveRun
	if live == nil {
		return "", errors.New("there is no run in flight in this session to attach to")
	}
	r.noteFollow()
	for _, line := range live.progress {
		progress("%s", line)
	}
	select {
	case <-live.release:
	case <-ctx.Done():
		// The follow was cancelled, and a cancelled follow ASKS THE GATEWAY to stop the run. That
		// is the contract the interface relies on - see RunController.FollowRun and the client's
		// context.AfterFunc - and counting it here is what lets a test measure Escape.
		r.cancelRunAtGateway()
		return "", ctx.Err()
	}
	if live.err != nil {
		return "", live.err
	}
	return live.result, nil
}

// CancelRun asks the gateway to stop the run in flight.
func (r *switcherRunner) CancelRun(context.Context) (bool, error) {
	if r.liveRun == nil {
		return false, nil
	}
	r.cancelRunAtGateway()
	return true, nil
}

func (r *switcherRunner) cancelRunAtGateway() {
	if r.liveRun != nil {
		r.liveRun.recordCancel()
	}
}

// cancelCalls is how many stop requests reached the gateway.
func (r *switcherRunner) cancelCalls() int {
	if r.liveRun == nil {
		return 0
	}
	return r.liveRun.cancelRequests()
}

// waitForBusy waits until the interface reports a turn in flight, by polling the flag rather than
// sleeping a fixed time - a fixed sleep is a test that passes on a fast machine and fails on a slow
// one, and neither outcome says anything about the code.
func waitForBusy(t *testing.T, r *switcherRunner, ui *TUI) {
	t.Helper()
	waitUntil(t, func() bool { return r.followCount() > 0 && ui.isBusy() },
		"the interface never went after the live run")
}

// waitForIdle waits until a followed run has been settled, which is how a test knows the turn is
// over instead of racing it.
//
// It waits for the follow to have STARTED before waiting for it to end. The follow takes a moment
// to be installed - it runs on its own goroutine - and asserting on the view before it began would
// measure a turn that had not been drawn.
func waitForIdle(t *testing.T, r *switcherRunner, ui *TUI) {
	t.Helper()
	waitUntil(t, func() bool { return r.followCount() > 0 }, "the interface never followed the live run")
	waitUntil(t, func() bool { return !ui.isBusy() }, "the interface never went idle after the run ended")
}

// isFollowing reports that a run is being followed right now: the turn's cancel func is installed,
// which is the state Escape acts on and the state a progress line can be drawn from.
//
// It is the cancel func rather than the busy flag because a short run can begin and end between two
// polls: the cancel is the thing the test needs to have happened, and it is set for the whole life
// of the follow.
func (t *TUI) isFollowing() bool {
	return t.currentCancel() != nil
}

// waitForCancelArmed waits until the followed run can be stopped, which is the precondition of
// pressing Escape.
func waitForCancelArmed(t *testing.T, ui *TUI) {
	t.Helper()
	waitUntil(t, ui.isFollowing, "the followed run never became stoppable, so Escape could not be measured")
}

// waitUntil polls cond until it holds, and fails the test if it never does.
//
// It is deliberately not the package's existing waitFor (resize_test.go), which waits for text to
// appear in a buffer: this waits on a CONDITION, and naming it after what it polls keeps the two
// from being confused at a call site.
func waitUntil(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// isBusy reports whether a turn is in flight, and takes the lock because a followed run reports
// its progress from its own goroutine.
func (t *TUI) isBusy() bool {
	t.draw.Lock()
	defer t.draw.Unlock()
	return t.busy
}

// viewContains reports whether anything on screen holds s.
func (t *TUI) viewContains(s string) bool {
	t.draw.Lock()
	defer t.draw.Unlock()
	for _, m := range t.messages {
		if strings.Contains(m.Text, s) {
			return true
		}
	}
	return false
}

// listingOnlyRunner offers conversations and NOT control of a run: the shape of a front end backed
// by something that is not the gateway. It is the double the capability-is-optional tests need,
// because a runner that had both could not tell whether either was required.
type listingOnlyRunner struct {
	*fakeRunner
	sessions   []SessionInfo
	transcript map[string][]Turn
	current    string
	err        error
}

func (r *listingOnlyRunner) ListSessions(context.Context) ([]SessionInfo, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.sessions, nil
}

func (r *listingOnlyRunner) SwitchSession(_ context.Context, id string) error {
	r.current = id
	return nil
}

func (r *listingOnlyRunner) CurrentSession() string { return r.current }

func (r *listingOnlyRunner) Conversation(context.Context) ([]Turn, error) {
	return r.transcript[r.current], nil
}

// followCount is how many times this double was asked to follow a run. It is how a test knows the
// interface went after the turn at all, without polling a flag the interface clears when the run
// ends - a short run can start and finish between two polls, and a test that sampled the flag would
// be measuring its own timing rather than the interface.
func (r *switcherRunner) followCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.follows
}

func (r *switcherRunner) noteFollow() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.follows++
}

// switcher builds the double with the embedded fakeRunner ready, and a TUI whose output can be
// read back, which is how the package's own tests inspect what was drawn.
func switcher(r *switcherRunner) *TUI {
	if r.fakeRunner == nil {
		r.fakeRunner = &fakeRunner{}
	}
	ui := newFakeTUI("", r)
	return ui
}

func (r *switcherRunner) ListSessions(context.Context) ([]SessionInfo, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.sessions, nil
}

func (r *switcherRunner) SwitchSession(_ context.Context, id string) error {
	if r.err != nil {
		return r.err
	}
	r.current = id
	return nil
}

func (r *switcherRunner) CurrentSession() string { return r.current }

// messagesSnapshot is the conversation as the view holds it.
//
// It is a test helper and not an exported method: production code has no reason to read the
// view's messages, and adding the method for a test's convenience would be production code
// written for the test.
func (t *TUI) messagesSnapshot() []Message {
	t.draw.Lock()
	defer t.draw.Unlock()
	return append([]Message(nil), t.messages...)
}

// TestAttachingReadsTheConversationItIsReturningTo: coming back to a session means seeing what
// happened while you were away. A cleared view with the new status bar is the one outcome that
// makes "attach" worse than useless - the user is told they are in a conversation and shown none
// of it.
//
// The content is read from the GATEWAY and not from anything the interface kept, which is the same
// rule as everywhere else: the interface's claim is that everything it shows comes from the
// gateway.
//
// It goes through the package's own newFakeTUI rather than tui.New: New writes frames to
// os.Stdout, and a test that draws on the real terminal is a test whose output nobody can read.
func TestAttachingReadsTheConversationItIsReturningTo(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}},
		current:  "default",
		transcript: map[string][]Turn{
			"sabc": {
				{User: "count the files"},
				{Agent: "there are twelve"},
			},
		},
	}
	ui := switcher(r)

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}

	got := ui.messagesSnapshot()
	if len(got) != 3 {
		// Two turns read back, and the line that says which conversation was entered.
		t.Fatalf("the attached view holds %d messages, the two turns and the announcement must be drawn: %+v", len(got), got)
	}
	if got[0].Author != AuthorUser || got[0].Text != "count the files" {
		t.Errorf("the first line is %+v, it must be what the user said", got[0])
	}
	if got[1].Author != AuthorAgent || got[1].Text != "there are twelve" {
		t.Errorf("the second line is %+v, it must be what the agent answered", got[1])
	}
	if !strings.Contains(got[2].Text, "sabc") {
		t.Errorf("the attach must say which conversation was entered, got %+v", got[2])
	}
}

// TestAttachingToAnEmptySessionSaysSo: a conversation with nothing in it must not look like a
// failed attach. An empty screen is ambiguous - it could be a conversation with no turns or a view
// that never loaded - and one line removes the ambiguity.
func TestAttachingToAnEmptySessionSaysSo(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "fresh"}}, current: "default"}
	ui := switcher(r)

	if err := ui.attachTo(context.Background(), "fresh"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	got := ui.messagesSnapshot()
	if len(got) == 0 || !strings.Contains(got[len(got)-1].Text, "fresh") {
		t.Fatalf("attaching to an empty conversation must say which one it is; view holds %+v", got)
	}
}

// TestAttachingReplacesTheConversationItLeft: the lines on screen belong to the conversation being
// LEFT. Keeping them beside the new one's status bar is a view that lies about which conversation
// it is showing, and it is the same failure as the blank screen seen from the other side.
func TestAttachingReplacesTheConversationItLeft(t *testing.T) {
	r := &switcherRunner{
		sessions:   []SessionInfo{{ID: "default"}, {ID: "sabc"}},
		current:    "default",
		transcript: map[string][]Turn{"sabc": {{Agent: "the answer from sabc"}}},
	}
	ui := switcher(r)
	ui.addMessage(AuthorUser, "something said in the old conversation")

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	for _, m := range ui.messagesSnapshot() {
		if strings.Contains(m.Text, "old conversation") {
			t.Fatalf("the previous conversation is still on screen: %+v", ui.messagesSnapshot())
		}
	}
	// And the frame is rebuilt: the diffing painter compares against the rows it last wrote, so
	// a replaced conversation with a stale frame would leave the previous conversation's lines
	// on the terminal - which is exactly the lie this whole method avoids.
	if strings.Contains(strings.Join(ui.lastFrame, "\n"), "old conversation") {
		t.Fatalf("the last frame still holds the previous conversation:\n%s", strings.Join(ui.lastFrame, "\n"))
	}
}

// TestAttachingReportsAConversationThatCouldNotBeRead: the switch happened and the read did not,
// so the user is IN a conversation whose content could not be fetched. That is a failure worth
// reporting: silently showing the empty view would look exactly like a conversation with no turns.
func TestAttachingReportsAConversationThatCouldNotBeRead(t *testing.T) {
	r := &switcherRunner{
		sessions:        []SessionInfo{{ID: "default"}, {ID: "sabc"}},
		current:         "default",
		conversationErr: errors.New("connection refused"),
	}
	ui := switcher(r)

	err := ui.attachTo(context.Background(), "sabc")
	if err == nil {
		t.Fatal("a conversation that could not be read must be reported")
	}
	if !strings.Contains(err.Error(), "sabc") {
		t.Errorf("the failure must name the session it is about, got: %v", err)
	}
	if r.current != "sabc" {
		t.Errorf("the switch did happen and must not be undone, the runner is on %q", r.current)
	}
}

// TestSessionSwitcherIsOptional: it is an OPTIONAL capability, resolved by a type assertion, the
// same pattern askSource and taskObserver already use. Requiring it on Runner would mean every
// existing double and every future front end grows two methods it may have no sessions to offer -
// and the many small runners the tests build would all become wrong for no reason.
func TestSessionSwitcherIsOptional(t *testing.T) {
	var r Runner = &fakeRunner{}
	if _, ok := r.(SessionSwitcher); ok {
		t.Fatal("a runner that offers no sessions must not be reported as offering them")
	}
}

// TestSessionsAreListedWithTheirState: the user has to see which conversations exist and which one
// is busy, because attaching to a conversation mid-run is a different experience from attaching to
// an idle one.
func TestSessionsAreListedWithTheirState(t *testing.T) {
	var r Runner = &switcherRunner{sessions: []SessionInfo{
		{ID: "default", Running: true},
		{ID: "sabc", Running: false},
	}}
	sw, ok := r.(SessionSwitcher)
	if !ok {
		t.Fatal("a runner that offers sessions must be reported as offering them")
	}
	all, err := sw.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("listed %d sessions, both must be there", len(all))
	}
	if !all[0].Running {
		t.Error("a session with a run in flight must say so: attaching to it is a different thing")
	}
}

// TestAttachingChangesTheSessionAndSaysSo: switching has to be visible. A silent switch would leave
// the user answering a question in a conversation they did not mean to be in.
func TestAttachingChangesTheSessionAndSaysSo(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}}, current: "default"}
	ui := switcher(r)
	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	if got := r.current; got != "sabc" {
		t.Errorf("the runner is on session %q, the attach must have happened", got)
	}
}

// TestAttachingToASessionWithALiveRunFollowsIt: the turn outlives the client, so coming back to a
// session mid-run is the case the design created - not an edge case. The interface has to FOLLOW
// the run it did not start: show that work is happening, and show the lines as they arrive.
//
// Without this the user attaches to a conversation that is visibly doing something, sees a static
// screen, and has no way to tell a working turn from a hung one.
func TestAttachingToASessionWithALiveRunFollowsIt(t *testing.T) {
	release := make(chan struct{})
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "sabc", Running: true}},
		current:  "default",
		liveRun:  &liveRun{progress: []string{"reading the tree", "running the check"}, release: release},
	}
	ui := switcher(r)

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}

	// The work must be VISIBLE while it happens: the status line shows a spinner, which is what
	// tells the user a turn is in flight rather than a screen that forgot to paint.
	waitForBusy(t, r, ui)
	// The run is released by the TEST and not by a cancel: this is the ordinary ending of a turn
	// the user was watching, and it is what the assertions below measure.
	release <- struct{}{}
	waitForIdle(t, r, ui)

	// The lines the live run emitted are in the view, and the turn ended visibly.
	if !ui.viewContains("running the check") {
		t.Errorf("the attached view does not hold what the live run said: %+v", ui.messagesSnapshot())
	}
	if ui.isBusy() {
		t.Error("the interface is still busy after the live run finished")
	}
}

// TestAttachingToALiveRunDoesNotBlockTheKeyLoop: /attach is typed, so attachTo runs INSIDE the
// loop that reads the keys. Following a run there would freeze the interface for the whole turn:
// the user could not read, scroll or stop, and the only way out would be to kill the process.
//
// The test drives attachTo from a goroutine and requires that it RETURN while the run is still in
// flight. That is the property, and it is the one the plan flags twice as the real risk.
func TestAttachingToALiveRunDoesNotBlockTheKeyLoop(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "sabc", Running: true}},
		current:  "default",
		liveRun:  &liveRun{progress: []string{"working"}, release: make(chan struct{})},
	}
	ui := switcher(r)

	done := make(chan error, 1)
	go func() { done <- ui.attachTo(context.Background(), "sabc") }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("attachTo: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attachTo blocked while following a run: the key loop would be frozen for the whole turn")
	}
	waitForBusy(t, r, ui)
}

// TestEscapeCancelsTheLiveRunOfTheSessionItIsIn: the interface cancels a run through t.cancelRun,
// which only the run it STARTED sets - so for a run that was already in flight, Escape falls
// through to scrolling and the turn keeps going while the user believes they stopped it.
// Cancelling has to go to the GATEWAY, which is where the run lives.
func TestEscapeCancelsTheLiveRunOfTheSessionItIsIn(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "sabc", Running: true}},
		current:  "default",
		liveRun:  &liveRun{progress: []string{"working"}, release: make(chan struct{})},
	}
	ui := switcher(r)
	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	waitForCancelArmed(t, ui)

	// The key handling the terminal actually reaches, and not a direct call to a cancel: the point
	// is that the keyboard works on a run this interface did not start.
	handled, quit := ui.handleShortcut(context.Background(), keyEsc)
	if !handled || quit {
		t.Fatalf("Escape must be handled without quitting (handled=%v quit=%v)", handled, quit)
	}

	// The request leaves on the following goroutine - it is a round trip to the gateway - so the
	// test waits for it instead of sampling a counter that may not have been incremented yet. Then
	// it is EXACTLY one: two requests would mean the key cancelled something twice.
	waitUntil(t, func() bool { return r.cancelCalls() > 0 },
		"Escape produced no cancel request; it must ask the gateway to stop the run it is following")
	if n := r.cancelCalls(); n != 1 {
		t.Fatalf("Escape produced %d cancel requests, want exactly one", n)
	}
	waitForIdle(t, r, ui)
}

// TestAttachingToASessionWithNoRunDoesNotBecomeBusy: the common case - a conversation that is
// simply sitting there - must not look like work is happening. A spinner with nothing behind it is
// the interface inventing activity.
func TestAttachingToASessionWithNoRunDoesNotBecomeBusy(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "sabc"}}, current: "default"}
	ui := switcher(r)
	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	if ui.isBusy() {
		t.Error("attaching to an idle conversation left the interface busy")
	}
}

// TestAttachingReportsAGatewayThatCannotSayWhetherARunIsInFlight: the conversation was read and
// the user is IN the session, so failing the whole attach over the one remaining question would
// send them back to the session they just left. It is reported in the conversation instead.
func TestAttachingReportsAGatewayThatCannotSayWhetherARunIsInFlight(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "sabc"}},
		current:  "default",
		liveErr:  errors.New("connection refused"),
	}
	ui := switcher(r)

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("a failed question about a run must not fail the attach: %v", err)
	}
	if r.current != "sabc" {
		t.Errorf("the switch was undone by the failure, the runner is on %q", r.current)
	}
	if !ui.viewContains("in flight") {
		t.Errorf("the failure is not reported in the conversation: %+v", ui.messagesSnapshot())
	}
}

// TestAFollowedRunThatEndsShowsItsResult: the run's own result is what the block settles on, the
// same as a turn this interface started. A followed run that ended silently would leave the user
// unable to tell it finished from it being about to.
func TestAFollowedRunThatEndsShowsItsResult(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "sabc", Running: true}},
		current:  "default",
		liveRun:  &liveRun{progress: []string{"working"}, result: "twelve files", release: make(chan struct{}, 1)},
	}
	ui := switcher(r)

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	waitForBusy(t, r, ui)
	r.liveRun.release <- struct{}{}
	waitForIdle(t, r, ui)
	if !ui.viewContains("twelve files") {
		t.Errorf("the followed run's result is not in the view: %+v", ui.messagesSnapshot())
	}
}

// TestAFollowedRunThatEndsWithNoResultSaysSo: a run that reports nothing must say so rather than
// leave an empty block, which is indistinguishable from a block still waiting.
func TestAFollowedRunThatEndsWithNoResultSaysSo(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "sabc", Running: true}},
		current:  "default",
		liveRun:  &liveRun{progress: []string{"working"}, release: make(chan struct{}, 1)},
	}
	ui := switcher(r)

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	waitForBusy(t, r, ui)
	r.liveRun.release <- struct{}{}
	waitForIdle(t, r, ui)
	if !ui.viewContains("the turn finished") {
		t.Errorf("a followed run with no result must say it finished: %+v", ui.messagesSnapshot())
	}
}

// TestAFollowedRunThatCouldNotBeFollowedIsReported: a session whose run ended between the question
// and the attach, or a connection that could not hold. The user must be told, not shown a turn
// that is not there.
func TestAFollowedRunThatCouldNotBeFollowedIsReported(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "sabc", Running: true}},
		current:  "default",
		liveRun:  &liveRun{progress: []string{"working"}, err: errors.New("the stream ended"), release: make(chan struct{}, 1)},
	}
	ui := switcher(r)

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	waitForBusy(t, r, ui)
	r.liveRun.release <- struct{}{}
	waitForIdle(t, r, ui)
	if !ui.viewContains("could not be followed") {
		t.Errorf("a follow that failed must be reported: %+v", ui.messagesSnapshot())
	}
}

// TestEscapeOnAFollowedRunEndsItVisibly: the turn stops AND the block stops being pending. A
// cancelled block left spinning would say the work is still going.
func TestEscapeOnAFollowedRunEndsItVisibly(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "sabc", Running: true}},
		current:  "default",
		liveRun:  &liveRun{progress: []string{"working"}, release: make(chan struct{}, 1)},
	}
	ui := switcher(r)
	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	waitForBusy(t, r, ui)

	// Escape ends the follow through the same field a local run uses, so the run is released by the
	// cancelled context and nothing has to be sent here.
	ui.handleShortcut(context.Background(), keyEsc)
	waitForIdle(t, r, ui)

	if !ui.viewContains("cancelled") {
		t.Errorf("a cancelled follow must say so: %+v", ui.messagesSnapshot())
	}
}

// TestTheRunControllerIsOptional: whether a runner can look at and stop a run belonging to the
// GATEWAY is a property of the runner, exactly like the session listing. A runner backed by a
// single local session has no gateway to ask, and requiring the methods would make every existing
// double wrong for no reason.
func TestTheRunControllerIsOptional(t *testing.T) {
	var plain Runner = &fakeRunner{}
	if _, ok := plain.(RunController); ok {
		t.Fatal("a runner with no gateway behind it must not be reported as able to control a run")
	}
	var with Runner = &switcherRunner{}
	if _, ok := with.(RunController); !ok {
		t.Fatal("a runner that can answer about a run in flight must be reported as able to control one")
	}
}

// TestAttachingWithoutTheRunCapabilityStillReads: the capability is OPTIONAL, so a runner offering
// sessions but not runs must attach, read and draw exactly as before. Demanding the capability
// would break every front end backed by something other than the gateway.
func TestAttachingWithoutTheRunCapabilityStillReads(t *testing.T) {
	r := &listingOnlyRunner{
		fakeRunner: &fakeRunner{},
		sessions:   []SessionInfo{{ID: "sabc"}},
		transcript: map[string][]Turn{"sabc": {{User: "hello"}}},
		current:    "default",
	}
	ui := switcher(&switcherRunner{fakeRunner: &fakeRunner{}})
	ui.Runner = r

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	if ui.isBusy() {
		t.Error("a runner with no run to follow must leave the interface idle")
	}
	if !ui.viewContains("hello") {
		t.Errorf("the conversation was not drawn: %+v", ui.messagesSnapshot())
	}
}

// TestTheSessionListingIsUnavailableWithoutTheCapability (above) covers the listing; this covers
// the same runner shape for the run question.
func TestASessionListAndConversationWithoutRunsIsEnough(t *testing.T) {
	var r Runner = &listingOnlyRunner{}
	if _, ok := r.(RunController); ok {
		t.Fatal("this double is meant to lack the run capability")
	}
	if _, ok := r.(SessionSwitcher); !ok {
		t.Fatal("this double is meant to have the session capability")
	}
}

// TestAttachingToAnUnknownSessionIsReported: the gateway knows the real list, the interface does
// not, and a session that vanished between the listing and the attach must be reported rather than
// leaving the interface pointing at nothing.
func TestAttachingToAnUnknownSessionIsReported(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "default"}},
		err:      errors.New(`there is no session "gone"`),
	}
	ui := switcher(r)
	if err := ui.attachTo(context.Background(), "gone"); err == nil {
		t.Fatal("attaching to a session the gateway does not hold must fail")
	}
}

// TestAttachingWithoutTheCapabilityIsReported: an ordinary runner - the embedded interface's - has
// no conversations to move between, and saying so is better than a switch that silently does
// nothing.
func TestAttachingWithoutTheCapabilityIsReported(t *testing.T) {
	ui := newFakeTUI("", &fakeRunner{})
	if err := ui.attachTo(context.Background(), "sabc"); err == nil {
		t.Fatal("attaching through a runner with no sessions must be reported")
	}
}

// TestACommandExistsToSeeTheSessions: the capability is useless if there is no way to reach it, and
// the help table is what tells a user it exists.
func TestACommandExistsToSeeTheSessions(t *testing.T) {
	for _, want := range []string{"/sessions", "/attach"} {
		found := false
		for _, c := range commands {
			if c.Name == want {
				found = true
				if c.Help == "" {
					t.Errorf("%s must describe itself: it is how a user discovers the feature", want)
				}
			}
		}
		if !found {
			t.Errorf("%s is not in the command table, so nothing tells the user the feature exists", want)
		}
	}
}

// TestTheListingMarksTheCurrentSession: the user has to be able to see WHICH conversation they are
// on, or the list is a list of ids with no way to tell where they are.
func TestTheListingMarksTheCurrentSession(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}}, current: "sabc"}
	ui := switcher(r)

	ui.printSessions(context.Background())

	drawn := stripANSI(outputOf(ui))
	if !strings.Contains(drawn, "* sabc") {
		t.Errorf("the current session is not marked in:\n%s", drawn)
	}
	if !strings.Contains(drawn, "  default") {
		t.Errorf("the other session is not listed in:\n%s", drawn)
	}
}

// TestTheListingSaysWhenARunIsInFlight: attaching to a busy conversation is a different thing from
// attaching to a quiet one, and it is the reason the field exists.
func TestTheListingSaysWhenARunIsInFlight(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default", Running: true}}}
	ui := switcher(r)
	ui.printSessions(context.Background())

	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "in flight") {
		t.Errorf("a run in flight is not reported in:\n%s", drawn)
	}
}

// TestTheListingReportsAFailedAsk: the gateway is the authority on which conversations exist, so a
// listing that could not be fetched must say so rather than showing nothing.
func TestTheListingReportsAFailedAsk(t *testing.T) {
	r := &switcherRunner{err: errors.New("connection refused")}
	ui := switcher(r)
	ui.printSessions(context.Background())

	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "could not be asked") {
		t.Errorf("a failed listing is not reported in:\n%s", drawn)
	}
}

// TestTheListingIsUnavailableWithoutTheCapability: the same courtesy the other messages give.
func TestTheListingIsUnavailableWithoutTheCapability(t *testing.T) {
	ui := newFakeTUI("", &fakeRunner{})
	ui.printSessions(context.Background())

	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "several sessions") {
		t.Errorf("the lack of the capability is not reported in:\n%s", drawn)
	}
}

// TestTheListingSaysWhenThereIsNothing: a gateway that reports no conversations at all is a state
// that should not be reachable, and saying so beats an empty list that looks like a rendering bug.
func TestTheListingSaysWhenThereIsNothing(t *testing.T) {
	r := &switcherRunner{}
	ui := switcher(r)
	ui.printSessions(context.Background())

	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "which should not be possible") {
		t.Errorf("an empty listing is not reported in:\n%s", drawn)
	}
}

// TestTheAttachCommandThroughTheTable: the other tests call attachTo directly, which leaves the
// COMMAND - the part a user actually reaches - unexecuted. The table is the dispatch path, so this
// exercises the closure that runs when someone types /attach.
func TestTheAttachCommandThroughTheTable(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}}, current: "default"}
	ui := switcher(r)
	ui.addMessage(AuthorUser, "before")

	if quit := commandActions["/attach"](ui, context.Background(), "sabc"); quit {
		t.Error("/attach must not quit the interface")
	}
	if r.current != "sabc" {
		t.Fatalf("the command did not move the session, it is on %q", r.current)
	}
	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "attached to session sabc") {
		t.Errorf("the move was not announced:\n%s", drawn)
	}
}

// TestTheAttachCommandReportsAFailure: the message path is what tells the user the switch did not
// happen. Without it they keep typing into a conversation they did not choose.
func TestTheAttachCommandReportsAFailure(t *testing.T) {
	r := &switcherRunner{err: errors.New("connection refused")}
	ui := switcher(r)

	if quit := commandActions["/attach"](ui, context.Background(), "sabc"); quit {
		t.Error("/attach must not quit the interface")
	}
	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "connection refused") {
		t.Errorf("the failure was not reported:\n%s", drawn)
	}
}

// TestTheAttachCommandWithoutAnArgumentExplainsItself: "/attach" alone is a typo, and the answer
// has to be the usage line rather than a switch to a conversation named "".
func TestTheAttachCommandWithoutAnArgumentExplainsItself(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}}, current: "default"}
	ui := switcher(r)

	if quit := commandActions["/attach"](ui, context.Background(), "   "); quit {
		t.Error("/attach must not quit the interface")
	}
	if r.current != "default" {
		t.Errorf("a bare /attach moved the session to %q", r.current)
	}
	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "usage: /attach") {
		t.Errorf("the usage line was not shown:\n%s", drawn)
	}
}

// TestTheSessionsCommandThroughTheTable: same reason as /attach - the table is what the user
// reaches, and the closure has to run.
func TestTheSessionsCommandThroughTheTable(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}}, current: "default"}
	ui := switcher(r)

	if quit := commandActions["/sessions"](ui, context.Background(), ""); quit {
		t.Error("/sessions must not quit the interface")
	}
	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "sessions:") {
		t.Errorf("the listing did not run:\n%s", drawn)
	}
}

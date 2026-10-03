package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/session"
)

// liveRunner is a runner whose turns stay open until the test lets them end, and which never holds
// a lock while they do: the interface draws while a turn runs, and a double that blocked its own
// Config() for the length of a turn would deadlock the very behaviour under test.
type liveRunner struct {
	*fakeRunner
	// started receives every task or question as its turn begins.
	started chan string
	// release ends the turn in flight with the outcome sent.
	release chan runOutcome
	// stopped receives once for every turn that ended because it was cancelled.
	stopped chan struct{}
	// progress is reported by every turn before it waits.
	progress []string
}

func newLiveRunner() *liveRunner {
	return &liveRunner{
		fakeRunner: &fakeRunner{cfg: configWithKey("k"), cfgSet: true},
		started:    make(chan string, 4),
		release:    make(chan runOutcome),
		stopped:    make(chan struct{}, 4),
	}
}

func (r *liveRunner) turn(ctx context.Context, text string, progress func(string, ...any)) (string, error) {
	r.started <- text
	for _, p := range r.progress {
		progress("%s", p)
	}
	select {
	case out := <-r.release:
		return out.result, out.err
	case <-ctx.Done():
		r.stopped <- struct{}{}
		return "", ctx.Err()
	}
}

func (r *liveRunner) RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
	return r.turn(ctx, task, progress)
}

func (r *liveRunner) RunPlan(ctx context.Context, prompt string, progress func(string, ...any)) (string, error) {
	return r.turn(ctx, prompt, progress)
}

func (r *liveRunner) Config() config.Config                 { return configWithKey("k") }
func (r *liveRunner) ConversationSummary() session.Snapshot { return session.Snapshot{} }

// newLiveTUI is an interface in character mode reading from a pipe the test writes keys into.
func newLiveTUI(r Runner) (*TUI, *io.PipeWriter) {
	pr, pw := io.Pipe()
	tu := New(r)
	tu.In = pr
	tu.Out = &bytes.Buffer{}
	tu.Err = &bytes.Buffer{}
	tu.Width, tu.Height = 100, 30
	tu.NoColor = true
	tu.LiveInput = true
	return tu, pw
}

// runLive starts the interface and returns the channel its exit code arrives on.
func runLive(ctx context.Context, tu *TUI) <-chan int {
	done := make(chan int, 1)
	go func() { done <- tu.Run(ctx) }()
	return done
}

func waitOn[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

func messageTexts(tu *TUI) string {
	var b strings.Builder
	for _, m := range tu.messages {
		b.WriteString(m.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// The defect this file exists for: a turn used to be awaited in place, so no key was read while it
// ran and Escape could not stop it. Measured on a real terminal: Escape two seconds into a task did
// nothing, and the task ran its three attempts to the end.
func TestEscapeStopsATaskWhileItRuns(t *testing.T) {
	r := newLiveRunner()
	r.progress = []string{"analysing the task..."}
	tu, keys := newLiveTUI(r)
	done := runLive(context.Background(), tu)

	keys.Write([]byte("do something\n"))
	if got := waitOn(t, r.started, "the task to start"); got != "do something" {
		t.Fatalf("started %q", got)
	}
	keys.Write([]byte{0x1b})
	waitOn(t, r.stopped, "the task to be stopped")
	keys.Close()

	if code := waitOn(t, done, "the interface to exit"); code != ExitSuccess {
		t.Errorf("exit code %d", code)
	}
	if !strings.Contains(messageTexts(tu), "cancelled.") {
		t.Errorf("the stopped turn must say so:\n%s", messageTexts(tu))
	}
}

// /quit while a turn runs stops it and waits for it, so nothing is left running behind the shell.
func TestQuitWhileATaskRunsStopsIt(t *testing.T) {
	r := newLiveRunner()
	tu, keys := newLiveTUI(r)
	done := runLive(context.Background(), tu)

	keys.Write([]byte("do something\n"))
	waitOn(t, r.started, "the task to start")
	go keys.Write([]byte("/quit\n"))
	waitOn(t, r.stopped, "the task to be stopped")
	if code := waitOn(t, done, "the interface to exit"); code != ExitSuccess {
		t.Errorf("exit code %d", code)
	}
}

// A closed input is a script that piped one task: the turn is let finish, and its answer shown.
func TestTheEndOfTheInputLetsTheTurnFinish(t *testing.T) {
	r := newLiveRunner()
	tu, keys := newLiveTUI(r)
	done := runLive(context.Background(), tu)

	keys.Write([]byte("plan it\n"))
	waitOn(t, r.started, "the task to start")
	keys.Close()
	r.release <- runOutcome{result: "all done"}
	waitOn(t, done, "the interface to exit")
	if !strings.Contains(messageTexts(tu), "all done") {
		t.Errorf("the answer must be shown:\n%s", messageTexts(tu))
	}
}

// Ctrl+C cancels the interface's context: the turn stops with it and the exit says so.
func TestACancelledContextStopsTheTurn(t *testing.T) {
	r := newLiveRunner()
	tu, keys := newLiveTUI(r)
	ctx, cancel := context.WithCancel(context.Background())
	done := runLive(ctx, tu)

	keys.Write([]byte("do something\n"))
	waitOn(t, r.started, "the task to start")
	cancel()
	if code := waitOn(t, done, "the interface to exit"); code != ExitInterrupted {
		t.Errorf("exit code %d, want interrupted", code)
	}
	keys.Close()
}

// driveLoop runs the input loop's reader on one goroutine until it returns a byte or gives up,
// which is where a turn in flight is driven. The test only touches the interface after it ends.
func driveLoop(ctx context.Context, tu *TUI) <-chan byte {
	out := make(chan byte, 1)
	go func() {
		b, _ := tu.readByteOrCancel(ctx)
		out <- b
	}()
	return out
}

// settle drives the loop until no turn is in flight. A turn's outcome and a keystroke can be
// ready at the same moment, and the loop may take either first; driving it again until the turn
// is gone is what makes the result independent of that choice. The interface is only read
// between drives, when nothing else is touching it.
func settle(t *testing.T, tu *TUI, keys io.Writer) {
	t.Helper()
	// Bounded by TIME, not by a count: the turn ends when its goroutine gets to run, and on a
	// loaded machine a thousand quick drives can all happen before it does.
	deadline := time.Now().Add(5 * time.Second)
	for tu.current != nil && time.Now().Before(deadline) {
		got := driveLoop(context.Background(), tu)
		time.Sleep(time.Millisecond) // let a ready outcome reach the loop before the key does
		keys.Write([]byte("x"))
		waitOn(t, got, "the loop to return")
	}
	if tu.current != nil {
		t.Fatal("the turn never ended")
	}
}

// A message typed while a turn runs waits for it, and is sent the moment it ends.
func TestAMessageTypedWhileATurnRunsIsSentWhenItEnds(t *testing.T) {
	r := newLiveRunner()
	tu, keys := newLiveTUI(r)
	tu.charMode = true
	ctx := context.Background()

	tu.runTask(ctx, "first")
	waitOn(t, r.started, "the first task")
	tu.queue("second")
	tu.queue("third") // replaces the second, and says so

	got := driveLoop(ctx, tu)
	r.release <- runOutcome{result: "one"}
	if task := waitOn(t, r.started, "the queued task"); task != "third" {
		t.Fatalf("started %q, want the queued message", task)
	}
	go func() { r.release <- runOutcome{result: "two"} }()
	keys.Write([]byte("x"))
	if b := waitOn(t, got, "the loop to return"); b != 'x' {
		t.Errorf("the key typed after must still arrive, got %q", b)
	}
	settle(t, tu, keys)
	text := messageTexts(tu)
	for _, want := range []string{"Queued: it will be sent", "replacing the message queued before", "one", "two"} {
		if !strings.Contains(text, want) {
			t.Errorf("the conversation lacks %q:\n%s", want, text)
		}
	}
	if tu.current != nil || tu.queued != "" {
		t.Errorf("nothing may be left in flight: current=%v queued=%q", tu.current, tu.queued)
	}
}

// Escape means stop, not "skip to the next": the queued message goes back into the input.
func TestAStoppedTurnDoesNotSendTheQueuedMessage(t *testing.T) {
	r := newLiveRunner()
	tu, keys := newLiveTUI(r)
	tu.charMode = true
	ctx := context.Background()

	tu.runTask(ctx, "first")
	waitOn(t, r.started, "the first task")
	tu.queue("second")
	tu.currentCancel()()
	waitOn(t, r.stopped, "the task to stop")
	settle(t, tu, keys)

	if tu.draft != "second" {
		t.Errorf("the queued message must be back in the input, got %q", tu.draft)
	}
	if !strings.Contains(messageTexts(tu), "Stopped. Your queued message was not sent") {
		t.Errorf("the conversation must say so:\n%s", messageTexts(tu))
	}
	select {
	case task := <-r.started:
		t.Errorf("no new turn may start, got %q", task)
	default:
	}
}

// The input already holds something: the queued message is not put on top of it.
func TestAStoppedTurnLeavesAFreshDraftAlone(t *testing.T) {
	r := newLiveRunner()
	tu, keys := newLiveTUI(r)
	tu.charMode = true
	ctx := context.Background()

	tu.runPlan(ctx, "first")
	waitOn(t, r.started, "the first question")
	tu.queue("second")
	tu.draft = "typing"
	tu.currentCancel()()
	waitOn(t, r.stopped, "the turn to stop")
	settle(t, tu, keys)
	if tu.draft != "typing" {
		t.Errorf("the draft must be left alone, got %q", tu.draft)
	}
}

// Progress, a request for approval, the spinner and the outcome are all applied by the loop that
// reads the keys, between keystrokes.
func TestTheLoopDrivesTheTurnBetweenKeystrokes(t *testing.T) {
	oldTick := spinInterval
	spinInterval = time.Millisecond
	t.Cleanup(func() { spinInterval = oldTick })

	r := newLiveRunner()
	r.progress = []string{"step one"}
	tu, keys := newLiveTUI(r)
	tu.charMode = true
	ctx := context.Background()

	tu.runTask(ctx, "work")
	waitOn(t, r.started, "the task")
	spun := tu.spin
	got := driveLoop(ctx, tu)

	// The agent asks before running a command: the window opens while the turn runs.
	reply := make(chan bool, 1)
	tu.approvalChannel() <- &confirmState{req: agent.ApprovalRequest{Command: "rm -rf build"}, reply: reply}
	time.Sleep(20 * time.Millisecond) // a few spinner ticks
	keys.Write([]byte("x"))
	waitOn(t, got, "the loop to return")
	if tu.confirm == nil {
		t.Fatal("the confirmation window must be open while the turn runs")
	}
	if tu.spin <= spun {
		t.Errorf("the spinner must move while nothing is reported, spin=%d (was %d)", tu.spin, spun)
	}

	// The answer is a key read by the same loop.
	tu.handleConfirmLine("y")
	if approved := waitOn(t, reply, "the answer"); !approved {
		t.Error("y must approve")
	}

	// A window still open when the turn ends is closed with a refusal.
	reply2 := make(chan bool, 1)
	tu.openConfirm(&confirmState{req: agent.ApprovalRequest{Command: "make deploy"}, reply: reply2})
	go func() { r.release <- runOutcome{result: "finished"} }()
	settle(t, tu, keys)
	if approved := waitOn(t, reply2, "the refusal"); approved {
		t.Error("a window left open by a finished turn must refuse")
	}
	// The progress line was overwritten by the result, which is the Task-mode contract: a phase
	// is a transient label, and only the result remains.
	if !strings.Contains(messageTexts(tu), "finished") {
		t.Errorf("the outcome must be applied:\n%s", messageTexts(tu))
	}
}

// The way out refuses a confirmation nobody is left to answer, and applies what the turn reports.
func TestFinishTurnAppliesTheRestAndRefusesTheQuestion(t *testing.T) {
	tu, _ := newKeyTUI("")
	progress := make(chan string)
	done := make(chan runOutcome)
	var seen []string
	var outcome runOutcome
	tu.current = &turn{
		progress:   progress,
		done:       done,
		onProgress: func(p string) { seen = append(seen, p) },
		onDone:     func(o runOutcome) { outcome = o },
		tick:       time.NewTicker(time.Hour),
	}
	tu.queued = "never sent"
	reply := make(chan bool, 1)
	go func() {
		progress <- "late line"
		tu.approvalChannel() <- &confirmState{req: agent.ApprovalRequest{Command: "x"}, reply: reply}
		done <- runOutcome{result: "end"}
	}()
	tu.finishTurn(context.Background())
	if len(seen) != 1 || outcome.result != "end" {
		t.Errorf("seen=%v outcome=%+v", seen, outcome)
	}
	if approved := <-reply; approved {
		t.Error("nobody is left to approve: the answer is no")
	}
	if tu.queued != "" {
		t.Error("nothing queued may start on the way out")
	}
}

func TestLeaveTurnWithNothingRunning(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.leaveTurn(context.Background()) // must return at once
}

// Leaving is not stopping: behind a gateway the run outlives the client that started it, and only
// Escape asks the gateway to stop it.
func TestQuittingDoesNotStopTheRunAtTheGateway(t *testing.T) {
	sw := &switcherRunner{fakeRunner: &fakeRunner{}, liveRun: &liveRun{release: make(chan struct{})}}
	r := newLiveRunner()
	tu, keys := newLiveTUI(r)
	// The live runner turns, with the switcher's gateway controls.
	tu.Runner = struct {
		*liveRunner
		RunController
	}{r, sw}
	done := runLive(context.Background(), tu)
	keys.Write([]byte("do something\n"))
	waitOn(t, r.started, "the task to start")
	go keys.Write([]byte("/quit\n"))
	waitOn(t, r.stopped, "the local wait to end")
	waitOn(t, done, "the interface to exit")
	if n := sw.liveRun.cancelRequests(); n != 0 {
		t.Errorf("leaving must not ask the gateway to stop the run, it was asked %d times", n)
	}
}

// The commands that replace the conversation, hand over the terminal or rate an unfinished turn
// wait for the turn; the rest work while it runs.
func TestSomeCommandsWaitForTheTurn(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.current = &turn{}
	for _, cmd := range []string{"/new", "/config", "/models", "/attach x", "/good", "/bad"} {
		tu.messages = nil
		handled, _ := tu.handleTypedCommand(context.Background(), cmd)
		if !handled || !strings.Contains(messageTexts(tu), "has to wait") {
			t.Errorf("%s must wait for the turn:\n%s", cmd, messageTexts(tu))
		}
	}
	tu.messages = nil
	if handled, _ := tu.handleTypedCommand(context.Background(), "/help"); !handled || strings.Contains(messageTexts(tu), "has to wait") {
		t.Error("/help must work while a turn runs")
	}
}

// Behind a gateway, Escape asks the gateway to stop the run too: it keeps a run going when its
// client goes away, which is what lets a phone follow it.
func TestStoppingATurnBehindAGatewayStopsItThere(t *testing.T) {
	sw := &switcherRunner{fakeRunner: &fakeRunner{}, liveRun: &liveRun{release: make(chan struct{})}}
	tu, _ := newKeyTUI("")
	tu.Runner = sw
	cancelled := false
	stop := tu.stopper(func() { cancelled = true })
	stop()
	if !cancelled {
		t.Error("the local wait must stop")
	}
	deadline := time.Now().Add(5 * time.Second)
	for sw.liveRun.cancelRequests() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if sw.liveRun.cancelRequests() == 0 {
		t.Error("the gateway must be asked to stop the run")
	}
}

// The questions window is answered from the loop's own context when no run is in flight.
func TestLoopCtxOr(t *testing.T) {
	tu, _ := newKeyTUI("")
	fallback := context.Background()
	if tu.loopCtxOr(fallback) != fallback {
		t.Error("with no loop the fallback is used")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tu.loopCtx = ctx
	if tu.loopCtxOr(fallback) != ctx {
		t.Error("the loop's context is used when there is one")
	}
}

// A message typed on the Models screen is a task, and the screen moves to Task for it.
func TestSubmitFromTheModelsScreenRunsATask(t *testing.T) {
	runner := &fakeRunner{}
	tu := newFakeTUI("", runner)
	tu.screen = ScreenModels
	tu.submit(context.Background(), "fix it")
	if tu.screen != ScreenTask || runner.lastTask != "fix it" {
		t.Errorf("screen=%s task=%q", tu.screen, runner.lastTask)
	}
}

// errReader fails every read, the way a terminal that went away does.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("the terminal went away") }

func TestReadByteOrCancelReportsAFailedRead(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.In = errReader{}
	if _, ok := tu.readByteOrCancel(context.Background()); ok {
		t.Error("a failed read is the end of the input")
	}
}

// TestEndingATurnAppliesTheLinesItLeftQueued: the outcome and the last lines arrive together, and
// the input loop's select may take the outcome first. The lines are the turn's, and are applied
// before it is settled.
func TestEndingATurnAppliesTheLinesItLeftQueued(t *testing.T) {
	tu, _ := newKeyTUI("")
	progress := make(chan string, 2)
	progress <- "late line"
	progress <- "final snapshot"
	var seen []string
	settled := false
	tu.current = &turn{
		progress:   progress,
		onProgress: func(p string) { seen = append(seen, p) },
		onDone:     func(runOutcome) { settled = len(seen) == 2 },
		tick:       time.NewTicker(time.Hour),
	}
	tu.endCurrent(context.Background(), runOutcome{result: "end"})
	if !settled {
		t.Errorf("the queued lines must be applied before the turn is settled: %v", seen)
	}
}

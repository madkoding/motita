package tui

// The confirmation window is the one piece of the interface whose whole purpose is to STOP
// something from happening, so these tests are written around that: what the user sees, and what
// happens when they say no, when they say nothing at all, and when the run is cancelled under
// them. The window that runs anyway is the failure this file exists to rule out.

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

// confirmAgent is an agent that proposes a consequential command and BLOCKS until it is answered,
// which is what the real one does: the policy asks in the middle of a turn and the turn cannot
// continue without the answer.
//
// It is the shape that makes the window necessary. An agent that could answer itself would not
// need one.
type confirmAgent struct {
	command string
	// approver is what the runner installs. Its presence is what makes the turn ASK instead of
	// refusing, which is the behaviour under test.
	approver agent.Approver
	approved bool
	asked    bool
}

func (a *confirmAgent) SetApprover(fn agent.Approver)      { a.approver = fn }
func (a *confirmAgent) SetObserver(func(agent.TaskResult)) {}
func (a *confirmAgent) SetProgress(func(string, ...any))   {}
func (a *confirmAgent) SetTranscript([]agent.DialogueTurn) {}
func (a *confirmAgent) Transcript() []agent.DialogueTurn   { return nil }

func (a *confirmAgent) Run(ctx context.Context) error {
	if a.approver == nil {
		return nil // nobody to ask: the command is never proposed
	}
	a.asked = true
	a.approved, _ = a.approver(ctx, agent.ApprovalRequest{
		Command: a.command,
		Reason:  "the command writes outside the directory this task works in",
		Rule:    "write-outside-workspace",
	})
	return nil
}

func (a *confirmAgent) RunCommand(context.Context, string) (string, int, error) { return "", 0, nil }

// confirmTUI builds a TUI whose runner produces a blocking agent, with the given keystrokes
// waiting in the input.
func confirmTUI(t *testing.T, inputs string, command string) (*TUI, *AppRunner, *confirmAgent) {
	t.Helper()
	ag := &confirmAgent{command: command}
	r := NewAppRunner(&strings.Builder{}, &strings.Builder{}, config.Default(), nil, nil, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return ag
	}
	tui := newFakeTUI(inputs, r)
	return tui, r, ag
}

// TestConfirmationWindowShowsTheExactCommand: the user approves a TEXT, so the text is what they
// are shown. A window that showed a summary would be asking them to approve something they cannot
// read.
func TestConfirmationWindowShowsTheExactCommand(t *testing.T) {
	line := "rm -rf /home/alguien/proyectos/fuera"
	tui, _, _ := confirmTUI(t, "", line)

	tui.confirm = &confirmState{req: agent.ApprovalRequest{
		Command: line,
		Reason:  "escribe fuera del workspace",
	}, reply: make(chan bool, 1)}

	rendered := strings.Join(tui.confirmLines(0), "\n")
	if !strings.Contains(rendered, line) {
		t.Errorf("the window must show the exact command:\n%s", rendered)
	}
	if !strings.Contains(rendered, "escribe fuera del workspace") {
		t.Errorf("the window must say WHY it is asking:\n%s", rendered)
	}
	// The keys have to be visible, or the user cannot answer.
	if !strings.Contains(rendered, "y = yes") {
		t.Errorf("the window must say how to answer:\n%s", rendered)
	}
}

// TestConfirmationWindowWrapsALongCommand: a long line is exactly the one worth reading to the
// end, so it is wrapped rather than clipped.
func TestConfirmationWindowWrapsALongCommand(t *testing.T) {
	long := "git push origin " + strings.Repeat("una-rama-muy-larga/", 8)
	tui, _, _ := confirmTUI(t, "", long)
	tui.confirm = &confirmState{req: agent.ApprovalRequest{Command: long}, reply: make(chan bool, 1)}

	lines := tui.confirmLines(0)
	if len(lines) < 3 {
		t.Fatalf("a long command must occupy more than one row, got %d", len(lines))
	}
	// Every piece of the command survives somewhere across the rows.
	joined := strings.Join(lines, "")
	for _, part := range []string{"git", "push", "origin", "una-rama-muy-larga"} {
		if !strings.Contains(joined, part) {
			t.Errorf("the wrapped command must keep %q:\n%s", part, joined)
		}
	}
}

// TestConfirmationCapKeepsTheKeysHint: on a short terminal the window is cut, and the LAST row
// survives the cut. The user has to be able to see how to get out of a window they cannot read
// in full.
func TestConfirmationCapKeepsTheKeysHint(t *testing.T) {
	long := strings.Repeat("x", 2000)
	tui, _, _ := confirmTUI(t, "", long)
	tui.confirm = &confirmState{req: agent.ApprovalRequest{Command: long}, reply: make(chan bool, 1)}

	capped := tui.confirmLines(4)
	if len(capped) != 4 {
		t.Fatalf("the window must fit the cap, got %d rows", len(capped))
	}
	if !strings.Contains(strings.Join(capped, "\n"), "y = yes") {
		t.Errorf("the keys hint must survive the cut:\n%s", strings.Join(capped, "\n"))
	}
}

// TestConfirmationRowsMatchWhatIsDrawn: the layout reserves exactly what the window draws. A
// window that drew more rows than were reserved would push the frame past the bottom of the
// terminal and scroll it on a keypress.
func TestConfirmationRowsMatchWhatIsDrawn(t *testing.T) {
	tui, _, _ := confirmTUI(t, "", "rm -rf fuera")
	if rows := tui.confirmRows(); rows != 0 {
		t.Errorf("with nothing being confirmed the window takes no rows, got %d", rows)
	}
	// And with nothing being confirmed it draws nothing either: the frame must not reserve a
	// window that is not there.
	if lines := tui.confirmLines(0); lines != nil {
		t.Errorf("a closed window must draw nothing, got %#v", lines)
	}
	tui.confirm = &confirmState{req: agent.ApprovalRequest{Command: "rm -rf fuera"}, reply: make(chan bool, 1)}
	if got, want := tui.confirmRows(), len(tui.confirmLines(0)); got != want {
		t.Errorf("confirmRows = %d but confirmLines draws %d", got, want)
	}
	if tui.popupRows() != tui.confirmRows() {
		t.Error("the layout must reserve the window's rows")
	}
}

// answerTurn runs the agent's turn in the background and answers the confirmation from the run
// loop, which is the production arrangement: the agent blocks inside the turn waiting for the
// answer, and the loop reading the keys is what delivers it.
//
// The channel the agent uses is the one the runner would install, obtained the same way the
// runner obtains it (`tui.approverFor()`), so the test exercises the real handoff rather than a
// stand-in for it.
func answerTurn(t *testing.T, tui *TUI, ag *confirmAgent) {
	t.Helper()
	ag.approver = tui.approverFor()

	done := make(chan error, 1)
	go func() { done <- ag.Run(context.Background()) }()

	select {
	case c := <-tui.approvalChannel():
		tui.answerConfirm(context.Background(), c)
	case <-time.After(2 * time.Second):
		t.Fatal("the turn was expected to propose a consequential command and ask about it")
	}
	if err := <-done; err != nil {
		t.Fatalf("the turn must continue once the question is answered: %v", err)
	}
}

// TestSayYesApprovesTheCommand is the whole point, end to end: the agent blocks in a background
// goroutine, the user's keystroke reaches the loop, and the turn continues because the answer
// arrived.
func TestSayYesApprovesTheCommand(t *testing.T) {
	tui, _, ag := confirmTUI(t, "s\n", "rm -rf fuera")
	answerTurn(t, tui, ag)

	if !ag.asked {
		t.Fatal("the agent must have asked")
	}
	if !ag.approved {
		t.Error("the command must be approved when the user says yes")
	}
}

// TestTheApprovalChannelIsCreatedExactlyOnce is the guarantee that keeps a turn from hanging.
//
// The channel is reached from BOTH goroutines: the run loop receives on it while the agent, in
// its own goroutine, sends the request. A lazily-created channel without a Once is a data race
// between them, and the failure it produces is not a torn read — each side can come away with a
// DIFFERENT channel, so the loop waits for a request that will never arrive and the agent waits
// for an answer that will never come. The user sees the agent stop responding.
//
// Built with a struct literal on purpose: that is how the tests, and therefore any future caller,
// construct one, and it is the case a constructor-only fix would have missed.
func TestTheApprovalChannelIsCreatedExactlyOnce(t *testing.T) {
	tui := &TUI{}

	// Both goroutines call it at the same moment, which is what -race needs to see.
	const callers = 8
	got := make(chan chan *confirmState, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got <- tui.approvalChannel()
		}()
	}
	wg.Wait()
	close(got)

	first := <-got
	if first == nil {
		t.Fatal("the channel must exist after the first call")
	}
	for c := range got {
		if c != first {
			t.Fatal("every caller must get the SAME channel, or the turn hangs")
		}
	}
}

// TestTheApprovalChannelIsStableAcrossCalls: the same property, without concurrency, so a
// regression that only breaks the sequential case is caught too.
func TestTheApprovalChannelIsStableAcrossCalls(t *testing.T) {
	tui := &TUI{}
	// The channel is captured FIRST, then compared. Writing the call twice made the
	// assertion a tautology: the compiler saw two calls to the same method on the same
	// receiver in the same expression and staticcheck flagged it — and it was right, because
	// the property claimed is "the same channel comes back later", which is what capturing
	// it actually tests. Two calls in one expression prove nothing about the caching.
	first := tui.approvalChannel()
	if second := tui.approvalChannel(); first != second {
		t.Error("the channel must be the same one on every call")
	}
	if first == nil {
		t.Fatal("the channel must be created on first use")
	}
}

// TestSayNoRefusesTheCommand: a no is a no, and nothing runs. Enter is included because it is the
// CAUTIOUS answer: a stray Enter is far more likely than a considered one.
func TestSayNoRefusesTheCommand(t *testing.T) {
	for _, answer := range []string{"n\n", "no\n", "\n", "\x1b"} {
		tui, _, ag := confirmTUI(t, answer, "rm -rf fuera")
		answerTurn(t, tui, ag)
		if ag.approved {
			t.Errorf("answer %q must NOT approve the command", answer)
		}
	}
}

// TestTheWindowClosesAfterTheAnswer: an open window after the turn ended would capture the keys
// of a user who is trying to type their next message.
func TestTheWindowClosesAfterTheAnswer(t *testing.T) {
	tui, _, ag := confirmTUI(t, "s\n", "rm -rf fuera")
	answerTurn(t, tui, ag)
	if tui.answeringConfirm() {
		t.Error("the window must close once the answer is given")
	}
}

// TestTheRunnerInstallsTheChannelOnEveryTurn'sAgent is the plumbing the handoff above depends on:
// a turn builds a NEW agent, so an install that happened once would leave the second turn with
// nobody to ask.
func TestTheRunnerInstallsTheChannelOnTheTurnsAgent(t *testing.T) {
	ag := &confirmAgent{command: "rm -rf fuera"}
	cfg := config.Default()
	cfg.LLM.APIKey = "x" // the turn needs an engine to build one; it never calls it
	r := NewAppRunner(&strings.Builder{}, &strings.Builder{}, cfg, nil, nil, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return ag
	}

	// Without an interface installing one, there is no channel, and the turn must therefore not
	// have one to call: this is the state a batch run is in.
	if _, err := r.RunTask(context.Background(), "una tarea", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if ag.approver != nil {
		t.Error("with no interface there is nobody to ask, so no channel may be installed")
	}

	// The interface installs one, and the next turn's agent receives it. The turn then ASKS, so
	// it has to be answered from the run loop — which is what the interface does while the turn
	// is in flight.
	tui := newFakeTUI("s\n", r)
	tui.installApprover()
	if r.approverOrNil() == nil {
		t.Fatal("the interface must install its channel on the runner")
	}

	done := make(chan error, 1)
	go func() {
		_, err := r.RunTask(context.Background(), "otra tarea", func(string, ...any) {})
		done <- err
	}()
	select {
	case c := <-tui.approvalChannel():
		tui.answerConfirm(context.Background(), c)
	case <-time.After(2 * time.Second):
		t.Fatal("the turn was expected to ask through the installed channel")
	}
	if err := <-done; err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	// The handoff worked: the agent got the channel AND the answer came back through it.
	if !ag.asked || !ag.approved {
		t.Errorf("the installed channel must reach the agent and carry the answer back "+
			"(asked=%v approved=%v)", ag.asked, ag.approved)
	}
}

// TestACancelledRunRefusesTheCommand: silence is not consent. When the run is cancelled under an
// open window, the command is refused — and the agent must not be left parked forever.
func TestACancelledRunRefusesTheCommand(t *testing.T) {
	tui, _, _ := confirmTUI(t, "", "rm -rf fuera")

	ctx, cancel := context.WithCancel(context.Background())
	approved := make(chan bool, 1)
	go func() {
		ok, _ := tui.askConfirm(ctx, agent.ApprovalRequest{Command: "rm -rf fuera"})
		approved <- ok
	}()

	c := <-tui.approvalChannel()
	// Nothing is read from the input: the window is open and the run is cancelled.
	cancel()
	// The reader goroutine is what would have answered; cancelling makes it give up.
	go tui.answerConfirm(ctx, c)

	select {
	case ok := <-approved:
		if ok {
			t.Error("a cancelled run must not approve a consequential command")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled run must not leave the agent waiting forever")
	}
}

// TestACancelledRunBeforeTheWindowOpensIsAlsoRefused: the cancellation can land on the OTHER side
// of the handoff — the agent is waiting to deliver a command and the run dies first. That path is
// a different branch, and it has to refuse as well: an agent left waiting here would hang the
// turn instead of ending it.
func TestACancelledRunBeforeTheWindowOpensIsAlsoRefused(t *testing.T) {
	tui, _, _ := confirmTUI(t, "", "rm -rf fuera")

	// The channel nobody is reading: the run loop has already gone.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	approved := make(chan bool, 1)
	go func() {
		ok, _ := tui.askConfirm(ctx, agent.ApprovalRequest{Command: "rm -rf fuera"})
		approved <- ok
	}()

	select {
	case ok := <-approved:
		if ok {
			t.Error("a run cancelled before the window opened must not approve anything")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the agent must not wait forever for a window that will never open")
	}
}

// TestAClosedInputRefusesTheCommand: the input ending (EOF) while the window is open is not an
// approval either.
func TestAClosedInputRefusesTheCommand(t *testing.T) {
	tui, _, _ := confirmTUI(t, "" /* no input at all: EOF */, "rm -rf fuera")

	ctx := context.Background()
	approved := make(chan bool, 1)
	go func() {
		ok, _ := tui.askConfirm(ctx, agent.ApprovalRequest{Command: "rm -rf fuera"})
		approved <- ok
	}()
	c := <-tui.approvalChannel()
	tui.answerConfirm(ctx, c)

	if ok := <-approved; ok {
		t.Error("an input that ended is not an approval")
	}
}

// TestAnUnknownKeyDoesNotAnswer: only the keys the window names are answers. Any other keystroke
// leaves the question open, so a typo cannot become a yes.
func TestAnUnknownKeyDoesNotAnswer(t *testing.T) {
	tui, _, _ := confirmTUI(t, "x\nz\ns\n", "rm -rf fuera")

	ctx := context.Background()
	approved := make(chan bool, 1)
	go func() {
		ok, _ := tui.askConfirm(ctx, agent.ApprovalRequest{Command: "rm -rf fuera"})
		approved <- ok
	}()
	c := <-tui.approvalChannel()
	tui.answerConfirm(ctx, c)

	if ok := <-approved; !ok {
		t.Error("the window must keep asking until a key it knows is pressed, then approve on s")
	}
}

// TestTheRunLoopAnswersTheConfirmation: the branch of `awaitRun` this whole mechanism depends on.
//
// The turn runs in a background goroutine — exactly as `runTask` starts it — and the loop that
// drives it is also the loop that reads the keys. That is the arrangement that makes an approval
// possible at all: the agent cannot read anything itself while it is blocked waiting.
func TestTheRunLoopAnswersTheConfirmation(t *testing.T) {
	tui, _, ag := confirmTUI(t, "s\n", "rm -rf fuera")
	ag.approver = tui.approverFor()

	progress := make(chan string, 4)
	done := make(chan runOutcome, 1)
	go func() {
		done <- runOutcome{result: "listo", err: ag.Run(context.Background())}
	}()

	out := tui.awaitRun(context.Background(), progress, done, func(string) {})
	if out.err != nil {
		t.Fatalf("the turn must finish once the confirmation is answered: %v", out.err)
	}
	if out.result != "listo" {
		t.Errorf("the outcome must be the runner's: %q", out.result)
	}
	// The proof that the branch was taken: the agent asked, and the answer it got was a yes.
	if !ag.asked || !ag.approved {
		t.Errorf("the run loop must have answered the confirmation (asked=%v approved=%v)",
			ag.asked, ag.approved)
	}
	if tui.answeringConfirm() {
		t.Error("the window must be closed when the loop returns")
	}
}

// TestTheRunLoopRefusesWhenTheUserDeclines: the same branch, with the other answer, so the test
// above cannot pass by approving everything.
func TestTheRunLoopRefusesWhenTheUserDeclines(t *testing.T) {
	tui, _, ag := confirmTUI(t, "n\n", "rm -rf fuera")
	ag.approver = tui.approverFor()

	progress := make(chan string, 4)
	done := make(chan runOutcome, 1)
	go func() {
		done <- runOutcome{result: "listo", err: ag.Run(context.Background())}
	}()

	if out := tui.awaitRun(context.Background(), progress, done, func(string) {}); out.err != nil {
		t.Fatalf("the turn must finish: %v", out.err)
	}
	if ag.approved {
		t.Error("a declined command must not be approved")
	}
}

// TestTheConversationRecordsTheDecision: the transcript keeps what was approved and what was not,
// because the next turn has to be able to see it.
//
// It asserts through the FRAME, not by calling the helper: the note only means something if it
// reaches the conversation the user reads.
func TestTheConversationRecordsTheDecision(t *testing.T) {
	for _, c := range []struct {
		answer  string
		note    string
		command string
	}{
		{"s\n", "approved", "rm -rf fuera"},
		{"n\n", "rejected", "rm -rf fuera"},
	} {
		tui, _, ag := confirmTUI(t, c.answer, c.command)
		ag.approver = tui.approverFor()

		progress := make(chan string, 4)
		done := make(chan runOutcome, 1)
		go func() {
			done <- runOutcome{result: "listo", err: ag.Run(context.Background())}
		}()
		tui.awaitRun(context.Background(), progress, done, func(string) {})

		var found bool
		for _, m := range tui.messages {
			if strings.Contains(m.Text, c.note) && strings.Contains(m.Text, c.command) {
				found = true
			}
		}
		if !found {
			t.Errorf("the transcript must record the decision (%q) on %q, got %+v",
				c.note, c.command, tui.messages)
		}
	}
}

// TestTheOutcomeIsNeverDecidedByTheWindowAlone: the window answers a question and nothing else. An
// agent that runs is the agent's decision, taken with the answer it was given.
func TestTheWindowOnlyAnswersTheQuestion(t *testing.T) {
	tui, _, _ := confirmTUI(t, "", "rm -rf fuera")
	buf := &bytes.Buffer{}

	// The window draws into the frame like any other piece of view state.
	tui.Out = buf
	tui.confirm = &confirmState{req: agent.ApprovalRequest{Command: "rm -rf fuera"}, reply: make(chan bool, 1)}
	tui.drawFrame()

	if !strings.Contains(buf.String(), "rm -rf fuera") {
		t.Errorf("the window must be drawn in the frame:\n%s", buf.String())
	}
}

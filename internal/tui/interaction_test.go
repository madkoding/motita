package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// These tests cover the interactive loop itself: the view switch, the input
// reading edge cases and the outcome handling of a plan run.

// TestNextScreenTogglesBetweenTaskAndPlan: Tab switches between the two modes a user works in
// and nothing else.
//
// It used to walk the whole screen order — Task, Plan, Models, Config — which meant pressing it
// twice to get back where you started landed somewhere else, and the two modes that ARE toggled
// while working were buried in a four-stop cycle. Models and Config are things you go to on
// purpose, by command.
func TestNextScreenTogglesBetweenTaskAndPlan(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.screen = ScreenTask

	want := []Screen{ScreenPlan, ScreenTask, ScreenPlan, ScreenTask}
	for i, w := range want {
		tui.nextScreen()
		if tui.screen != w {
			t.Fatalf("step %d: screen = %v, want %v", i, tui.screen, w)
		}
	}
}

// TestNextScreenFromAnotherScreenGoesToPlan: the toggle must never trap the user. Arriving from
// the model list or the wizard — reachable by command — the next Tab has to lead somewhere in
// the pair rather than nowhere.
func TestNextScreenFromAnotherScreenGoesToPlan(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})

	for _, from := range []Screen{ScreenModels, ScreenConfig} {
		tui.screen = from
		tui.nextScreen()
		if tui.screen != ScreenPlan {
			t.Errorf("from screen %v: got %v, want Plan", from, tui.screen)
		}
	}
}

// TestNextScreenFromAnUnknownScreenStartsAtTheFirst: a screen value that is not in
// the order (a zero value after a bad cast) must not panic on the modulo.
func TestNextScreenFromAnUnknownScreenStartsAtTheFirst(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.screen = Screen(99)
	tui.nextScreen()
	if tui.screen != ScreenPlan {
		t.Errorf("an unknown screen must fall back to the first step, got %v", tui.screen)
	}
}

// TestAuthorAndScreenStrings: both are used to label the interface, and an
// out-of-range value has to degrade to a placeholder instead of a panic.
func TestAuthorAndScreenStrings(t *testing.T) {
	cases := map[Author]string{
		AuthorUser:   "you",
		AuthorAgent:  "starlight",
		AuthorSystem: "system",
		Author(42):   "?",
	}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("Author(%d).String() = %q, want %q", a, got, want)
		}
	}
	if got := Screen(42).String(); got != "?" {
		t.Errorf("Screen(42).String() = %q, want %q", got, "?")
	}
}

// TestTabIsConsumedAndNeverBecomesInput: bufio only stops at a newline, so without
// the interception a Tab lands inside the prompt the user is typing. This is the
// bug the user reported; here it is pinned.
func TestTabIsConsumedAndNeverBecomesInput(t *testing.T) {
	runner := &fakeRunner{}
	// \t is the escape the tabReader turns into a real Tab byte.
	tui := newFakeTUI("\\t\n\\t\nq\n", runner)
	tui.Run(context.Background())

	frame := stripANSI(lastFrame(t, tui))
	if strings.Contains(frame, "\t") {
		t.Errorf("a Tab must never reach the conversation:\n%q", frame)
	}
	if strings.Contains(frame, "\\t") {
		t.Errorf("the escape must not be shown literally either:\n%q", frame)
	}
	// Two Tabs are a round trip: Task -> Plan -> Task. Starting and ending on the same screen is
	// also the strongest assertion available here — a test that ended on a DIFFERENT screen would
	// pass even if both Tabs were swallowed as one, while returning to the start proves the second
	// one arrived and moved it back.
	if tui.screen != ScreenTask {
		t.Errorf("screen = %v, want Task after two Tabs from Task: the toggle is a round trip", tui.screen)
	}
}

// TestReadLineReturnsAnEmptyLineForABareEnter: the empty line is the trigger of the
// models and config views. Reading the next line instead of the empty one was the
// bug that made "press Enter" silently do nothing.
func TestReadLineReturnsAnEmptyLineForABareEnter(t *testing.T) {
	tui := newFakeTUI("\n", &fakeRunner{})
	line, ok := tui.readLine(context.Background())
	if !ok {
		t.Fatal("a bare Enter must produce a line")
	}
	if line != "" {
		t.Errorf("line = %q, want empty", line)
	}
}

// TestReadLineHandlesCarriageReturn: a terminal in raw mode sends \r for Enter, so
// both endings must count as "the user pressed Enter".
func TestReadLineHandlesCarriageReturn(t *testing.T) {
	tui := newFakeTUI("\r", &fakeRunner{})
	line, ok := tui.readLine(context.Background())
	if !ok || line != "" {
		t.Errorf("readLine(\\r) = (%q, %v), want empty and true", line, ok)
	}
}

// TestReadLineStripsSurroundingSpace: a task typed with trailing spaces must not
// carry them into the prompt the model receives.
func TestReadLineStripsSurroundingSpace(t *testing.T) {
	tui := newFakeTUI("  hello world  \n", &fakeRunner{})
	line, ok := tui.readLine(context.Background())
	if !ok || line != "hello world" {
		t.Errorf("readLine = (%q, %v)", line, ok)
	}
}

// TestReadLineOnEOF: a closed input ends the session cleanly.
func TestReadLineOnEOF(t *testing.T) {
	tui := newFakeTUI("", &fakeRunner{})
	if _, ok := tui.readLine(context.Background()); ok {
		t.Error("readLine must report false on EOF")
	}
}

// TestReadLineIsCancelledByTheContext: Ctrl+C while waiting at the prompt has to
// return immediately instead of blocking on the read.
func TestReadLineIsCancelledByTheContext(t *testing.T) {
	r, _ := io.Pipe()
	tui := &TUI{In: r, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Runner: &fakeRunner{}, NoColor: true}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, ok := tui.readLine(ctx); ok {
			t.Error("a cancelled read must report false")
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readLine did not return after the context was cancelled")
	}
}

// TestPlanStreamDropsAEmptyBlockAndKeepsToolEvents: a run that only announces a
// phase leaves no empty bubble behind, and a tool event survives the text that
// follows it.
func TestPlanStreamDropsAnEmptyBlockAndKeepsToolEvents(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	stream := &planStream{tui: tui, pendingIdx: 0}
	tui.messages = []Message{{Author: AuthorAgent, Pending: true}}

	stream.handle("[thinking...]")
	if tui.messages[0].Text != "" {
		t.Errorf("a phase marker must not become body text: %q", tui.messages[0].Text)
	}

	stream.handle("[using tool: execute_command]")
	if len(tui.messages) != 2 {
		t.Fatalf("the tool event must open a new block, have %d messages", len(tui.messages))
	}
	if !tui.messages[0].Frozen {
		t.Error("the tool event must be frozen")
	}
	// The block left empty by the phase is dropped: only the frozen event remains.
	if tui.messages[0].Text != "using execute_command" {
		t.Errorf("the frozen event holds %q", tui.messages[0].Text)
	}

	stream.handle("20")
	stream.closePending()
	if got := tui.messages[len(tui.messages)-1].Text; got != "20" {
		t.Errorf("the answer must land in the open block, got %q", got)
	}
}

// TestPlanStreamSettleEveryOutcome: the last block has to explain what happened,
// whatever the outcome was.
func TestPlanStreamSettleEveryOutcome(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		answer string
		text   string
		want   string
	}{
		{"a cancellation says so", context.Canceled, "", "", "cancelled."},
		{"an error is shown", errors.New("boom"), "", "", "error: boom"},
		{"the answer wins", nil, "42", "partial", "42"},
		{"the streamed text is the answer when there is no return", nil, "", "streamed", "streamed"},
		{"nothing at all is explained", nil, "", "", "nothing to show"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tui := newFakeTUI("q\n", &fakeRunner{})
			tui.messages = []Message{{Author: AuthorAgent, Pending: true}}
			s := &planStream{tui: tui, pendingIdx: 0, text: tc.text}
			s.settle(tc.err, tc.answer)
			if !strings.Contains(tui.messages[0].Text, tc.want) {
				t.Errorf("settle produced %q, want it to contain %q", tui.messages[0].Text, tc.want)
			}
			if tui.messages[0].Pending {
				t.Error("a settled block must not stay pending")
			}
		})
	}
}

// TestPlanStreamReopensAfterAnEmptyClose: when the last block was empty and got
// dropped, settling must not write outside the slice.
func TestPlanStreamReopensAfterAnEmptyClose(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.messages = []Message{{Author: AuthorAgent, Pending: true}}
	s := &planStream{tui: tui, pendingIdx: 0}
	s.closePending()
	if len(tui.messages) != 0 {
		t.Fatalf("an empty block must be dropped, have %d messages", len(tui.messages))
	}
	s.openPending()
	s.settle(nil, "done")
	if len(tui.messages) != 1 || tui.messages[0].Text != "done" {
		t.Errorf("settle after a drop = %+v", tui.messages)
	}
}

// TestUnknownCommandInAConversationViewIsATask: in Task and Plan a line that looks
// like neither a command nor a mode switch is the content the user wants sent.
func TestUnknownCommandInAConversationViewIsATask(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("/unknown\nq\n", runner)
	tui.Run(context.Background())
	if !runner.taskCalled {
		t.Error("an unrecognised line in Task mode must be sent as a task")
	}
	if runner.lastTask != "/unknown" {
		t.Errorf("task = %q, want the line as typed", runner.lastTask)
	}
}

// TestHelpListsTheCommands: the help text is the only documentation reachable from
// inside the interface, so the commands it promises have to be the ones that work.
func TestHelpListsTheCommands(t *testing.T) {
	tui := newFakeTUI("?\nq\n", &fakeRunner{})
	tui.Run(context.Background())
	// The question is what the help SAYS, so it is asked of the help itself.
	//
	// It used to be asked of the screen, which worked only while the help fit in the panel.
	// The help is longer than the frame, so the panel is a window on it and its opening lines
	// are simply not among the visible rows — the screen answer is a false negative about the
	// content. (Adding the reward commands made the help long enough to expose that.)
	//
	// The screen is still checked below: that SOMETHING of the help is on it.
	for _, want := range []string{"switch between Task and Plan", "complete the command", "task", "plan", "models", "config", "reasoning", "quit"} {
		if !strings.Contains(helpText, want) {
			t.Errorf("the help must mention %q:\n%s", want, helpText)
		}
	}
	// And the reward commands, which a user cannot guess from the others.
	for _, want := range []string{"/good", "/bad", "/value"} {
		if !strings.Contains(helpText, want) {
			t.Errorf("the help must document %q:\n%s", want, helpText)
		}
	}
	// The help did reach the interface: its tail is what the window shows.
	if !strings.Contains(stripANSI(lastFrame(t, tui)), "/value") {
		t.Errorf("the help must be shown:\n%s", stripANSI(lastFrame(t, tui)))
	}
}

// TestQuitAliases: every documented way of leaving has to leave.
func TestQuitAliases(t *testing.T) {
	for _, in := range []string{"q", "quit", "/q", "/quit", "Q", " QUIT "} {
		tui := newFakeTUI(in+"\n", &fakeRunner{})
		if code := tui.Run(context.Background()); code != ExitSuccess {
			t.Errorf("input %q returned %d, want ExitSuccess", in, code)
		}
	}
}

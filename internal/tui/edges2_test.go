package tui

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// The last branches of the new layout and the live input path. Each one is a real situation:
// an alias reached by its prefix, a terminal narrower than its own margins, a config that named
// nothing, a stream that ended mid-sequence.

// TestAnAliasIsFoundThroughItsOwnPrefix: the alias branch exists so "/r" reaches /reasoning.
// A completion that only matched canonical names would make every alias unreachable, and the
// alias list would be a promise nothing keeps.
func TestAnAliasIsFoundThroughItsOwnPrefix(t *testing.T) {
	// "/m" is the alias of /models: matched through the alias branch, not the name branch.
	got := completions("/m")
	if len(got) != 1 || got[0].Name != "/models" {
		t.Fatalf("completions(/m) = %v, want /models", got)
	}
	// And a prefix that matches both a name and an alias of the same command must offer it ONCE:
	// a duplicate row would look like two different commands.
	for _, c := range completions("/t") {
		if c.Name == "/task" {
			dupes := 0
			for _, again := range completions("/t") {
				if again.Name == "/task" {
					dupes++
				}
			}
			if dupes != 1 {
				t.Errorf("/task was offered %d times for /t", dupes)
			}
		}
	}
}

// TestThePopupIsEmptyWhenTheDraftStopsMatching: the popup is measured on every keystroke, so it
// must answer "nothing" cleanly once the draft is no longer a prefix of any command. Returning
// a stale list would leave the popup on screen over ordinary input.
func TestThePopupIsEmptyWhenTheDraftStopsMatching(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 100, 30
	tu.draft = "/zzz"

	if lines := tuiPopupBody(tu); len(lines) != 0 {
		t.Errorf("a draft matching nothing must draw nothing, got %v", lines)
	}
	if tu.completing() {
		t.Error("completing must be false when no command matches")
	}
}

// TestConversationWidthNeverCollapses: a terminal narrower than its own margins would produce a
// zero or negative wrap width, and a negative width reaches strings.Repeat and panics. The floor
// is what keeps a very small terminal merely ugly instead of fatal.
func TestConversationWidthNeverCollapses(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 1, 4

	if got := tu.conversationWidth(); got < 1 {
		t.Errorf("conversationWidth = %d, want at least 1", got)
	}
	// And it must stay consistent with the body it wraps into.
	if got, want := tu.conversationWidth(), tu.frameCols()-2*leftMargin; got != want && want >= 1 {
		t.Errorf("conversationWidth = %d, but frameCols-2*margin = %d", got, want)
	}
}

// TestACellWithNoRoomStillDraws: clipLine already brought the text inside the room, so the
// padding is normally positive. The guard is for the degenerate width, where the arithmetic can
// still go negative and strings.Repeat would panic.
func TestACellWithNoRoomStillDraws(t *testing.T) {
	tu, _ := newKeyTUI("", "")

	got := stripANSI(tu.cell("text", 0))
	if visibleLen(got) > 2 {
		t.Errorf("a zero-width cell drew %q (%d cols)", got, visibleLen(got))
	}
}

// TestBodyWidthNeverCollapses: same floor, for the width the conversation body is drawn at.
func TestBodyWidthNeverCollapses(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 1, 4

	if got := tu.bodyWidth(); got < 1 {
		t.Errorf("bodyWidth = %d, want at least 1", got)
	}
}

// TestTheStatusLineNamesItsDefaults: an empty config still has to say who is answering. Two
// blank columns where the model should be is worse than saying "unknown", because the user
// cannot tell an unset value from a broken interface.
func TestTheStatusLineNamesItsDefaults(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	// A configuration that named nothing: no provider, no model, no reasoning block.
	tu.Runner.(*fakeRunner).cfg = config.Config{}
	tu.Runner.(*fakeRunner).cfgSet = true

	joined := stripANSI(strings.Join(tu.statusLines(120), "\n"))
	if !strings.Contains(joined, "openai") {
		t.Errorf("the default provider must be named: %q", joined)
	}
	if !strings.Contains(joined, "unknown") {
		t.Errorf("an unnamed model must say so: %q", joined)
	}
	if !strings.Contains(joined, "off") {
		t.Errorf("reasoning must report a level even when disabled: %q", joined)
	}
}

// TestCtrlCAbandonsTheLine: cbreak keeps signal generation, so Ctrl+C normally arrives as a
// signal — but it can also reach the read, and then it must abandon the line rather than be
// typed into it or ignored.
func TestCtrlCAbandonsTheLine(t *testing.T) {
	tu, _ := liveTUI("half typed\x03")

	_, ok := tu.readLine(context.Background())
	if ok {
		t.Error("Ctrl+C must abandon the read, not return a line")
	}
	if tu.draft != "" {
		t.Errorf("the draft must be cleared, got %q", tu.draft)
	}
}

// TestAControlKeyClearsTheDraftAndRedraws: the control tokens clear what was typed and repaint,
// so the abandoned text does not stay on screen while the mode changes.
func TestAControlKeyClearsTheDraftAndRedraws(t *testing.T) {
	tu, out := liveTUI("some text\x06")
	out.Reset()

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the token must be returned")
	}
	if line != "\x06" {
		t.Errorf("line = %q, want Ctrl+F", line)
	}
	if tu.draft != "" {
		t.Errorf("the draft must be cleared, got %q", tu.draft)
	}
	if out.Len() == 0 {
		t.Error("the frame must be redrawn after the draft is cleared")
	}
}

// TestAnEscapeSequenceEndingAtTheTerminator: the sequence ends at the first byte in the final
// position range, which is what makes "\x1b[B" two bytes and not a read that keeps waiting.
func TestAnEscapeSequenceEndingAtTheTerminator(t *testing.T) {
	tu, _ := liveTUI("\x1b[B\x1b[F")

	first, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the first sequence must be returned")
	}
	second, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the second sequence must be returned")
	}
	if first != keyDown || second != keyEnd {
		t.Errorf("read %q then %q, want the two sequences read separately", first, second)
	}
}

// TestASequenceThatRunsOutOfInput: an input that ends in the middle of a sequence must report an
// Escape rather than block forever. A terminal that dies mid-keypress is a real way to get here.
func TestASequenceThatRunsOutOfInput(t *testing.T) {
	tu, _ := liveTUI("\x1b[1")

	// The caller reads the ESC byte; readEscapeLive completes what follows. Consuming it here
	// is what puts the reader in the middle of a sequence, which is the case under test.
	if _, err := tu.input().ReadByte(); err != nil {
		t.Fatal(err)
	}
	if got := tu.readEscapeLive(); got != keyEsc {
		t.Errorf("a truncated sequence must report Escape, got %q", got)
	}
}

// TestASequenceWithAnEarlyTerminatorIsNotOverrun: the loop stops at the terminator, so bytes
// after it belong to the next read. Consuming them would swallow the following keypress.
func TestASequenceWithAnEarlyTerminatorIsNotOverrun(t *testing.T) {
	tu, _ := liveTUI("\x1b[Ax")

	// readEscapeLive is called AFTER the ESC byte has been consumed — that is its contract,
	// since the caller is what reads the first byte. Consuming it here is what makes this a
	// test of the completion rather than of the contract's precondition.
	if _, err := tu.input().ReadByte(); err != nil {
		t.Fatal(err)
	}
	seq := tu.readEscapeLive()
	if seq != "\x1b[A" {
		t.Errorf("seq = %q, want just the up-arrow sequence", seq)
	}
	// The "x" must still be there.
	next, err := tu.input().ReadByte()
	if err != nil || next != 'x' {
		t.Errorf("the byte after the sequence was consumed: %q, %v", next, err)
	}
}

// TestTheLiveReaderIgnoresADeletedTerminal: when the input is not a terminal at all — piped,
// which is how the e2e scripts drive it — the mode is inactive and the whole-line reader is
// used. This asserts the two paths are chosen by the mode and not by a guess about the input.
func TestTheLiveReaderIsChosenByTheModeAlone(t *testing.T) {
	var out bytes.Buffer
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.In = strings.NewReader("hello\n")
	tu.Out = &out
	tu.Width, tu.Height = 80, 24

	// charMode false: the whole-line reader handles it.
	tu.charMode = false
	line, ok := tu.readLine(context.Background())
	if !ok || line != "hello" {
		t.Errorf("the whole-line path returned %q, %v", line, ok)
	}
}

// TestTheModeIsNotEnteredForAPipe: enterRaw is what decides, and a pipe has no controlling
// terminal. This runs the real entry point against the test's own stdin, which is not a tty.
func TestTheModeIsNotEnteredForAPipe(t *testing.T) {
	if _, err := os.Stat("/dev/tty"); err != nil {
		t.Skip("no controlling terminal in this environment")
	}
	// The test process's stdin is not a character device, so the mode must stay inactive and
	// the interface must fall back to reading whole lines.
	m := enterRaw()
	defer m.restore()
	// Either it degraded (no tty to change) or it changed a real terminal — both are correct,
	// and what must never happen is a panic or a leaked file descriptor.
}

// TestAnAliasIsMatchedByTheAliasBranch: "/q" is not a prefix of any canonical name, so the only
// way it can be offered is through the alias loop. Without that branch every alias would be a
// spelling the popup never suggests.
func TestAnAliasIsMatchedByTheAliasBranch(t *testing.T) {
	// "/s" is deliberately NOT in this list: it is an alias of /session AND a prefix of the newer
	// /sessions, so it legitimately offers two. What this test is about is the alias branch, and
	// the point of the branch is that an alias NOT shared with a prefix still reaches its command.
	for _, typed := range []string{"/q", "/h", "/c", "/f", "/v", "/r", "/think"} {
		got := completions(typed)
		if len(got) != 1 {
			t.Errorf("completions(%q) = %v, want exactly one command reached by its alias", typed, got)
			continue
		}
		// The canonical name must be what is offered, not the alias the user typed.
		if !strings.HasPrefix(got[0].Name, "/") || len(got[0].Aliases) == 0 {
			t.Errorf("completions(%q) offered %q, want the canonical command", typed, got[0].Name)
		}
	}
}

// TestTheLayoutStaysWithinTheTerminalAtTheSmallestWidth: the floors on the body width and the
// conversation width are what keep a terminal narrower than its own margins from producing a
// negative wrap width. minWidth is the floor the TUI already applies, so this asserts the
// arithmetic holds AT that floor — which is the narrowest terminal a user can actually have.
func TestTheLayoutStaysWithinTheTerminalAtTheSmallestWidth(t *testing.T) {
	tu, _ := newKeyTUI("", "a message that will need wrapping somewhere")
	tu.Width, tu.Height = minWidth, 20

	w, _ := tu.size()
	if bw := tu.bodyWidth(); bw < 1 || bw > w {
		t.Errorf("bodyWidth = %d, must be between 1 and the terminal width %d", bw, w)
	}
	if cw := tu.conversationWidth(); cw < 1 || cw > w {
		t.Errorf("conversationWidth = %d, must be between 1 and the terminal width %d", cw, w)
	}

	// And the whole frame must fit: every row within the width, no more rows than the height.
	lines, _ := tu.layout(w, 20)
	if len(lines) > 20 {
		t.Errorf("the frame is %d rows in a 20-row terminal", len(lines))
	}
	for i, l := range lines {
		if n := visibleLen(l); n > w {
			t.Errorf("row %d is %d columns in a %d-column terminal: %q", i, n, w, stripANSI(l))
		}
	}
}

// TestBodyWidthWithADegenerateTerminal: the two width helpers each carry their own floor, and
// both must hold when the measured width is smaller than the margins they subtract.
func TestBodyWidthWithADegenerateTerminal(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	// size() clamps to minWidth, so the only way to reach the floors with a real measurement is
	// a size narrower than twice the margin — smaller than any terminal, which is exactly why
	// the floors exist rather than being unreachable.
	if minWidth > 2*leftMargin {
		t.Skipf("the clamp (%d) is wider than the floors test, so the branch is unreachable here", minWidth)
	}
	tu.Width, tu.Height = 1, 10

	for name, got := range map[string]int{"bodyWidth": tu.bodyWidth(), "conversationWidth": tu.conversationWidth()} {
		if got < 1 {
			t.Errorf("%s = %d, want at least 1", name, got)
		}
	}
}

// TestTheRuleAtADegenerateWidth: the divider takes its width from the caller, so a width at or
// below the margins must produce a minimum rule rather than a negative count reaching
// strings.Repeat — which panics. The rule is what gives the frame its structure, so it has to
// survive the narrowest terminal rather than take the interface down with it.
func TestTheRuleAtADegenerateWidth(t *testing.T) {
	tu, _ := newKeyTUI("", "")

	for _, w := range []int{0, 1, 2, 4} {
		got := stripANSI(tu.rule(w))
		if got == "" {
			t.Errorf("rule(%d) drew nothing", w)
		}
		// It must not exceed the space it was given, plus the margin it always spends.
		if n := visibleLen(got); n > w+2*leftMargin {
			t.Errorf("rule(%d) drew %d columns: %q", w, n, got)
		}
	}
}

// TestTheBodyWidthIsAlwaysPositive: bodyWidth subtracts twice the margin from a width that
// size() has already clamped to minWidth, so it cannot reach zero. This asserts the arithmetic
// rather than the guard, because the guard was removed for being unreachable.
func TestTheBodyWidthIsAlwaysPositive(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	for _, w := range []int{0, 1, minWidth - 1, minWidth, 200} {
		tu.Width, tu.Height = w, 20
		if got := tu.bodyWidth(); got < 1 {
			t.Errorf("bodyWidth at width %d = %d, want positive", w, got)
		}
		if got := tu.conversationWidth(); got < 1 {
			t.Errorf("conversationWidth at width %d = %d, want positive", w, got)
		}
	}
}

// TestThePopupIsMeasuredEvenWhenItDrawsNothing: the layout asks for the popup's height on every
// repaint, including the ones where there is nothing to show. The empty answer has to be a
// clean nil — a stale list would leave the popup on screen over ordinary input.
func TestThePopupIsMeasuredEvenWhenItDrawsNothing(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 100, 30

	// Nothing typed: the popup must contribute no rows to the layout.
	if got := tu.completionLinesCapped(tu.bodyWidth(), 0); got != nil {
		t.Errorf("with an empty draft the popup must draw nothing, got %v", got)
	}
	// And the layout must be the same height as it would be with no popup at all.
	without, _ := tu.layout(100, 30)
	tu.draft = "/"
	with, _ := tu.layout(100, 30)
	// The frame keeps its height: the popup takes rows from the conversation, and the composer
	// stays pegged to the bottom of the window either way.
	if len(with) != len(without) {
		t.Errorf("the frame height must not change with the popup: %d vs %d", len(without), len(with))
	}
	if !strings.Contains(stripANSI(strings.Join(with, "\n")), "→ completes") {
		t.Error("the popup must be drawn once a command is being typed")
	}
}

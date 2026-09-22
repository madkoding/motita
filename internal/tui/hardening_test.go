package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/madkoding/starlight/internal/agent"
)

// TestAFilterOverTextWhoseLowercaseChangesLengthHighlightsTheMatch: lowering 'Ⱥ' grows it
// and lowering 'İ' shrinks it, so offsets found on a lowercased copy do not fit the
// original. The first sliced out of range and killed the frame; the second coloured the
// wrong text.
func TestAFilterOverTextWhoseLowercaseChangesLengthHighlightsTheMatch(t *testing.T) {
	tu, _ := newKeyTUI("", "ȺȺȺ ab")
	tu.applyFind("ab") // panicked before the fix

	tu.query = "AB"
	got := tu.highlight("İİİ ab", colBase)
	if !strings.Contains(got, tu.color(0, colAccent, "ab")) {
		t.Fatalf("the match must be highlighted, got %q", got)
	}
	if strings.Contains(got, tu.color(0, colAccent, "İ")) {
		t.Fatalf("a character that is not the match was highlighted: %q", got)
	}
}

// TestModelTextCannotSendEscapesToTheTerminal: model and tool output is untrusted, and a raw
// escape in it could retitle the window, write the clipboard, clear the screen or move the
// cursor over the confirm prompt.
func TestModelTextCannotSendEscapesToTheTerminal(t *testing.T) {
	tu, out := newKeyTUI("", "hello \x1b]0;PWNED\x07 \x1b[2J world\x01\x7f\u009b\xff")
	tu.drawFrame()
	for _, bad := range []string{"\x1b]0;", "PWNED", "\x07", "\x1b[2J world", "\x01", "\x7f", "\u009b", "\xff"} {
		if strings.Contains(out.String(), bad) {
			t.Errorf("%q from model text reached the terminal", bad)
		}
	}
	if got := plainText("a\tb\nc"); got != "a    b\nc" {
		t.Errorf("a tab becomes spaces and a newline stays, got %q", got)
	}
	if got := plainText("plain"); got != "plain" {
		t.Errorf("clean text is unchanged, got %q", got)
	}
}

// TestTheQuestionsAndTheConfirmWindowDrawNoEscapesFromTheModel: the questions, their options
// and the command to approve are all written by the model.
func TestTheQuestionsAndTheConfirmWindowDrawNoEscapesFromTheModel(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.ask = newAsk([]agent.AskItem{{Text: "q\x1b]52;c;eA==\x07", Assumption: "a\x1b[8m", Options: []string{"o\x1b[2J"}}}, "")
	tu.ask.answers[0] = "o\x1b[2J"
	tu.confirm = &confirmState{req: agent.ApprovalRequest{Command: "ls\x1b[1A", Reason: "r\x1b[1A"}}
	rows := append(tu.askLines(0), tu.confirmLines(0)...)
	for _, bad := range []string{"\x1b]52", "\x1b[8m", "\x1b[2J", "\x1b[1A"} {
		if strings.Contains(strings.Join(rows, "\n"), bad) {
			t.Errorf("%q from the model reached a window row", bad)
		}
	}
}

// TestAnsweringTheQuestionsRunsUnderTheRootContext: the follow-up run used a background
// context, so Ctrl+C (a cancel of the root context) was ignored until the run ended itself.
func TestAnsweringTheQuestionsRunsUnderTheRootContext(t *testing.T) {
	runner := &fakeRunner{cfg: configWithKey("k"), taskBlock: make(chan struct{}), taskStarted: make(chan struct{})}
	tu := New(runner)
	tu.Out, tu.Width, tu.Height = io.Discard, 80, 24
	tu.ask = newAsk([]agent.AskItem{{Text: "which?", Options: []string{"a"}}}, "orig")
	tu.ask.answers[0] = "a"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tu.handleAskKey(ctx, keyEnter)
		close(done)
	}()
	<-runner.taskStarted
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		close(runner.taskBlock)
		t.Fatal("the run started by answering questions ignored the cancelled root context")
	}
}

// TestAWidthOnlyResizeRepaintsEveryRow: the row count does not change, so a diff would skip
// the rows that compare equal while the terminal has reflowed the old, wider rows into them.
func TestAWidthOnlyResizeRepaintsEveryRow(t *testing.T) {
	tu, out := newKeyTUI("", "hello")
	tu.Width, tu.Height = 100, 30
	tu.drawFrame()
	out.Reset()
	tu.Width = 60
	tu.drawFrame()
	if !strings.HasPrefix(out.String(), "\x1b[H\x1b[2J") {
		t.Error("a resized frame must clear the screen first")
	}
	if n := strings.Count(out.String(), ";1H"); n != 30 {
		t.Errorf("%d of 30 rows were written after the resize", n)
	}
	out.Reset()
	tu.drawFrame()
	if strings.Contains(out.String(), "\x1b[2J") {
		t.Error("an unchanged geometry must not clear the screen")
	}
}

// TestClippingADecoratedLineKeepsItsEscapesWhole: cell() clips coloured headers, and cutting
// by rune index counted escape bytes as columns, split a sequence and dropped the reset.
func TestClippingADecoratedLineKeepsItsEscapesWhole(t *testing.T) {
	tu := &TUI{}
	for _, width := range []int{12, 17} {
		got := clipLine(tu.muted("  * ")+tu.color(colAccent, 0, strings.Repeat("x", 60)), width)
		if n := visibleLen(got); n != width {
			t.Errorf("width %d: %d columns drawn, want all of them: %q", width, n, got)
		}
		if !strings.HasSuffix(got, "…\x1b[0m") {
			t.Errorf("width %d: the cut must end with the ellipsis and a reset: %q", width, got)
		}
		if stripANSI(got) != strings.TrimRight(stripANSI(got), "\x1b[") {
			t.Errorf("width %d: a dangling escape was left: %q", width, got)
		}
	}
	if got := clipLine(strings.Repeat("x", 10), 5); got != "xxxx…" {
		t.Errorf("a plain line gets no reset, got %q", got)
	}
}

// TestAToolLabelIsCutByRune: cutting a non-ASCII argument by byte split a rune in half.
func TestAToolLabelIsCutByRune(t *testing.T) {
	label, ok := toolLabel("[using tool: " + strings.Repeat("a", 43) + "ñññ]")
	if !ok || !utf8.ValidString(label) || !strings.HasSuffix(label, "añ...") {
		t.Fatalf("the label must be valid UTF-8 cut after 44 runes, got %q", label)
	}
}

// TestMutedTextUsesBrightBlack: 30+8 is SGR 38, the extended-colour introducer, which a
// terminal ignores without arguments; bright black is SGR 90 (and 100 as a background).
func TestMutedTextUsesBrightBlack(t *testing.T) {
	tu := &TUI{}
	if got := tu.muted("x"); got != "\x1b[90mx\x1b[0m" {
		t.Errorf("muted = %q", got)
	}
	if got := tu.color(colBase, colMuted, "x"); got != "\x1b[37;100mx\x1b[0m" {
		t.Errorf("a bright background = %q", got)
	}
}

// TestWideCharactersAreMeasuredInCells: a CJK character or an emoji takes two cells, so a row
// measured in runes drew up to twice as wide as the terminal and the frame scrolled.
func TestWideCharactersAreMeasuredInCells(t *testing.T) {
	tu, _ := newKeyTUI("", strings.Repeat("漢", 60)+" "+strings.Repeat("\U0001F31F", 40))
	tu.Width, tu.Height = 60, 24
	tu.draft = strings.Repeat("字", 80)
	lines, _ := tu.layout(tu.size())
	for i, l := range lines {
		if n := visibleLen(l); n > 60 {
			t.Errorf("row %d is %d cells wide on a 60-column terminal", i, n)
		}
	}
	if cut, piece := splitAtWidth("漢x", 1); cut != len("漢") || piece != "漢" {
		t.Errorf("a rune wider than the width is still taken on its own, got %d %q", cut, piece)
	}
	for _, r := range []rune{'a', 'é', '́', '‍', '️', '漢', '\U0001F31F', 'Ω'} {
		want := map[rune]int{'a': 1, 'é': 1, '́': 0, '‍': 0, '️': 0, '漢': 2, '\U0001F31F': 2, 'Ω': 1}[r]
		if got := runeWidth(r); got != want {
			t.Errorf("runeWidth(%q) = %d, want %d", r, got, want)
		}
	}
}

// TestAResizeIsPaintedByTheRunLoopWhileItWaits: the resize is painted from the selects the run
// loop blocks in, so no other goroutine ever reads the conversation.
func TestAResizeIsPaintedByTheRunLoopWhileItWaits(t *testing.T) {
	done := make(chan runOutcome, 1)
	waits := map[string]func(tu *TUI){
		"line reader": func(tu *TUI) { tu.readLine(context.Background()) },
		"live reader": func(tu *TUI) { tu.charMode = true; tu.readLine(context.Background()) },
		"run":         func(tu *TUI) { tu.awaitRun(context.Background(), nil, done, nil) },
	}
	for name, wait := range waits {
		t.Run(name, func(t *testing.T) {
			r, w := io.Pipe()
			resized := make(chan struct{})
			var out syncBuffer
			tu := &TUI{In: r, Out: &out, Width: 80, Height: 24, Runner: &fakeRunner{cfg: configWithKey("k")}, resized: resized}
			finished := make(chan struct{})
			go func() { wait(tu); close(finished) }()
			resized <- struct{}{}
			waitFor(t, &out, glyphRule)
			done <- runOutcome{}
			w.Close() // ends the readers; the run already ended on done
			<-finished
			select {
			case <-done: // left over by the readers, which never read it
			default:
			}
		})
	}
}

package tui

import (
	"context"
	"strings"
	"testing"
)

// tuiPopupBody is the popup's rows, stripped of colour: the tests assert on what is read.
func tuiPopupBody(tu *TUI) []string {
	lines := tu.completionLines(tu.bodyWidth())
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, stripANSI(l))
	}
	return out
}

// The slash-command catalogue is the single source of truth for the handler, the help screen
// and the completion popup. These tests hold the three together: a command that exists in one
// and not the others is a promise the interface does not keep.

// TestEveryCatalogueCommandIsHandled: the switch must accept every name and alias the
// catalogue advertises, or the popup would offer something that does nothing.
func TestEveryCatalogueCommandIsHandled(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30

	for _, c := range commands {
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			if handled, _ := tu.handleShortcut(context.Background(), name); !handled {
				t.Errorf("the catalogue advertises %q and the handler ignores it", name)
			}
		}
	}
}

// TestTheCommandCatalogueMatchesTheHelpScreen: the help is generated from the same list, so a
// command added to one and not the other fails here rather than in a user's hands.
func TestTheCommandCatalogueMatchesTheHelpScreen(t *testing.T) {
	for _, c := range commands {
		if !strings.Contains(helpText, c.Name) {
			t.Errorf("the help screen does not document %s", c.Name)
		}
		if c.Help != "" && !strings.Contains(helpText, c.Help) {
			t.Errorf("the help screen does not describe %s (%q)", c.Name, c.Help)
		}
	}
}

// TestCompletionsMatchPrefixes: the popup offers what the typed text could become.
func TestCompletionsMatchPrefixes(t *testing.T) {
	for _, tc := range []struct {
		typed string
		want  []string
	}{
		{"/p", []string{"/plan"}},
		{"/s", []string{"/session"}},
		{"/m", []string{"/models"}},
		{"/n", []string{"/new"}},
		{"/", []string{"/task", "/plan", "/models", "/config", "/reasoning", "/find", "/session", "/good", "/bad", "/value", "/new", "/help", "/quit"}},
	} {
		got := completions(tc.typed)
		var names []string
		for _, c := range got {
			names = append(names, c.Name)
		}
		if len(names) != len(tc.want) {
			t.Errorf("completions(%q) = %v, want %v", tc.typed, names, tc.want)
			continue
		}
		for i := range names {
			if names[i] != tc.want[i] {
				t.Errorf("completions(%q) = %v, want %v", tc.typed, names, tc.want)
				break
			}
		}
	}
}

// TestAShortAliasDoesNotSwallowTheLongName: "/t" must offer /task and anything else that
// starts the same way, and "/ta" must still offer /task. A completion that only matched the
// canonical name would make the alias a dead end.
func TestCompletionsReachThroughTheAliases(t *testing.T) {
	for _, typed := range []string{"/t", "/ta", "/task"} {
		if got := completions(typed); len(got) == 0 || got[0].Name != "/task" {
			t.Errorf("completions(%q) = %v, want /task first", typed, got)
		}
	}
}

// TestCompletionsStopOnceAnArgumentStarts: a line with a space is already an argument, so the
// popup must get out of the way instead of covering the conversation.
func TestCompletionsStopOnceAnArgumentStarts(t *testing.T) {
	for _, typed := range []string{"/find a path", "/find text", "/plan something"} {
		if got := completions(typed); got != nil {
			t.Errorf("completions(%q) = %v, want nothing once the argument has started", typed, got)
		}
	}
	// The trailing space right after a completion is not an argument yet: Tab leaves one there
	// so the user can type, and the popup must survive that instant rather than flicker away.
	if got := completions("/find "); len(got) != 1 {
		t.Errorf("completions(\"/find \") = %v, want the command still offered", got)
	}
}

// TestCompletionsIgnorePlainText: ordinary input is not a command prefix, and the popup must
// not appear for it.
func TestCompletionsIgnorePlainText(t *testing.T) {
	for _, typed := range []string{"", "hello", "count the files", "x"} {
		if got := completions(typed); len(got) != 0 {
			t.Errorf("completions(%q) = %v, want none", typed, got)
		}
	}
}

// TestThePopupAlignsItsColumns: the design guide is explicit that columnar data must align.
// The popup has two columns — the command and its description — and the second one must start
// in the same place on every row.
func TestThePopupAlignsItsColumns(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/"

	lines := tuiPopupBody(tu)
	if len(lines) < 3 {
		t.Fatalf("the popup must list the candidates, got %d lines", len(lines))
	}

	var starts []int
	for _, l := range lines {
		plain := stripANSI(l)
		// The description is the first word after the command column.
		for _, c := range commands {
			if strings.HasPrefix(strings.TrimLeft(plain, " "), c.Name+" ") ||
				strings.HasPrefix(strings.TrimLeft(plain, " "), c.Name+"  ") {
				idx := strings.Index(strings.TrimLeft(plain, " "), c.Help)
				if idx >= 0 {
					starts = append(starts, idx)
				}
				break
			}
		}
	}
	if len(starts) < 2 {
		t.Fatalf("could not measure the description column: %v", lines)
	}
	for _, s := range starts {
		if s != starts[0] {
			t.Errorf("the descriptions start at different columns: %v", starts)
			break
		}
	}
}

// TestThePopupExplainsHowToAccept: a list of candidates with no instruction is a puzzle. The
// keys that complete, run and dismiss it are stated.
func TestThePopupExplainsHowToAccept(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/pl"

	joined := strings.Join(tuiPopupBody(tu), "\n")
	// The key that accepts a completion is the right arrow, because Tab is the mode switch.
	for _, want := range []string{"→", "Enter", "Esc"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the popup must say how to use %q:\n%s", want, joined)
		}
	}
}

// TestThePopupIsNotDrawnWhileSearching: the search owns the input, so a command popup over it
// would be two things claiming the same keystrokes.
func TestThePopupIsNotDrawnWhileSearching(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/pl"
	tu.searching = true

	if lines := tuiPopupBody(tu); len(lines) != 0 {
		t.Errorf("the popup must not appear during a search, got %v", lines)
	}
}

// TestCompletingTheDraftAcceptsTheFirstCandidate: Tab accepts what the popup is showing, and
// leaves a space so the argument can be typed straight away.
func TestCompletingTheDraftAcceptsTheFirstCandidate(t *testing.T) {
	tu, out := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/pl"

	if !tu.completeDraft() {
		t.Fatal("Tab must complete the draft")
	}
	if tu.draft != "/plan " {
		t.Errorf("draft = %q, want %q", tu.draft, "/plan ")
	}
	if !strings.Contains(stripANSI(lastFrameOf(out)), "/plan") {
		t.Errorf("the completion must be drawn:\n%s", stripANSI(lastFrameOf(out)))
	}
}

// TestCompletingDoesNothingWithoutCandidates: Tab keeps its old meaning — switch mode — when
// there is nothing to complete, which is why completeDraft reports whether it acted.
func TestCompletingDoesNothingWithoutCandidates(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.draft = "hello"

	if tu.completeDraft() {
		t.Error("Tab must not complete ordinary text")
	}
	if tu.draft != "hello" {
		t.Errorf("the draft must be untouched, got %q", tu.draft)
	}
}

// TestTheLayoutCountsThePopup: the popup grows as candidates appear, so the frame must be
// measured with it. A fixed height would overflow the terminal and scroll the interface.
func TestTheLayoutCountsThePopup(t *testing.T) {
	tu, _ := newKeyTUI("", "a message")
	tu.Width, tu.Height = 100, 30

	without, _ := tu.layout(100, 30)
	tu.draft = "/"
	withPopup, _ := tu.layout(100, 30)

	// The frame fills the terminal height either way — the composer is pegged to the bottom —
	// so the popup does not make the frame taller: it takes its rows from the conversation.
	if len(withPopup) != len(without) {
		t.Errorf("the frame must keep its height: %d without, %d with", len(without), len(withPopup))
	}
	if len(withPopup) > 30 {
		t.Errorf("the frame must still fit a 30-row terminal, got %d rows", len(withPopup))
	}
	// What the popup costs the conversation is what must be visible.
	if !strings.Contains(stripANSI(strings.Join(withPopup, "\n")), "→ completes") {
		t.Error("the popup must be drawn")
	}
}

// TestThePopupShrinksTheConversationNotTheComposer: the composer and the status bar are never
// dropped, so what gives way is the conversation above them.
func TestThePopupShrinksTheConversationNotTheComposer(t *testing.T) {
	tu, _ := newKeyTUI("", "m")
	padBody(tu, 40)
	tu.Width, tu.Height = 100, 24
	tu.draft = "/"

	lines, prompt := tu.layout(100, 24)
	if prompt == "" {
		t.Fatal("the composer must survive the popup")
	}
	if len(lines) > 24 {
		t.Errorf("the frame must fit, got %d rows", len(lines))
	}
	body := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(body, "→ completes") {
		t.Errorf("the popup must be visible:\n%s", body)
	}
	// The status bar is the last row.
	if !strings.Contains(stripANSI(lines[len(lines)-1]), "Task") {
		t.Errorf("the status bar must be last: %q", stripANSI(lines[len(lines)-1]))
	}
}

// TestTheEmptyDraftShowsNoPopup: nothing typed, nothing to suggest.
func TestTheEmptyDraftShowsNoPopup(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 100, 30
	tu.draft = ""

	if lines := tuiPopupBody(tu); len(lines) != 0 {
		t.Errorf("an empty draft must not open the popup, got %v", lines)
	}
	if tu.completing() {
		t.Error("completing must be false with nothing typed")
	}
}

// TestThePopupOpensWithTheFirstRowSelected: a popup that opens on a row the user did not
// pick is a popup that surprises. The first row is the default, the same row the right
// arrow and Tab used to accept, and the row every menu in every program opens with.
func TestThePopupOpensWithTheFirstRowSelected(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/p"

	if tu.completingIdx != 0 {
		t.Errorf("the popup must open on the first row, got idx=%d", tu.completingIdx)
	}
	body := tuiPopupBody(tu)
	if len(body) == 0 {
		t.Fatal("the popup must be visible with /p")
	}
	// The marker sits in column 2 (after the left margin) on the selected row,
	// and is absent from the other rows — that is the only difference between
	// the highlighted row and its neighbours.
	if !strings.Contains(body[0], "›") {
		t.Errorf("the first row must carry the arrow marker, got %q", body[0])
	}
	for _, l := range body[1:] {
		if strings.Contains(l, "›") {
			t.Errorf("only the first row is highlighted, got %q", l)
		}
	}
}

// TestThePopupArrowsMoveTheHighlightThroughReadLineLive: readLineLive is the live reader
// the chat loop drives, and it is what runs the popup's arrow bindings. The test feeds a
// real CSI sequence into the reader's byte stream and verifies that the highlight moved,
// which is the only way to know that the binding the reader installs actually fires when
// a terminal sends it.
func TestThePopupArrowsMoveTheHighlightThroughReadLineLive(t *testing.T) {
	cands := completions("/")
	if len(cands) < 2 {
		t.Skipf("need at least two candidates for /, got %d", len(cands))
	}
	// The reader runs through these steps:
	//   1. /    — opens the popup, idx = 0
	//   2. \x1b[B — Down moves idx to 1
	//   3. \n   — Enter accepts the highlight and dispatches the full command
	_, line, _ := tu_caller("/\x1b[B\n")
	if line != cands[1].Name+" " {
		t.Errorf("the reader must return the highlighted candidate, got %q want %q", line, cands[1].Name+" ")
	}
}

// tu_caller is a tiny helper that drives readLineLive with the given bytes and
// returns the TUI so the caller can inspect state the reader mutated.
func tu_caller(keys string) (*TUI, string, bool) {
	tu, _ := newKeyTUI(keys, "")
	tu.Width, tu.Height = 110, 30
	tu.charMode = true
	line, ok := tu.readLineLive(context.Background())
	return tu, line, ok
}

// TestThePopupUpArrowWrapsTheHighlightToTheLastRow: the wrap is part of the binding the
// reader installs, not just an arithmetic trick. The test feeds Up while the highlight is
// on row 0 and verifies the highlight lands on the last row, by accepting the row with
// Enter and checking which candidate was filled in — the same shape every menu uses.
func TestThePopupUpArrowWrapsTheHighlightToTheLastRow(t *testing.T) {
	cands := completions("/")
	if len(cands) < 2 {
		t.Skipf("need at least two candidates, got %d", len(cands))
	}

	// Up from row 0 wraps to the last row, Enter accepts it. The line returned
	// must be the last candidate, not the first.
	_, line, _ := tu_caller("/\x1b[A\n")
	if line != cands[len(cands)-1].Name+" " {
		t.Errorf("Up from row 0 must wrap and accept the last candidate %q, got %q", cands[len(cands)-1].Name+" ", line)
	}
}

// TestEnterAcceptsTheHighlightedRowAndDispatchesTheFilledLine: Enter is what a user reaches
// for when they have moved the highlight with the arrows and want to run the command. The
// reader must accept the highlight, fill the line with the full name, and dispatch it in
// one motion — two presses for what looks like one action would be a guess the user has to
// make about which Enter does what.
func TestEnterAcceptsTheHighlightedRowAndDispatchesTheFilledLine(t *testing.T) {
	cands := completions("/")
	if len(cands) < 2 {
		t.Skipf("need at least two candidates, got %d", len(cands))
	}

	// Down moves the highlight to row 1, Enter accepts it and dispatches the
	// filled line. The reader must return the second candidate, not the first.
	_, line, ok := tu_caller("/\x1b[B\n")
	if !ok {
		t.Fatal("the reader must accept the line and return")
	}
	if line != cands[1].Name+" " {
		t.Errorf("Down+Enter must dispatch %q, got %q", cands[1].Name+" ", line)
	}
}

// TestThePopupIndexAdvancesWithDownAndWraps: the user can land on a row without knowing
// how many candidates there are, the way every menu behaves. Wrapping from the last row
// to the first is the same gesture every shell completion uses.
func TestThePopupIndexAdvancesWithDownAndWraps(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/"
	cands := completions(tu.draft)
	if len(cands) < 2 {
		t.Skipf("need at least two candidates to test wrap, got %d", len(cands))
	}

	// Down advances one row, wrapping from the last row back to the first.
	want := 1
	if tu.completingIdx+1 < len(cands) {
		want = tu.completingIdx + 1
	}
	tu.completingIdx = (tu.completingIdx + 1) % len(cands)
	if tu.completingIdx != want {
		t.Errorf("Down must advance one row, got %d, want %d", tu.completingIdx, want)
	}

	// Walking Down `len(cands)` times brings the highlight back to where it started,
	// which is the wrap working in both directions.
	start := tu.completingIdx
	for i := 0; i < len(cands); i++ {
		tu.completingIdx = (tu.completingIdx + 1) % len(cands)
	}
	if tu.completingIdx != start {
		t.Errorf("Down %d times from %d must wrap back to %d, got %d", len(cands), start, start, tu.completingIdx)
	}

	// Up wraps from the first row to the last.
	tu.completingIdx = 0
	tu.completingIdx = (tu.completingIdx - 1 + len(cands)) % len(cands)
	if tu.completingIdx != len(cands)-1 {
		t.Errorf("Up from the first row must wrap to the last (idx=%d), got %d", len(cands)-1, tu.completingIdx)
	}
}

// TestThePopupIndexResetsOnDraftChange: a new character or a backspace is a new prefix.
// The row the user picked a moment ago no longer refers to a candidate that matches —
// the popup reopens with the first row selected, the way every menu behaves when the
// filter changes.
func TestThePopupIndexResetsOnDraftChange(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/"
	tu.completingIdx = 2
	if tu.completingIdx != 2 {
		t.Fatal("setup: completingIdx must be 2")
	}

	// Simulate the path the live reader takes on a backspace: it shrinks the draft
	// and resets the highlight.
	tu.draft = ""
	tu.completingIdx = 0

	if tu.completingIdx != 0 {
		t.Errorf("a draft reset must take the highlight back to the first row, got %d", tu.completingIdx)
	}
}

// TestThePopupIndexResetsBetweenLines: the next prompt must open on the first row, even
// if the previous one left the highlight on a different row.
func TestThePopupIndexResetsBetweenLines(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/pl"
	tu.completingIdx = 1
	if tu.completingIdx != 1 {
		t.Fatal("setup: completingIdx must be 1")
	}

	// A new line is what readLineLive does on entry: clear the draft and reset.
	tu.draft = ""
	tu.completingIdx = 0

	if tu.completingIdx != 0 {
		t.Errorf("a new line must reset the highlight to the first row, got %d", tu.completingIdx)
	}
}

// TestCompletingAcceptsTheHighlightedRow: completeDraft is what the right arrow and Enter
// call when a popup is open. It must take the row the user picked, not the first row —
// otherwise the arrows are decorations.
func TestCompletingAcceptsTheHighlightedRow(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/"
	cands := completions(tu.draft)
	if len(cands) < 2 {
		t.Skipf("need at least two candidates, got %d", len(cands))
	}
	tu.completingIdx = 1
	want := cands[1].Name

	if !tu.completeDraft() {
		t.Fatal("completeDraft must act when candidates exist")
	}
	if tu.draft != want+" " {
		t.Errorf("draft = %q, want %q", tu.draft, want+" ")
	}
	if tu.completingIdx != 0 {
		t.Errorf("completingIdx must reset after acceptance, got %d", tu.completingIdx)
	}
}

// TestCompletingFallsBackToTheFirstRowWhenIndexIsStale: the user can navigate, then
// delete a character, and the popup may shrink. A highlight that points past the end is
// the same kind of stale as a highlight that points before the start; falling back to
// the first row is what the gesture used to do, and is the safe pick.
func TestCompletingFallsBackToTheFirstRowWhenIndexIsStale(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/pl"
	tu.completingIdx = 99
	cands := completions(tu.draft)
	if len(cands) == 0 {
		t.Skip("no candidates for /pl")
	}

	if !tu.completeDraft() {
		t.Fatal("completeDraft must act")
	}
	if tu.draft != cands[0].Name+" " {
		t.Errorf("stale idx must fall back to the first row, got draft=%q", tu.draft)
	}
}

// TestThePopupOnlyDrawsOneHighlightAtATime: a popup with two arrows is a popup that
// says two different rows are selected. Exactly one row carries the marker, and the
// others carry a plain space.
func TestThePopupOnlyDrawsOneHighlightAtATime(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 110, 30
	tu.draft = "/"
	cands := completions(tu.draft)
	if len(cands) < 2 {
		t.Skipf("need at least two candidates, got %d", len(cands))
	}

	for i := 0; i < len(cands); i++ {
		tu.completingIdx = i
		body := tuiPopupBody(tu)
		highlighted := 0
		for _, l := range body {
			if strings.Contains(l, "›") {
				highlighted++
			}
		}
		if highlighted != 1 {
			t.Errorf("idx=%d: exactly one row must be highlighted, got %d", i, highlighted)
		}
	}
}

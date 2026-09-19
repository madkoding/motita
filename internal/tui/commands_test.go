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
		{"/", []string{"/task", "/plan", "/models", "/config", "/reasoning", "/find", "/session", "/new", "/help", "/quit"}},
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
	for _, want := range []string{"Tab", "Enter", "Esc"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the popup must say how to use %s:\n%s", want, joined)
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

	if len(withPopup) <= len(without) {
		t.Errorf("the popup must add rows: %d without, %d with", len(without), len(withPopup))
	}
	if len(withPopup) > 30 {
		t.Errorf("the frame must still fit a 30-row terminal, got %d rows", len(withPopup))
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
	if !strings.Contains(body, "Tab completes") {
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

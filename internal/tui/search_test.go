package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Searching the conversation. The guide asks for live filtering with the match count
// and the matches highlighted, and for an escape route out of every state.

// TestSearchFiltersTheConversation: a query narrows what is drawn, and clearing it brings
// the whole history back. Hiding the user's own conversation with no way back would be worse
// than not having the feature.
func TestSearchFiltersTheConversation(t *testing.T) {
	tu, out := newKeyTUI("", "alpha line", "beta line", "gamma line")
	tu.Width, tu.Height = 100, 30

	tu.openSearch()
	if !tu.searching {
		t.Fatal("Ctrl+F must open the search")
	}
	tu.applyQuery("beta")

	// The assertions are on the LAST frame: the buffer keeps every repaint, so the whole
	// string still contains the history from before the search.
	body := stripANSI(lastFrameOf(out))
	if !strings.Contains(body, "beta line") {
		t.Errorf("the match must be shown:\n%s", body)
	}
	if strings.Contains(body, "alpha line") || strings.Contains(body, "gamma line") {
		t.Errorf("non-matching lines must be filtered out:\n%s", body)
	}
	// The filter is visible while it is being typed: the status line and the composer both
	// name it, so the user knows why the history looks short.
	if !strings.Contains(body, `"beta"`) {
		t.Errorf("an applied filter must announce itself:\n%s", body)
	}

	tu.closeSearch()
	body = stripANSI(lastFrameOf(out))
	for _, want := range []string{"alpha line", "beta line", "gamma line"} {
		if !strings.Contains(body, want) {
			t.Errorf("clearing the search must restore %q:\n%s", want, body)
		}
	}
	if tu.query != "" || tu.searching {
		t.Errorf("Esc must leave no filter behind, got query=%q searching=%v", tu.query, tu.searching)
	}
}

// TestSearchIsCaseInsensitiveAndHighlights: the match is located on a lowercased copy
// but the ORIGINAL text is emitted — coluring a lowercased copy would rewrite what the
// agent said, which is the kind of silent corruption nobody forgives.
func TestSearchIsCaseInsensitiveAndHighlights(t *testing.T) {
	tu, out := newKeyTUI("", "The GO version is 1.27")
	tu.Width, tu.Height = 100, 30

	tu.openSearch()
	tu.applyQuery("go version")

	// This test reads the STREAM, not the screen: a colour is not a cell, so the cell-level
	// emulator cannot see it. Both questions are real and they need different views — what the
	// text says, and how it is emphasised.
	raw := out.String()
	// The original casing survives.
	if !strings.Contains(stripANSI(raw), "GO version") {
		t.Errorf("the original text must be preserved:\n%s", stripANSI(raw))
	}
	// And the match is emphasised, which is what "highlight matches" means.
	if !strings.Contains(raw, "\x1b[30;46m") {
		t.Errorf("the match must be highlighted:\n%q", raw)
	}
	// The row is still on the screen, which is the other half: highlighting a row that is not
	// displayed would emphasise nothing.
	if !strings.Contains(lastFrameOf(out), "GO version") {
		t.Errorf("the highlighted line must be on screen:\n%s", lastFrameOf(out))
	}
}

// TestSearchThatMatchesNothingExplainsItself: a filter with no hits is a dead end
// unless the screen says what was searched and how to leave. The guide's empty-state
// rule is an explanation plus an action.
func TestSearchThatMatchesNothingExplainsItself(t *testing.T) {
	tu, out := newKeyTUI("", "one", "two")
	tu.Width, tu.Height = 100, 30

	tu.openSearch()
	tu.applyQuery("nothing matches this")

	body := stripANSI(lastFrameOf(out))
	if !strings.Contains(body, "No line matches") {
		t.Errorf("an empty result must say so:\n%s", body)
	}
	if !strings.Contains(body, "Esc") || !strings.Contains(body, "Ctrl+F") {
		t.Errorf("the empty result must offer the way out and the way to retry:\n%s", body)
	}
}

// TestTheSearchOwnsTheInputWhileItIsOpen: a line typed during a search is a query, not
// a task. Running it would start a turn nobody asked for, from inside a filter box.
func TestTheSearchOwnsTheInputWhileItIsOpen(t *testing.T) {
	runner := &fakeRunner{cfg: configWithKey("k"), cfgSet: true}
	tu, _ := newKeyTUI("", "")
	tu.Runner = runner
	tu.Width, tu.Height = 100, 30

	// Ctrl+F, then a line, then an empty line to close the search, then quit.
	tu.In = strings.NewReader("\x06alpha\n\nq\n")
	tu.Run(context.Background())

	runner.mu.Lock()
	called := runner.taskCalled
	last := runner.lastTask
	runner.mu.Unlock()
	if called {
		t.Errorf("the search must not run anything, but a task was started: %q", last)
	}
	if tu.query != "alpha" {
		t.Errorf("query = %q, want alpha", tu.query)
	}
}

// TestAnEmptyQueryKeepsTheFilter: pressing Enter on an empty line confirms the filter
// as it stands rather than clearing it. Esc is the way to clear.
func TestAnEmptyQueryKeepsTheFilter(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two")
	tu.Width, tu.Height = 100, 30

	tu.openSearch()
	tu.applyQuery("two")
	tu.applyQuery("")

	if tu.query != "two" {
		t.Errorf("an empty line must keep the filter, got %q", tu.query)
	}
	if tu.searching {
		t.Error("an empty line must close the search box")
	}
}

// TestEscapeLeavesTheSearchBeforeAnythingElse: Esc unwinds in the reverse order of how
// the interface was entered — the search first, then a run, then the scroll.
func TestEscapeLeavesTheSearchBeforeAnythingElse(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	tu.Width, tu.Height = 100, 30

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tu.cancelRun = cancel
	tu.runningCtx = runCtx

	tu.openSearch()
	tu.applyQuery("one")

	handled, quit := tu.handleShortcut(context.Background(), keyEsc)
	if !handled || quit {
		t.Fatalf("Esc must be handled without quitting (handled=%v quit=%v)", handled, quit)
	}
	if tu.query != "" {
		t.Error("Esc must have left the search")
	}
	if runCtx.Err() != nil {
		t.Error("Esc must not reach the run while a search is open: leave in reverse order")
	}
}

// TestTheSearchKeyIsReachableAndRepeats: Ctrl+F opens the search, and opens it again
// from a filter that is already applied, which is how a user edits a query.
func TestTheSearchKeyIsReachableAndRepeats(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	tu.Width, tu.Height = 100, 30

	if handled, _ := tu.handleShortcut(context.Background(), keyFind); !handled {
		t.Fatal("Ctrl+F must be handled")
	}
	if !tu.searching {
		t.Fatal("Ctrl+F must open the search")
	}

	// A line with text is the query itself; the box stays open so it can be refined.
	tu.applyQuery("one")
	if !tu.searching {
		t.Fatal("typing a query must leave the box open for editing")
	}
	// An empty line confirms the filter and closes the box.
	tu.applyQuery("")
	if tu.searching {
		t.Fatal("an empty line must close the box")
	}
	// And the key opens it again, which is how the applied filter is edited.
	if handled, _ := tu.handleShortcut(context.Background(), keyFind); !handled {
		t.Fatal("Ctrl+F must work again with a filter applied")
	}
	if !tu.searching {
		t.Error("Ctrl+F must reopen the search for editing")
	}
}

// TestTheFilterCountMatchesWhatIsDrawn: the number in the prompt is an assertion the
// user reads, so it has to agree with the lines on screen.
func TestTheFilterCountMatchesWhatIsDrawn(t *testing.T) {
	tu, out := newKeyTUI("", "match one", "other", "match two")
	tu.Width, tu.Height = 100, 30

	tu.query = "match"
	tu.drawFrame()
	matching := tu.matchingMessages()
	if len(matching) != 2 {
		t.Fatalf("matchingMessages = %d, want 2", len(matching))
	}
	// The count is drawn by the composer, so the assertion goes through the frame the user
	// reads rather than through a second renderer that could drift from it.
	painted := stripANSI(lastFrameOf(out))
	if !strings.Contains(painted, "2 lines") {
		t.Errorf("the composer must report the count it has:\n%s", painted)
	}
	if !strings.Contains(painted, "match") {
		t.Errorf("the composer must show the applied filter:\n%s", painted)
	}
}

// TestTheSearchFindsTheSpeakerToo: "who said this" is a reasonable question, so the
// author is searched as well as the text.
func TestTheSearchFindsTheSpeakerToo(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 100, 30
	tu.messages = []Message{
		{Author: AuthorUser, Text: "hello"},
		{Author: AuthorAgent, Text: "hi there"},
	}

	tu.query = "you"
	got := tu.matchingMessages()
	if len(got) != 1 || got[0].Author != AuthorUser {
		t.Errorf("searching the speaker must find the user's turn, got %+v", got)
	}
}

// lastFrameOf returns what the terminal is SHOWING after the captured output, as text.
//
// Same reasoning as lastFrame: once a repaint writes only the rows that changed, the raw
// bytes are no longer a picture of the screen. The calls that used to search this string for
// a word were searching the whole session's output when they meant the visible rows.
//
// It is variadic and ignores its arguments so the existing call sites keep working while the
// signature stays honest about not needing them.
func lastFrameOf(out *bytes.Buffer, _ ...int) string {
	s := out.String()
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	// The size the tests use; the emulator clamps anything written outside it.
	const w, h = 200, 60
	sc := newScreen(w, h)
	sc.feed(s)
	return sc.text()
}

// TestHighlightLeavesNonMatchingLinesAlone: a filtered conversation can contain a line
// that matched on the SPEAKER and not on its text. The highlighter must then colour the
// line plainly instead of searching for a substring that is not there.
func TestHighlightLeavesNonMatchingLinesAlone(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.query = "absent"

	got := tu.highlight("a line without it", colBase)
	if strings.Contains(got, "\x1b[30;46m") {
		t.Errorf("a line with no match must not be highlighted: %q", got)
	}
	if stripANSI(got) != "a line without it" {
		t.Errorf("the text must pass through unchanged, got %q", stripANSI(got))
	}
}

// TestHighlightWithNoQueryIsPlain: the highlighter is only reached while filtering, but
// an empty query must still be safe rather than matching everything at position zero.
func TestHighlightWithNoQueryIsPlain(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.query = ""

	got := tu.highlight("anything", colBase)
	if strings.Contains(got, "\x1b[30;46m") {
		t.Errorf("an empty query must not highlight: %q", got)
	}
	if stripANSI(got) != "anything" {
		t.Errorf("text = %q", stripANSI(got))
	}
}

// TestHighlightMarksEveryOccurrence: a line can contain the match more than once, and a
// highlighter that stops at the first one makes the rest of the line look like it did
// not match.
func TestHighlightMarksEveryOccurrence(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.query = "ab"

	got := tu.highlight("ab xx ab yy ab", colBase)
	if n := strings.Count(got, "\x1b[30;46m"); n != 3 {
		t.Errorf("expected 3 highlighted spans, got %d: %q", n, got)
	}
	if stripANSI(got) != "ab xx ab yy ab" {
		t.Errorf("the text must not be altered, got %q", stripANSI(got))
	}
}

// The typed form of the search. A terminal in canonical mode consumes the control bytes
// itself — Ctrl+U is the driver's kill-line, Ctrl+D its end-of-file, Ctrl+F is passed
// through only sometimes — so the shortcut alone would leave the feature unreachable on
// the target hardware. Measured on Uchikoma: Ctrl+F never arrived at the program.
func TestFindIsTheTypedSearch(t *testing.T) {
	tu, out := newKeyTUI("", "alpha line", "beta line")
	tu.Width, tu.Height = 100, 30

	handled, quit := tu.handleShortcut(context.Background(), "/find beta")
	if !handled || quit {
		t.Fatalf("/find must be handled without quitting (handled=%v quit=%v)", handled, quit)
	}
	if tu.query != "beta" {
		t.Errorf("query = %q, want beta", tu.query)
	}
	if tu.searching {
		t.Error("/find applies the filter and closes the box, so the next line is a task")
	}

	body := stripANSI(lastFrameOf(out))
	if strings.Contains(body, "alpha line") {
		t.Errorf("the filter must be applied immediately:\n%s", body)
	}
	if !strings.Contains(body, "beta line") {
		t.Errorf("the match must be kept:\n%s", body)
	}
}

// TestFindWithNoArgumentOpensTheSearchBox: "/find" alone is the typed way to reach the
// mode, so the user can then type the query with the live preview.
func TestFindWithNoArgumentOpensTheSearchBox(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	tu.Width, tu.Height = 100, 30

	if handled, _ := tu.handleShortcut(context.Background(), "/find"); !handled {
		t.Fatal("/find must be handled")
	}
	if !tu.searching {
		t.Error("/find with no argument must open the search box")
	}

	// The same guard reached directly, with an empty argument: applyFind is the one
	// place that decides, so the decision belongs to it rather than to the caller.
	tu2, _ := newKeyTUI("", "one")
	tu2.Width, tu2.Height = 100, 30
	tu2.applyFind("")
	if !tu2.searching {
		t.Error("an empty query must open the box instead of applying an empty filter")
	}
	if tu2.query != "" {
		t.Errorf("no filter must be installed, got %q", tu2.query)
	}
}

// TestFindClearsWithAnEmptyArgument: "/find" followed by nothing is how a user reaches
// the mode; to clear a filter they use Esc, which is the documented way out.
func TestFindCarriesTheRestOfTheLineAsTheQuery(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	tu.Width, tu.Height = 100, 30

	// The query keeps its internal spaces and loses only the surrounding ones.
	tu.handleShortcut(context.Background(), "/find   two words  ")
	if tu.query != "two words" {
		t.Errorf("query = %q, want %q", tu.query, "two words")
	}
}

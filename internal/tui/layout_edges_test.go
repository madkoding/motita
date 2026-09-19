package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/session"
)

// The edges of the new layout and the new input path: the branches a first pass through the
// feature does not reach, each of which is a real situation a user can be in.

// TestCompletionsWhenAnAliasIsTheOnlyMatch: a command reached by its alias must appear, or the
// alias would be a spelling the popup never offers.
func TestCompletionsWhenAnAliasIsTheOnlyMatch(t *testing.T) {
	// "/r" is an alias of /reasoning and the prefix of nothing else.
	got := completions("/r")
	if len(got) != 1 || got[0].Name != "/reasoning" {
		t.Errorf("completions(/r) = %v, want /reasoning", got)
	}
}

// TestCompletionsWithNoLeadingSlash: the function is given whatever the caller has, and a bare
// word must be treated as the start of a command.
func TestCompletionsWithNoLeadingSlash(t *testing.T) {
	got := completions("pl")
	if len(got) != 1 || got[0].Name != "/plan" {
		t.Errorf("completions(pl) = %v, want /plan", got)
	}
}

// TestThePopupDropsTheDescriptionWhenItDoesNotFit: on a narrow terminal the command itself is
// what must survive. A truncated description is worse than no description.
func TestThePopupDropsTheDescriptionWhenItDoesNotFit(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 44, 30
	tu.draft = "/"

	lines := tuiPopupBody(tu)
	if len(lines) == 0 {
		t.Fatal("the popup must still list the commands")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "/task") {
		t.Errorf("the command names must survive a narrow terminal:\n%s", joined)
	}
	// Whatever is drawn must fit: the frame must never overflow the window.
	for _, l := range lines {
		if visibleLen(l) > 44 {
			t.Errorf("a popup row overflows the terminal (%d cols): %q", visibleLen(l), l)
		}
	}
}

// TestTheContextLabelNeedsAWindow: with no session the figure would be a decoration computed
// from nothing, so the label is empty rather than invented.
func TestTheContextLabelNeedsAWindow(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	if got := tu.contextLabel(); got != "" {
		t.Errorf("contextLabel with no session = %q, want empty", got)
	}
}

// TestTheContextLabelShowsPercentageAndCounts: the percentage is what tells a user they are
// about to lose the earlier conversation; the counts are what make it trustworthy.
func TestTheContextLabelShowsPercentageAndCounts(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Runner.(*fakeRunner).snapshot = session.Snapshot{
		Model: "m", Window: 1000, Tokens: 250, Used: 0.25, Messages: 4,
	}

	got := tu.contextLabel()
	for _, want := range []string{"25%", "250", "1000"} {
		if !strings.Contains(got, want) {
			t.Errorf("contextLabel = %q, must contain %q", got, want)
		}
	}
}

// TestTheBottomBarKeepsTheModeAndContextWhenThereIsNoRoom: the two ends must survive a narrow
// terminal, and the key hints are what gives way.
func TestTheBottomBarKeepsTheModeAndContextWhenThereIsNoRoom(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Runner.(*fakeRunner).snapshot = session.Snapshot{Window: 1000, Tokens: 10, Used: 0.01}
	tu.Width, tu.Height = 30, 24

	got := stripANSI(tu.bottomBar(30))
	if !strings.Contains(got, "Task") {
		t.Errorf("the mode must survive: %q", got)
	}
	if !strings.Contains(got, "context") {
		t.Errorf("the context must survive: %q", got)
	}
	if visibleLen(got) > 30 {
		t.Errorf("the bar overflows the terminal (%d cols): %q", visibleLen(got), got)
	}
}

// TestTheBottomBarWithNoRoomAtAll: a terminal too narrow for even the mode must not produce a
// negative gap and a panic in strings.Repeat.
func TestTheBottomBarWithNoRoomAtAll(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Runner.(*fakeRunner).snapshot = session.Snapshot{Window: 1000, Tokens: 10, Used: 0.01}

	got := stripANSI(tu.bottomBar(2))
	if !strings.Contains(got, "Task") {
		t.Errorf("the mode is the last thing to go: %q", got)
	}
}

// TestScrollIsReportedInTheBottomBar: a user who has scrolled back needs to know they are not
// looking at the newest message, and how far back they are.
func TestScrollIsReportedInTheBottomBar(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two", "three")
	padBody(tu, 10)
	tu.scroll = 3

	got := stripANSI(tu.bottomBar(100))
	if !strings.Contains(got, "3") || !strings.Contains(got, "back") {
		t.Errorf("the scroll position must be reported: %q", got)
	}
}

// TestTheStatusLineFallsBackToTheDocumentedDefaults: an empty config still has a provider and
// a model worth naming, rather than two blank columns.
func TestTheStatusLineFallsBackToTheDocumentedDefaults(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Runner.(*fakeRunner).cfg = config.Config{}

	joined := stripANSI(strings.Join(tu.statusLines(100), "\n"))
	if !strings.Contains(joined, "openai") {
		t.Errorf("the default provider must be named: %q", joined)
	}
	// The config's own default model is the honest answer when the caller supplied none.
	if !strings.Contains(joined, "gpt-4o-mini") {
		t.Errorf("the default model must be named: %q", joined)
	}
}

// TestTheStatusLineNeverReportsTheKey: the user who reached a chat has a working key, and
// printing it — or its state — on every repaint is noise. This is asserted rather than
// assumed because it was a visible part of the old design.
func TestTheStatusLineNeverReportsTheKey(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	joined := stripANSI(strings.Join(tu.statusLines(100), "\n"))

	for _, unwanted := range []string{"key", "ready", "sk-"} {
		if strings.Contains(strings.ToLower(joined), unwanted) {
			t.Errorf("the status line must not mention %q: %q", unwanted, joined)
		}
	}
}

// TestTheComposerShowsTheSearchBar: the same row carries the search box and the prompt, and
// which one it is has to be visible.
func TestTheComposerShowsTheSearchBar(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.searching = true
	tu.query = "needle"
	// While the box is open the line being edited is the DRAFT, not the query: the query is
	// what was applied, and overwriting it as the user types is how an edit loses its subject.
	tu.draft = "needle"

	got := stripANSI(strings.Join(tu.composerLines(), "\n"))
	if !strings.Contains(got, "find") {
		t.Errorf("the search box must be drawn: %q", got)
	}
	if !strings.Contains(got, "needle") {
		t.Errorf("what is being typed must be visible: %q", got)
	}
}

// TestTheComposerShowsAnAppliedFilter: a filter that is applied while the box is closed must
// still be visible, or the user is reading a subset without knowing it.
func TestTheComposerShowsAnAppliedFilter(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.query = "applied"

	got := stripANSI(strings.Join(tu.composerLines(), "\n"))
	if !strings.Contains(got, "applied") {
		t.Errorf("an applied filter must be visible: %q", got)
	}
}

// TestBodyWidthHasAFloor: a terminal narrower than the margins must not produce a zero or
// negative width, which would reach strings.Repeat.
func TestBodyWidthHasAFloor(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 1, 5

	if got := tu.bodyWidth(); got < 1 {
		t.Errorf("bodyWidth = %d, want at least 1", got)
	}
}

// TestConversationWidthHasAFloor: same, for the width the conversation is wrapped to.
func TestConversationWidthHasAFloor(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 2, 5

	if got := tu.conversationWidth(); got < 1 {
		t.Errorf("conversationWidth = %d, want at least 1", got)
	}
}

// TestCellClampsRatherThanOverflowing: a row longer than the space it has must be clipped, not
// allowed to fill the last column — which is what makes a terminal wrap and the frame scroll.
func TestCellClampsRatherThanOverflowing(t *testing.T) {
	tu, _ := newKeyTUI("", "")

	got := stripANSI(tu.cell(strings.Repeat("x", 200), 40))
	if visibleLen(got) > 40 {
		t.Errorf("the row is %d columns, want at most 40", visibleLen(got))
	}
}

// TestPadLeavesASeparator: the help's two columns must not run together when the label is
// exactly as wide as the column.
func TestPadLeavesASeparator(t *testing.T) {
	if got := pad("exactly", 7); got != "exactly " {
		t.Errorf("pad(exactly, 7) = %q, want a separator space", got)
	}
	if got := pad("ab", 6); got != "ab    " {
		t.Errorf("pad(ab, 6) = %q", got)
	}
}

// The library the skill tools read and write.

// TestTheRunnerResolvesTheLibraryOnce: the directory does not change during a session, so
// resolving it per turn would be a filesystem call for nothing.
func TestTheRunnerResolvesTheLibraryOnce(t *testing.T) {
	r := &AppRunner{Cfg: config.Config{}}
	r.Cfg.Skills.Dir = t.TempDir()

	first := r.library()
	second := r.library()
	if first != second {
		t.Error("the library must be resolved once and kept")
	}
	if first.Dir != r.Cfg.Skills.Dir {
		t.Errorf("library dir = %q, want %q", first.Dir, r.Cfg.Skills.Dir)
	}
}

// TestTheLibraryDirHasADefault: an unset directory must not give a library rooted at the empty
// string, which would be the whole working directory.
func TestTheLibraryDirHasADefault(t *testing.T) {
	r := &AppRunner{Cfg: config.Config{}}

	got := r.library()
	if got.Dir != "skills" {
		t.Errorf("library dir = %q, want the default", got.Dir)
	}
}

// TestTheLibraryCapIsHonoured: the configured cap is the one the library enforces, so a stray
// large file cannot be pulled into the context as if it were a procedure.
func TestTheLibraryCapIsHonoured(t *testing.T) {
	r := &AppRunner{Cfg: config.Config{}}
	r.Cfg.Skills.Dir = t.TempDir()
	r.Cfg.Skills.MaxFileBytes = 128

	got := r.library()
	if got.MaxFileBytes != 128 {
		t.Errorf("cap = %d, want the configured 128", got.MaxFileBytes)
	}
}

// TestConversationSummaryWithNoSession: the status bar asks for figures before anything has
// been sent, and the honest answer is zeroes rather than a panic.
func TestConversationSummaryWithNoSession(t *testing.T) {
	r := &AppRunner{}

	got := r.ConversationSummary()
	if got.Window != 0 || got.Tokens != 0 {
		t.Errorf("with no session the figures must be zero, got %+v", got)
	}
}

// TestConversationSummaryReportsTheSession: once there is one, the figures are the session's.
func TestConversationSummaryReportsTheSession(t *testing.T) {
	r := &AppRunner{}
	r.session = session.New("gpt-4o", "system prompt", 1000)

	got := r.ConversationSummary()
	if got.Window != 1000 {
		t.Errorf("window = %d, want 1000", got.Window)
	}
	if got.Tokens <= 0 {
		t.Error("the tokens in use must be reported")
	}
}

// TestTheLibraryDirectoryIsCreatedLazily: building the runner must not touch the disk. A user
// who never asks the agent to save anything should have no skills directory appear.
func TestTheLibraryDirectoryIsCreatedLazily(t *testing.T) {
	dir := t.TempDir() + "/never"
	r := &AppRunner{Cfg: config.Config{}}
	r.Cfg.Skills.Dir = dir

	_ = r.library()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("resolving the library must not create the directory")
	}
}

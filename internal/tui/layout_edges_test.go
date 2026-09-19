package tui

import (
	"bytes"
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

// TestTheComposerIsPeggedToTheBottom: the input belongs at the foot of the window, where the
// eye and the cursor expect it. A short conversation used to end a few rows down and the input
// was drawn directly under it — near the top, with empty space below — which is the layout the
// user rejected. The frame must therefore fill the height it was given.
func TestTheComposerIsPeggedToTheBottom(t *testing.T) {
	tu, _ := newKeyTUI("", "one short answer")
	tu.Width, tu.Height = 100, 24

	lines, prompt := tu.layout(100, 24)
	if len(lines) != 24 {
		t.Fatalf("the frame must fill the 24-row terminal, got %d rows", len(lines))
	}
	if prompt == "" {
		t.Fatal("the composer must be drawn")
	}
	// The composer is the second-to-last row, the rule above it, the status bar below.
	if got := stripANSI(lines[21]); !strings.Contains(got, "›") {
		t.Errorf("row 22 must be the composer, got %q", got)
	}
	if got := lines[22]; setOf(got) != "─" {
		t.Errorf("row 23 must be the rule, got %q", stripANSI(got))
	}
	if got := stripANSI(lines[23]); !strings.Contains(got, "Task") {
		t.Errorf("row 24 must be the status bar, got %q", got)
	}
	// And the blank space is ABOVE the composer, between the content and the controls.
	blanks := 0
	for _, l := range lines[11:21] {
		if strings.TrimSpace(stripANSI(l)) == "" {
			blanks++
		}
	}
	if blanks == 0 {
		t.Error("the gap between the conversation and the composer must be blank rows")
	}
}

// TestTheModeIsNamedExactlyOnce: the status bar reports the mode, so the composer must not
// repeat it. Printing it in both places put the same word on two rows of every single frame —
// "Task >" directly above a bar that said "Task".
func TestTheModeIsNamedExactlyOnce(t *testing.T) {
	tu, _ := newKeyTUI("", "an answer")
	tu.Width, tu.Height = 100, 24

	lines, _ := tu.layout(100, 24)
	count := 0
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "Task") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the mode must appear on exactly one row, found %d:\n%s",
			count, stripANSI(strings.Join(lines, "\n")))
	}

	// The same holds in Plan mode, which is where a copy would be easiest to miss.
	tu.screen = ScreenPlan
	lines, _ = tu.layout(100, 24)
	count = 0
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "Plan") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Plan must appear on exactly one row, found %d:\n%s",
			count, stripANSI(strings.Join(lines, "\n")))
	}
}

// setOf is the set of distinct non-space runes in a decorated line: how a divider is recognised
// without hard-coding its width.
func setOf(decorated string) string {
	seen := map[rune]bool{}
	for _, r := range stripANSI(decorated) {
		if r != ' ' {
			seen[r] = true
		}
	}
	out := ""
	for r := range seen {
		out += string(r)
	}
	return out
}

// TestTheFrameNeverWritesPastTheLastRow: the number of line breaks a frame writes must be one
// fewer than the rows it draws.
//
// A trailing newline moves the cursor DOWN a row. When the frame already fills the window that
// row does not exist, the terminal scrolls, and every repaint pushes the whole interface one
// line up — which is what a user sees as "lines being shoved upward" when switching mode with
// Tab. The break belongs BETWEEN rows, never after the last one.
//
// This only bites once the frame fills the height: while the frame was short the extra newline
// landed in the empty space below it. That is why it appeared only after the body was padded to
// the bottom of the window.
func TestTheFrameNeverWritesPastTheLastRow(t *testing.T) {
	for _, h := range []int{12, 16, 20, 24, 30} {
		for _, s := range []Screen{ScreenTask, ScreenPlan} {
			var out bytes.Buffer
			tu := New(&fakeRunner{cfg: configWithKey("k")})
			tu.In = strings.NewReader("")
			tu.Out = &out
			tu.Width, tu.Height = 100, h
			tu.screen = s
			tu.painted = true // skip the launch clear, which is not part of a repaint

			tu.drawFrame()

			lines, _ := tu.layout(100, h)
			if len(lines) != h {
				t.Fatalf("h=%d screen=%v: the frame must fill the height, got %d rows", h, s, len(lines))
			}
			breaks := strings.Count(out.String(), "\n")
			if breaks >= len(lines) {
				t.Errorf("h=%d screen=%v: %d breaks for %d rows, so the cursor lands on row %d of %d and the terminal scrolls",
					h, s, breaks, len(lines), breaks+1, h)
			}
		}
	}
}

// TestSwitchingModeDoesNotChangeTheFrameHeight: Tab must not resize the frame. Two screens of
// different heights would make the interface jump every time it is pressed, even if neither
// scrolled.
func TestSwitchingModeDoesNotChangeTheFrameHeight(t *testing.T) {
	tu, _ := newKeyTUI("", "one short answer")
	tu.Width, tu.Height = 100, 20

	tu.screen = ScreenTask
	task, _ := tu.layout(100, 20)
	tu.nextScreen()
	plan, _ := tu.layout(100, 20)
	tu.nextScreen()
	back, _ := tu.layout(100, 20)

	if len(task) != len(plan) || len(plan) != len(back) {
		t.Errorf("the frame changes height when the mode does: %d -> %d -> %d",
			len(task), len(plan), len(back))
	}
}

// TestTheFrameNeverExceedsTheTerminalWithThePopupOpen: the completion popup is the one part that
// GROWS WHILE BEING USED — every keystroke can add candidates — so it is where a frame can pass
// the bottom of the window. A frame taller than the terminal scrolls, and the interface slides
// upward as the user types: the whole screen jumps on a keypress, which is what makes the input
// look like it is being shoved off the screen.
//
// This is a strict invariant, not a preference: drawing more rows than the terminal has is
// always wrong, whatever is on them.
func TestTheFrameNeverExceedsTheTerminalWithThePopupOpen(t *testing.T) {
	for _, h := range []int{minHeight, 12, 16, 18, 20, 24, 30, 40} {
		for _, draft := range []string{"", "/", "/p", "/pl", "/plan", "hello"} {
			tu, _ := newKeyTUI("", "an answer")
			tu.Width, tu.Height = 90, h
			tu.draft = draft

			lines, _ := tu.layout(90, h)
			if len(lines) > h {
				t.Errorf("h=%d draft=%q: the frame is %d rows in a %d-row terminal",
					h, draft, len(lines), h)
			}
		}
	}
}

// TestThePopupIsCappedRatherThanPushingTheFrameOff: with a terminal too short for the full
// candidate list, the popup shrinks and SAYS SO. A silently truncated menu looks like the
// complete one, so the user would never know to keep typing.
func TestThePopupIsCappedRatherThanPushingTheFrameOff(t *testing.T) {
	tu, _ := newKeyTUI("", "an answer")
	tu.Width, tu.Height = 90, 14
	tu.draft = "/" // every command is a candidate: more than a 14-row terminal can show beside
	// the conversation, the composer, the rules and the status bar.

	lines, _ := tu.layout(90, 14)
	if len(lines) > 14 {
		t.Fatalf("the frame must fit, got %d rows", len(lines))
	}
	body := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(body, "more") {
		t.Errorf("a cut list must say how many are hidden:\n%s", body)
	}
	// The composer and the bar must both still be there: the popup gives way, not them.
	if !strings.Contains(body, "›") {
		t.Error("the composer must survive a capped popup")
	}
	if !strings.Contains(body, "Task") {
		t.Error("the status bar must survive a capped popup")
	}
}

// TestTheCappedPopupStillOffersSomething: a cap of one row must not leave the popup empty. An
// empty popup with a "and N more" line would be worse than useless.
func TestTheCappedPopupStillOffersSomething(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 90, 30
	tu.draft = "/"

	for _, cap := range []int{0, 1, 2, 3, 5} {
		lines := tu.completionLinesCapped(90, cap)
		if cap > 0 && len(lines) > cap {
			t.Errorf("cap %d drew %d rows", cap, len(lines))
		}
		// Whatever the cap, at least one real command has to be offered.
		body := stripANSI(strings.Join(lines, "\n"))
		offered := false
		for _, c := range commands {
			if strings.Contains(body, c.Name) {
				offered = true
				break
			}
		}
		if !offered {
			t.Errorf("cap %d offered no command:\n%s", cap, body)
		}
	}
}

// TestThePopupCapHasAFloor: the cap arithmetic can compute a budget of zero — a popup capped to
// nothing — and a popup that draws no rows is not a popup. The floor of one is what keeps the
// suggestion visible in a terminal that has room for exactly one row of it.
func TestThePopupCapHasAFloor(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 90, 30
	tu.draft = "/"

	// A cap of one must still draw the first candidate, which is the one the user is most
	// likely to want.
	lines := tu.completionLinesCapped(90, 1)
	if len(lines) != 1 {
		t.Fatalf("a cap of 1 must draw exactly one row, got %d: %v", len(lines), lines)
	}
	if body := stripANSI(lines[0]); !strings.Contains(body, "/task") {
		t.Errorf("the first candidate must be the one shown, got %q", body)
	}
}

// TestTheConversationRoomHasAFloor: `room` is a residue, and on a terminal that cannot hold even
// the permanent rows it comes out at zero or negative. A negative width or a negative row count
// would reach strings.Repeat and panic, so the floor is what turns an impossible window into a
// merely ugly one.
func TestTheConversationRoomHasAFloor(t *testing.T) {
	// A terminal tall enough for the gate but with the popup asking for more than exists, which
	// is how the residue goes to zero.
	for _, h := range []int{minHeight, minHeight + 1, minHeight + 2} {
		tu, _ := newKeyTUI("", "an answer")
		tu.Width, tu.Height = 44, h
		tu.draft = "/"

		lines, _ := tu.layout(44, h)
		if len(lines) == 0 {
			t.Errorf("h=%d: the layout drew nothing", h)
		}
		// Every row must be drawable: a negative or zero width would have panicked already.
		for i, l := range lines {
			if visibleLen(l) > 44 {
				t.Errorf("h=%d row %d is %d columns wide in a 44-column terminal", h, i, visibleLen(l))
			}
		}
	}
}

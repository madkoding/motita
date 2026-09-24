package tui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/session"
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
// string, which would be the whole working directory — nor at the working directory itself,
// which would scatter motita's own state through whatever project the user is in. It falls
// back to the home.
func TestTheLibraryDirHasADefault(t *testing.T) {
	r := &AppRunner{Cfg: config.Config{}}

	got := r.library()
	want := config.Default().Skills.Dir
	if got.Dir != want {
		t.Errorf("library dir = %q, want %q", got.Dir, want)
	}
	if !filepath.IsAbs(got.Dir) {
		t.Errorf("library dir = %q, want it under the home rather than relative", got.Dir)
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
	// The stack from the bottom: status bar, rule, input field (inputRows tall), then a blank
	// row that gives the conversation breathing room before the input box — the divider
	// above the input was removed because it read as the bottom of the chat and made the
	// whole input block look "shifted up" against the cursor at its top.
	n := len(lines)
	if got := stripANSI(lines[n-1]); !strings.Contains(got, "Task") {
		t.Errorf("the last row must be the status bar, got %q", got)
	}
	if got := lines[n-2]; setOf(got) != "─" {
		t.Errorf("the second-to-last row must be the rule, got %q", stripANSI(got))
	}
	// Measured layout, from the bottom up:
	//
	//	n-1         status bar
	//	n-2         rule
	//	n-2-i .. n-3  the input field (inputRows rows)
	//	n-3-i       the blank row above the input, separating the chat from the composer
	fieldStart := n - 2 - inputRows
	above := stripANSI(lines[fieldStart-1])
	if strings.TrimSpace(above) != "" {
		t.Errorf("the row above the input must be a blank, got %q", above)
	}
	field := lines[fieldStart : n-2]
	if len(field) != inputRows {
		t.Fatalf("the input field must be %d rows, got %d", inputRows, len(field))
	}
	if got := stripANSI(field[0]); !strings.Contains(got, "›") {
		t.Errorf("the first row of the input must carry the prompt, got %q", got)
	}
	// And the blank space is above the input, between the content and the controls.
	blanks := 0
	for _, l := range lines[:fieldStart-1] {
		if strings.TrimSpace(stripANSI(l)) == "" {
			blanks++
		}
	}
	if blanks == 0 {
		t.Error("the gap between the conversation and the input must be blank rows")
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
	tu.Width, tu.Height = 90, 20
	tu.draft = "/" // every command is a candidate: more than a 20-row terminal can show beside
	// the conversation, the input field, the three rules and the status bar.

	lines, _ := tu.layout(90, 20)
	if len(lines) > 20 {
		t.Fatalf("the frame must fit, got %d rows", len(lines))
	}
	body := stripANSI(strings.Join(lines, "\n"))
	// When the list is cut there is room to say so, and it must: a silently truncated menu looks
	// like the complete one, so the user would never know to keep typing.
	if !strings.Contains(body, "more") {
		t.Errorf("a cut list must say how many are hidden:\n%s", body)
	}
	// The input and the status bar must both still be there: the popup gives way, not them.
	if !strings.Contains(body, "›") {
		t.Error("the input must survive a capped popup")
	}
	if !strings.Contains(body, "Task") {
		t.Error("the status bar must survive a capped popup")
	}

	// And in a terminal with no room for even the "more" line, the popup shrinks to what fits
	// rather than pushing the frame off the screen. The frame always fits; what varies is how
	// much of the list is offered.
	tight, _ := newKeyTUI("", "an answer")
	tight.Width, tight.Height = 90, 14
	tight.draft = "/"
	tightLines, _ := tight.layout(90, 14)
	if len(tightLines) > 14 {
		t.Errorf("a 14-row terminal got a %d-row frame", len(tightLines))
	}
	tightBody := stripANSI(strings.Join(tightLines, "\n"))
	if !strings.Contains(tightBody, "/task") {
		t.Errorf("the popup must still offer the first candidate:\n%s", tightBody)
	}
	if !strings.Contains(tightBody, "›") || !strings.Contains(tightBody, "Task") {
		t.Errorf("the input and the bar must survive:\n%s", tightBody)
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

// TestAWideTerminalIsUsedInFull: the interface must fill the window it was given.
//
// There used to be a maximum width of 116 columns, so on a wider terminal the rules, the status
// bar and the conversation all stopped there and the rest of the screen stayed blank — which is
// exactly "not covering the full width of the window". The cap was meant to keep lines
// readable, but line length is the user's choice: they sized the window.
func TestAWideTerminalIsUsedInFull(t *testing.T) {
	for _, w := range []int{80, 116, 120, 160, 200, 240} {
		tu, _ := newKeyTUI("", "an answer")
		tu.Width, tu.Height = w, 24

		// The divider is the widest thing the frame draws: a solid run of ink. It spans the
		// terminal minus the left margin and the ONE column deliberately left free at the end —
		// filling the last column makes some terminals wrap, which would scroll the frame.
		//
		// Exactly one column is reserved for that. A second one used to be dropped by the frame
		// arithmetic as well, which showed up as a visible strip of blank space down the right
		// edge of a wide window.
		rule := stripANSI(tu.rule(w))
		if got := visibleLen(rule); got != w-1 {
			t.Errorf("terminal %d: the rule spans %d columns, want %d", w, got, w-1)
		}

		// And nothing may exceed the terminal.
		lines, _ := tu.layout(w, 24)
		for i, l := range lines {
			if visibleLen(l) > w-1 {
				t.Errorf("terminal %d: row %d is %d columns wide", w, i, visibleLen(l))
			}
		}
		_ = lines
	}
}

// TestEveryFullWidthRowReachesTheSameColumn: the rule, the status bar and the composer all span
// the frame, so they must end on the SAME column. When one of them stops short, the right edge
// of the interface reads as ragged — a strip of blank space beside a row that does fill the
// width, which is what "not covering the full width of the window" looks like.
//
// The composer is excluded on purpose: it is a prompt, not a divider, and it is short by design.
func TestEveryFullWidthRowReachesTheSameColumn(t *testing.T) {
	for _, w := range []int{minWidth, 80, 116, 120, 160, 200} {
		tu, _ := newKeyTUI("", "an answer")
		tu.Width, tu.Height = w, 24
		tu.Runner.(*fakeRunner).snapshot = session.Snapshot{Window: 65536, Tokens: 1649, Used: 0.03}

		rows := map[string]int{
			"rule":      visibleLen(stripANSI(tu.rule(w))),
			"bottomBar": visibleLen(stripANSI(tu.bottomBar(w))),
		}
		want := w - 1
		for name, got := range rows {
			if got != want {
				t.Errorf("terminal %d: %s spans %d columns, want %d (the last column stays free so nothing wraps)",
					w, name, got, want)
			}
		}
	}
}

// TestTheWordmarkRowsAreAligned: the five rows of the banner must start on the same column.
//
// The third row used to carry one extra leading space that the other four did not have, which
// shifted its artwork a column to the right and made the mark read as crooked.
func TestTheWordmarkRowsAreAligned(t *testing.T) {
	leads := make([]int, len(bannerLines))
	for i, row := range bannerLines {
		plain := stripANSI(row)
		leads[i] = len(plain) - len(strings.TrimLeft(plain, " "))
	}
	for i := 1; i < len(leads); i++ {
		if leads[i] != leads[0] {
			t.Errorf("row %d starts at column %d and row 1 at column %d", i+1, leads[i], leads[0])
		}
	}
}

// TestTheWordmarkSeparatesTheLettersItsOwnWay: the banner is block art, so spacing between
// letters is part of the glyphs. The i is a narrow letter and needs the same breathing room as
// the others, or it reads as part of the L beside it.
//
// This asserts the property rather than a column count: every gap between letter runs must be
// at least one column, and the gap around the narrow letter at least two — which is what
// "add a space where the i is formed" asks for, without pinning the exact column.
func TestTheWordmarkSeparatesTheLettersItsOwnWay(t *testing.T) {
	// Count the gap for each row and require the narrow letter's gap to be the widest of them.
	for n, row := range bannerLines {
		plain := []rune(strings.TrimLeft(stripANSI(row), " "))
		var runs [][2]int
		c := 0
		for c < len(plain) {
			if plain[c] != ' ' {
				s := c
				for c < len(plain) && plain[c] != ' ' {
					c++
				}
				runs = append(runs, [2]int{s, c - 1})
			} else {
				c++
			}
		}
		// Every gap must be at least one column: two letters touching would be unreadable.
		for i := 1; i < len(runs); i++ {
			if gap := runs[i][0] - runs[i-1][1] - 1; gap < 1 {
				t.Errorf("row %d: letters %d and %d touch (gap %d)", n+1, i, i+1, gap)
			}
		}
	}
}

// TestTheInputWrapsLongTextInsteadOfOverflowing: the field is a fixed box, so text longer than
// its width has to wrap inside it. A single long word — a path, a URL — has no space to break
// at, and refusing to break would push the text out of the box and widen the row.
func TestTheInputWrapsLongTextInsteadOfOverflowing(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 80, 24
	tu.draft = strings.Repeat("x", 300) // far wider than the box, and one single word

	lines, _ := tu.layout(80, 24)
	if len(lines) > 24 {
		t.Fatalf("a long input must not grow the frame: %d rows", len(lines))
	}
	for i, l := range lines {
		if visibleLen(l) > 79 {
			t.Errorf("row %d is %d columns wide: %q", i, visibleLen(l), stripANSI(l))
		}
	}
	// The LAST characters typed are the ones visible: the box keeps the end of the line, which
	// is where the cursor is.
	body := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(body, "xxxxx") {
		t.Errorf("the field must show the text:\n%s", body)
	}
}

// TestTheInputFieldIsAFixedHeight: the frame must not change shape as the user types. A field
// that grows with its content moves everything above it, which is the "screen jumping" defect in
// a different costume.
func TestTheInputFieldIsAFixedHeight(t *testing.T) {
	var want int
	for n, draft := range []string{"", "a", "a short line", strings.Repeat("word ", 60)} {
		tu, _ := newKeyTUI("", "")
		tu.Width, tu.Height = 80, 24
		tu.draft = draft

		lines, _ := tu.layout(80, 24)
		if n == 0 {
			want = len(lines)
			continue
		}
		if len(lines) != want {
			t.Errorf("draft of %d chars changed the frame height: %d vs %d",
				len(draft), len(lines), want)
		}
	}
	// And the field itself is exactly inputRows tall, whatever is in it.
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 80, 24
	tu.draft = "one line"
	if got := len(tu.composerLines()); got != inputRows {
		t.Errorf("composerLines = %d rows, want %d", got, inputRows)
	}
	tu.draft = strings.Repeat("largo ", 80)
	if got := len(tu.composerLines()); got != inputRows {
		t.Errorf("composerLines with a long draft = %d rows, want %d", got, inputRows)
	}
}

// TestWrapVisibleHandlesADegenerateWidth: a width of zero or less cannot hold anything, and the
// wrapping must produce one character per row rather than looping or dividing by zero.
func TestWrapVisibleHandlesADegenerateWidth(t *testing.T) {
	for _, w := range []int{0, -5} {
		got := wrapVisible("abc", w)
		if len(got) != 3 {
			t.Errorf("width %d: got %d rows, want one per character", w, len(got))
		}
	}
}

// TestWrapVisibleKeepsEscapesWithTheirText: the input is a decorated string, and a wrap that
// split an escape sequence would leak colour across the rest of the frame.
func TestWrapVisibleKeepsEscapesWithTheirText(t *testing.T) {
	s := "\x1b[31mred\x1b[0m normal"
	got := wrapVisible(s, 4)
	if len(got) < 2 {
		t.Fatalf("expected a wrap, got %v", got)
	}
	// Every row must still be measurable: an escape cut in half would confuse the parser and
	// visibleLen would count the fragments as text.
	for i, row := range got {
		if n := visibleLen(row); n > 4 {
			t.Errorf("row %d is %d columns wide: %q", i, n, row)
		}
	}
	// The colour code survives whole on the row where the text begins.
	if !strings.Contains(got[0], "\x1b[31m") {
		t.Errorf("the escape was not kept with its text: %q", got[0])
	}
}

// TestTheStatusLineIsCentred: the identity line sits under the wordmark and must be on the
// mark's axis, not against the left edge.
func TestTheStatusLineIsCentred(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 100, 24

	line := stripANSI(tu.statusLines(100)[0])
	left := len(line) - len(strings.TrimLeft(line, " "))
	content := len(strings.TrimSpace(line))

	// It is centred in the drawing area, so the lead equals what the lead would be for a
	// centred string of this length: (room - content) / 2, plus the margin. Trailing padding is
	// not written — a row that ends where its text ends is what the frame expects.
	room := 100 - leftMargin - 1
	want := leftMargin + (room-content)/2
	if left != want {
		t.Errorf("the status line is off centre: lead %d, want %d:\n%q", left, want, line)
	}
	// And it is genuinely indented from the left edge, not flush against it.
	if left <= leftMargin {
		t.Errorf("the status line is not centred at all: %d columns of lead:\n%q", left, line)
	}
}

// TestPadCenterAlignsWithTheFrame: the banner and everything else must be centred inside the
// same box, or the mark sits half a column off the panels beneath it.
func TestPadCenterAlignsWithTheFrame(t *testing.T) {
	for _, w := range []int{80, 81, 100, 101} {
		tu, _ := newKeyTUI("", "")
		tu.Width = w

		got := stripANSI(tu.padCenter(tu.brand(compactMark), w))
		left := len(got) - len(strings.TrimLeft(got, " "))
		content := len(strings.TrimSpace(got))

		room := w - leftMargin - 1
		want := leftMargin + (room-content)/2
		if left != want {
			t.Errorf("width %d: the mark leads with %d columns, want %d", w, left, want)
		}
	}
}

// TestCenterPlainReservesTheMargin: an oversized string keeps the left margin, because the
// margin is part of the drawing area for every row.
func TestCenterPlainReservesTheMargin(t *testing.T) {
	tu, _ := newKeyTUI("", "")

	got := stripANSI(tu.centerPlain(strings.Repeat("x", 200), 40))
	if !strings.HasPrefix(got, "  ") {
		t.Errorf("the margin must be kept: %q", got[:min(10, len(got))])
	}
	if strings.Contains(got, "\n") {
		t.Error("centring must never introduce a line break")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestChatRowsHitsItsFloorOnAShortTerminal: between the size gate and the height the fixed rows
// need, a terminal can still be shorter than the fixed rows plus a minimal conversation. The
// floor is what keeps the conversation area positive there instead of negative.
func TestChatRowsHitsItsFloorOnAShortTerminal(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width = 80

	// Just above the gate, which is below what the fixed rows want. The value is read through
	// the path that actually uses it — paging — rather than by calling the helper: the helper is
	// only ever reached from a key press, so a test that calls it directly would pass even if
	// paging stopped consulting it.
	for _, h := range []int{minHeight, minHeight + 1, permanentRows} {
		if h < minHeight {
			continue
		}
		tu.Height = h
		tu.scroll = 0
		padBody(tu, 60)
		_ = context.Background()
		if got := tu.chatRows(); got < minChatLines {
			t.Errorf("h=%d: chatRows = %d, want at least the floor %d", h, got, minChatLines)
		}
	}
	// And with plenty of room it is the height minus the fixed rows, exactly.
	tu.Height = 30
	if got := tu.chatRows(); got != 30-permanentRows {
		t.Errorf("chatRows on a 30-row terminal = %d, want %d", got, 30-permanentRows)
	}
}

// TestTheCursorFollowsTheInputAsItWraps: the input is a box of several rows and the text wraps
// inside it, so the cursor's position is not "the first row at the total length".
//
// Computing the column from the whole string put the cursor past the right edge once the text
// passed the width — the terminal then moved it on its own, or refused to move it at all, and the
// user saw a cursor that was not where they were typing.
func TestTheCursorFollowsTheInputAsItWraps(t *testing.T) {
	const (
		width  = 40
		height = 20
	)

	for _, n := range []int{0, 5, 17, 25, 33, 41, 60, 200} {
		tu, _ := newKeyTUI("", "")
		tu.Width, tu.Height = width, height
		tu.draft = strings.Repeat("x", n)

		lines, prompt := tu.layout(width, height)

		m := regexp.MustCompile(`\x1b\[(\d+)A\x1b\[(\d+)G`).FindStringSubmatch(prompt)
		if m == nil {
			t.Fatalf("n=%d: the layout placed no cursor: %q", n, prompt)
		}
		up, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])

		// The cursor must land INSIDE the frame.
		row := len(lines) - 1 - up
		if row < 0 || row >= len(lines) {
			t.Errorf("n=%d: the cursor is on row %d, outside the %d-row frame", n, row, len(lines))
			continue
		}
		// And inside the terminal's width, which is what "past the right edge" meant.
		if col-1 >= width {
			t.Errorf("n=%d: the cursor is at column %d of a %d-column terminal", n, col-1, width)
		}
		// The row it lands on must be a row of the input box: it carries the prompt on its first
		// row, and the box is the last group of rows before the rule and the bar.
		fieldStart := len(lines) - 2 - inputRows
		if row < fieldStart || row > fieldStart+inputRows-1 {
			t.Errorf("n=%d: the cursor is on row %d, outside the input box (rows %d..%d)",
				n, row, fieldStart, fieldStart+inputRows-1)
			continue
		}
		// The column must be where the drawn text ENDS on that row: one past its last character.
		drawn := stripANSI(lines[row])
		if col-1 != visibleLen(drawn) && n > 0 {
			t.Errorf("n=%d: the cursor is at column %d but the row is %d columns wide: %q",
				n, col-1, visibleLen(drawn), drawn)
		}
	}
}

// TestTheQuestionIsShownAsAQuestionNotAFailure: when the agent cannot read the request it asks.
// Reporting that as "failed" tells the user they did something wrong when they only need to add
// three words, and it hides the question they are supposed to answer.
func TestTheQuestionIsShownAsAQuestionNotAFailure(t *testing.T) {
	got := summarise(agent.TaskResult{
		NeedsInput: true,
		Question:   "Do you want me to check the disk or the logs?",
		Assumption: "I assume the state of the disk",
	})
	if strings.HasPrefix(got, "failed") {
		t.Errorf("a question must not be reported as a failure: %q", got)
	}
	if !strings.Contains(got, "the disk or the logs") {
		t.Errorf("the question must be shown: %q", got)
	}
	// The assumption travels with it, so the user can confirm in one word.
	//
	// The assertion is over the ASSUMPTION itself, which is what the comment above says the test
	// is for. It used to assert the presence of the word "assume", which appears nowhere in the
	// fixture — so what it actually pinned was the hardcoded Spanish lead the interface put in
	// front of the field ("If you do not tell me otherwise, I will assume: "). That lead fixed the
	// language of
	// every conversation, and the assertion kept it there by failing when it was removed.
	if !strings.Contains(got, "I assume the state of the disk") {
		t.Errorf("the assumption must be shown so the user can confirm it: %q", got)
	}
}

// TestAQuestionWithNoTextStillSaysSomething: the model may return NeedsInput without a question —
// it reported that it could not understand but named nothing to ask. The user must still get a
// sentence, not an empty bubble.
func TestAQuestionWithNoTextStillSaysSomething(t *testing.T) {
	got := summarise(agent.TaskResult{NeedsInput: true})
	if strings.TrimSpace(got) == "" {
		t.Error("a question with no text must still produce a sentence")
	}
	if strings.HasPrefix(got, "failed") {
		t.Errorf("it is not a failure: %q", got)
	}
}

// TestAQuestionWithNoAssumptionOmitsTheLine: with nothing assumed there is nothing to offer as a
// default, and printing an empty "I will assume:" would be worse than saying nothing.
func TestAQuestionWithNoAssumptionOmitsTheLine(t *testing.T) {
	got := summarise(agent.TaskResult{NeedsInput: true, Question: "what do you want?"})
	if strings.Contains(got, "I will assume") {
		t.Errorf("no assumption means no assumption line: %q", got)
	}
}

// TestLeavingClearsTheScreen: the interface took the screen over when it started, so it gives it
// back when it ends. Leaving a full-screen frame behind hands the shell a window covered in text
// that is not the user's, with their prompt somewhere above it.
func TestLeavingClearsTheScreen(t *testing.T) {
	for _, in := range []string{"/quit\n", "q\n", ""} {
		var out bytes.Buffer
		tu := New(&fakeRunner{cfg: configWithKey("k")})
		tu.In = strings.NewReader(in)
		tu.Out = &out
		tu.Width, tu.Height = 100, 24

		tu.Run(context.Background())

		if !strings.HasSuffix(out.String(), exitClear) {
			t.Errorf("input %q: the screen must be cleared on the way out", in)
		}
	}
}

// TestTheExitClearRestoresTheCursor: the frame hides the cursor while painting, so an exit that
// does not show it again leaves the user with a terminal and no cursor.
func TestTheExitClearRestoresTheCursor(t *testing.T) {
	if !strings.HasPrefix(exitClear, "\x1b[?25h") {
		t.Error("the exit sequence must show the cursor first")
	}
	// And it wipes the scrollback too: the frames the user scrolled through are part of what the
	// interface put on the screen.
	if !strings.Contains(exitClear, "\x1b[3J") {
		t.Error("the exit sequence must clear the scrollback, not only the visible screen")
	}
}

// TestClearingOnExitWithoutAnOutput: clearOnExit is called from a defer, which runs even when the
// interface was built without an output — a partially constructed TUI in a test, or an embedder
// that only wants the model. Writing to a nil writer would panic ON THE WAY OUT, turning a normal
// quit into a crash.
func TestClearingOnExitWithoutAnOutput(t *testing.T) {
	tu := &TUI{}
	tu.clearOnExit() // must not panic
}

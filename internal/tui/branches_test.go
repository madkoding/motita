package tui

import (
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
)

// The remaining branches: the clamps that only fire at a degenerate size, the
// defaults used when the configuration is incomplete, and the two escape kinds
// that a colour-only test never exercises.

// TestInnerHasNoClampBecauseSizeAlreadyDoes: the floor lives in size(), so inner
// can be computed directly. This test pins the relationship, so a future change to
// either side is caught.
func TestInnerHasNoClampBecauseSizeAlreadyDoes(t *testing.T) {
	tui := &TUI{Width: 1}
	if got, want := tui.inner(), tui.frameCols()-6; got != want {
		t.Errorf("inner = %d, want %d", got, want)
	}
	if got := tui.inner(); got < 8 {
		t.Errorf("inner = %d: size() must have floored the width before this point", got)
	}
}

// TestStatusLinesDefaultsAnIncompleteConfiguration: a configuration with no
// provider and no model still has to describe something, and the values it shows
// are the fallbacks rather than empty fields.
func TestStatusLinesDefaultsAnIncompleteConfiguration(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = ""
	cfg.LLM.Model = ""
	cfg.LLM.APIKey = "k"
	cfg.LLM.Reasoning = config.Reasoning{Enabled: true, Level: "high"}
	runner := &fakeRunner{cfg: cfg, cfgSet: true}
	tui := newFakeTUI("q\n", runner)

	// The status line is assembled from three parts and a narrow width drops the
	// tail, so the widest form is asked for explicitly.
	line := stripANSI(strings.Join(tui.statusLines(200), "\n"))
	for _, want := range []string{"openai", "unknown", "key present", "reasoning high"} {
		if !strings.Contains(line, want) {
			t.Errorf("the status line must contain %q:\n%s", want, line)
		}
	}
}

// TestChatTopRowClampsTheRule: on a terminal where the title alone does not fit,
// the rule falls back to one character instead of a negative repeat count.
func TestChatTopRowClampsTheRule(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.Width = 1
	tui.screen = ScreenModels // the longest title
	top := stripANSI(strings.Join(tui.chatTopRow(), "\n"))
	if !strings.HasSuffix(top, glyphTopRight) {
		t.Errorf("the border must still close on the right: %q", top)
	}
	if !strings.Contains(top, "Models") {
		t.Errorf("the title must still be shown: %q", top)
	}
}

// TestHintLinesStopsWhenNothingFits: a terminal too narrow for even the first hint
// gets none, rather than a line that overflows the frame.
func TestHintLinesStopsWhenNothingFits(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	// "Tab switch mode" needs more than this.
	if lines := tui.hintLines(8); lines != nil {
		t.Errorf("nothing fits in 8 columns, got %q", lines)
	}
}

// TestScanEscapesHandlesOSCAndTwoByteEscapes: a window title (OSC) and a charset
// selection (a two-byte escape) both occupy zero columns, and the text around
// them is still measured. Getting this wrong would misalign the whole frame.
func TestScanEscapesHandlesOSCAndTwoByteEscapes(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"\x1b]0;title\x07abc", 3},                  // OSC terminated by BEL
		{"\x1b]0;title\x1b\\abc", 3},                // OSC terminated by ESC \
		{"\x1b(Babc", 3},                            // two-byte escape (charset)
		{"\x1b]8;;http://x\x07link\x1b]8;;\x07", 4}, // an OSC-8 hyperlink
		{"\x1b]unterminated", 0},                    // never closed: everything is escape
	}
	for _, tc := range cases {
		if got := visibleLen(tc.in); got != tc.want {
			t.Errorf("visibleLen(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if got := stripANSI("\x1b]0;title\x07abc"); got != "abc" {
		t.Errorf("stripANSI left %q, want %q", got, "abc")
	}
	if got := stripANSI("\x1b(Babc"); got != "abc" {
		t.Errorf("stripANSI left %q, want %q", got, "abc")
	}
}

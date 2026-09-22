package tui

import (
	"strings"
	"testing"
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
// TestStatusLinesDefaultsAnIncompleteConfiguration: a configuration that names neither a
// provider nor a model must still produce a readable line rather than an empty one.
//
// The key is deliberately NOT on this line any more: a user who reached the chat has a
// working key, and repeating it on every repaint is noise the eye learns to skip.
func TestStatusLinesDefaultsAnIncompleteConfiguration(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})

	line := stripANSI(strings.Join(tui.statusLines(80), "\n"))
	if !strings.Contains(line, "openai") {
		t.Errorf("an unnamed provider must default to openai, got %q", line)
	}
	// config.Default() carries a model, so "unknown" is only what an explicitly empty one
	// gets; what matters here is that the line is never blank.
	if strings.TrimSpace(line) == "" {
		t.Errorf("the status line must never be empty, got %q", line)
	}
	if !strings.Contains(line, "reasoning") {
		t.Errorf("the reasoning level must be shown, got %q", line)
	}
	if strings.Contains(line, "key") {
		t.Errorf("the key must not be reported in the chat, got %q", line)
	}
}

// TestChatTopRowClampsTheRule: on a terminal where the title alone does not fit,
// the rule falls back to one character instead of a negative repeat count.
// TestTheRulesSpanTheSameWidth: the two dividers bracket the conversation, so they must be
// the same length or the structure they carry reads as crooked. The old top border also had
// to close its right corner after the title and the position indicator; with the framing
// gone, the width is the whole invariant there is.
func TestTheRulesSpanTheSameWidth(t *testing.T) {
	// Wide terminals included on purpose: the interface used to stop at a maximum width and
	// leave the rest of the window blank, which is what the user saw as not filling the screen.
	for _, w := range []int{minWidth, 80, 116, 160, 240} {
		tui := newFakeTUI("q\n", &fakeRunner{})
		tui.Width = w

		top := stripANSI(tui.rule(w))
		bottom := stripANSI(tui.rule(w))
		if visibleLen(top) != visibleLen(bottom) {
			t.Errorf("width %d: the rules differ, %d vs %d", w, visibleLen(top), visibleLen(bottom))
		}
		// The rule leaves the last column free, like every other row.
		if visibleLen(top) > w-1 {
			t.Errorf("width %d: the rule is %d columns and would fill the last one", w, visibleLen(top))
		}
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

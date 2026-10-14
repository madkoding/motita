package termcap

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func filtered(p Profile, chunks ...string) string {
	var b bytes.Buffer
	w := p.Writer(&b)
	for _, c := range chunks {
		if n, err := w.Write([]byte(c)); err != nil || n != len(c) {
			panic("short write")
		}
	}
	return b.String()
}

// TestNothingToFilter: a colour terminal that can draw every glyph gets the writer itself.
func TestNothingToFilter(t *testing.T) {
	var b bytes.Buffer
	if w := (Profile{Level: ANSI16}).Writer(&b); w != &b {
		t.Error("a writer with nothing to change must be returned as it is")
	}
}

// TestColourIsDroppedAndTheRestKept: without colour only SGR goes; cursor moves and screen clears
// stay, because the menus redraw with them.
func TestColourIsDroppedAndTheRestKept(t *testing.T) {
	in := "\x1b[1m* motita\x1b[0m \x1b[2K\r\x1b[3A\u00b7\x1bOA"
	want := "* motita \x1b[2K\r\x1b[3A\u00b7\x1bOA"
	if got := filtered(Profile{}, in); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// The colour stays when the profile has it and only the glyphs are swapped.
	if got := filtered(Profile{Level: ANSI16, ASCII: true}, "\x1b[36m\u250c\u2500\u2022\x1b[0m"); got != "\x1b[36m+-*\x1b[0m" {
		t.Errorf("ASCII with colour: %q", got)
	}
}

// TestSequencesSplitAcrossWrites: a sequence or a character cut between two writes is still
// recognised, not passed on in halves.
func TestSequencesSplitAcrossWrites(t *testing.T) {
	if got := filtered(Profile{}, "a\x1b", "[3", "6mb\x1b[0m"); got != "ab" {
		t.Errorf("split escape: %q", got)
	}
	box := "\u2502"
	if got := filtered(Profile{ASCII: true}, "x"+box[:1], box[1:]+"y"); got != "x|y" {
		t.Errorf("split character: %q", got)
	}
}

// TestRunawayEscape: an ESC that never ends a sequence is passed on rather than held forever.
func TestRunawayEscape(t *testing.T) {
	long := "\x1b[" + strings.Repeat("1", maxPending+5)
	if got := filtered(Profile{}, long); got != long {
		t.Errorf("runaway escape: %q", got)
	}
}

// TestGlyphStandIns: every stand-in is one column per column it replaces, and text the console
// can show passes through.
func TestGlyphStandIns(t *testing.T) {
	cases := map[string]string{
		"\u250c\u2500\u2510\u2502\u2514\u2518": "+-+|++",
		"\u2550\u2551\u2554\u256c":             "-|++",
		"\u258e\u2588":                         "##",
		"\u2022 \u00b0 \u00bb \u00ab \u00b7 \u2026 \u2014 \u2013 \u2018\u2019\u201c\u201d \u2039 \u203a": `* o > < . . - - ''"" < >`,
		"\u2190 \u2191 \u2192 \u2193 \u221a \u2261 \u25b6 \u25b8 \u25cf \u25cb \u26a0":                   "< ^ > v v = > > * o !",
		"\u2713\u2714\u2717\u2718": "vvxx",
		"\U0001f642":               "??",
		"\u2605":                   "?",
		"¿Qué tal? ¡Sí! ñ":         "¿Qué tal? ¡Sí! ñ", // spanish-fixture: Latin-1 passes through
		"\xff":                     "?",
	}
	for in, want := range cases {
		if got := filtered(Profile{ASCII: true}, in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, errors.New("closed") }

// TestWriteErrorsReachTheCaller: a failed write is reported, not swallowed; a write the filter
// drops entirely touches nothing.
func TestWriteErrorsReachTheCaller(t *testing.T) {
	w := Profile{}.Writer(failing{})
	if _, err := w.Write([]byte("text")); err == nil {
		t.Error("the error must reach the caller")
	}
	if n, err := w.Write([]byte("\x1b[0m")); err != nil || n != 4 {
		t.Errorf("a dropped write: %d, %v", n, err)
	}
}

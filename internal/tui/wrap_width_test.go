package tui

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// --- why this file exists ----------------------------------------------------
//
// Profiling the per-keystroke path (BenchmarkLayout, body=20) put 50% of the samples in
// scanEscapes and visibleLen. Two things caused it:
//
//   - wordWrap called visibleLen(cur.String()) once per WORD, allocating the whole accumulated
//     line and re-scanning it from the start every time: O(n^2) in the length of a paragraph.
//     A conversation block is a long paragraph, so this runs on the hot path.
//   - visibleLen ran the escape state machine even when the string held no ESC byte at all,
//     which is the common case for plain text.
//
// Both are fixed without changing what any of them returns. These tests pin the behaviour so
// the optimisation cannot pass by being subtly wrong: the expected values here are written by
// hand, not taken from the code.

// TestVisibleLenCountsRunesNotBytes: the width of a line is measured in COLUMNS, and a rune
// that is not ASCII occupies one column but several bytes. Counting bytes would make every
// accented line too wide and misalign the frame.
func TestVisibleLenCountsRunesNotBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"ascii", "abc", 3},
		{"empty", "", 0},
		{"accented latin", "café", 4},
		{"multi-byte, one column each", "áéíóú", 5},
		{"emoji is one rune", "\U0001F31F", 1},
		{"mixed", "a\U0001F31Fb", 3},
		{"a combining accent is its own rune", "e\u0301", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := visibleLen(tc.in); got != tc.want {
				t.Errorf("visibleLen(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestWordWrapKeepsItsExactOutput: the fast path must produce byte-identical lines to the
// version it replaces, including the awkward cases. A wrapper that is merely "reasonable"
// would silently re-wrap the conversation and change what the reader sees.
func TestWordWrapKeepsItsExactOutput(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{
			name: "fits on one line",
			in:   "hola mundo", width: 40,
			want: []string{"hola mundo"},
		},
		{
			name: "wraps at a space",
			in:   "uno dos tres cuatro", width: 8,
			want: []string{"uno dos", "tres", "cuatro"},
		},
		{
			name: "a single word wider than the line is hard split",
			in:   "abcdefghij", width: 4,
			want: []string{"abcd", "efgh", "ij"},
		},
		{
			name: "an explicit newline starts a new paragraph",
			in:   "uno\ndos", width: 20,
			want: []string{"uno", "dos"},
		},
		{
			name: "runs of spaces collapse, as Fields does",
			in:   "uno    dos", width: 20,
			want: []string{"uno dos"},
		},
		{
			name: "empty input still yields one (empty) line",
			in:   "", width: 20,
			want: []string{""},
		},
		{
			name: "zero width returns the text untouched",
			in:   "no wrapping here", width: 0,
			want: []string{"no wrapping here"},
		},
		{
			name: "accents are measured in columns while wrapping",
			in:   "café con leche", width: 8,
			want: []string{"café con", "leche"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wordWrap(tc.in, tc.width)
			if len(got) != len(tc.want) {
				t.Fatalf("wordWrap(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %q, want %q (full: %q)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

// TestWordWrapMeasuresEscapesAsZeroWidth is the case that makes the accumulated-width
// optimisation safe: a coloured line carries escapes, which occupy no columns, so the running
// total must be built from the same measurement visibleLen reports.
func TestWordWrapMeasuresEscapesAsZeroWidth(t *testing.T) {
	// Two visible words of 3 columns each, wrapping at 8: "abc def" fits, "ghi" does not.
	in := "\x1b[31mabc\x1b[0m \x1b[32mdef\x1b[0m \x1b[33mghi\x1b[0m"
	got := wordWrap(in, 8)
	for _, l := range got {
		if n := visibleLen(stripANSI(l)); n == 0 && strings.TrimSpace(l) != "" {
			t.Errorf("a non-empty wrapped line measured zero columns: %q", l)
		}
	}
	// The escapes must survive: wrapping is a presentation decision, not a rewrite of the
	// colour the caller asked for.
	joined := strings.Join(got, " ")
	for _, seq := range []string{"\x1b[31m", "\x1b[32m", "\x1b[33m"} {
		if !strings.Contains(joined, seq) {
			t.Errorf("wrapping dropped the colour %q from %q", seq, joined)
		}
	}
}

// TestWordWrapWidthIsMeasuredInVisibleColumns pins the invariant that ties the two functions
// together: no wrapped line may exceed the width it was given, counting columns and not bytes.
//
// This is the regression test for the overflow bug. It asserts on every line wordWrap returns,
// over a sweep of widths, because the defect was conditional: a long word only overflowed when it
// followed a SHORT one, so a single example would have passed.
func TestWordWrapWidthIsMeasuredInVisibleColumns(t *testing.T) {
	text := strings.Repeat("palabra con acentos café \U0001F31F ", 40)
	for width := 4; width <= 40; width++ {
		for _, l := range wordWrap(text, width) {
			if n := visibleLen(l); n > width {
				t.Fatalf("width %d: a line measured %d columns: %q", width, n, l)
			}
		}
	}
}

// TestALongWordSurvivesWrappingAndClipping is the user-visible half of the overflow bug, and the
// reason the invariant above matters.
//
// An overflowing line is not drawn past the panel: railLines hands it to cell(), which clips it to
// the width and marks the cut with an ellipsis. So nothing crashes and nothing looks broken — a
// path just loses its tail. That is the dangerous form of the defect: the reader is shown a
// shorter, plausible path, with no indication that the rest of it exists.
//
// The assertion is deliberately NOT "the word appears on one line". A word wider than the panel is
// supposed to be hard split across several lines, so it is never contiguous on screen. What must
// hold either way is that no CHARACTER is lost: the text that is drawn, with the layout's spaces
// and margins removed, must still spell out the original. That is what the ellipsis breaks.
func TestALongWordSurvivesWrappingAndClipping(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		width int
	}{
		{"a long word after a short one", "ver acentoslargos", 10},
		{"a path after prose", "mira /usr/local/share/starlight/SKILL.md", 20},
		{"a URL after prose", "clona https://github.com/madkoding/starlight.git", 24},
		{"a long word at the start of a line", "acentoslargos y mas", 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tu, _ := newKeyTUI("")
			// Render exactly as railLines does: wrap into inner-leftMargin columns, then clip
			// into a cell of `inner` columns — cell() spends the margin before the text, so it
			// is passed the full row width, not the text width.
			row := tc.width + leftMargin
			var drawn string
			for _, l := range wordWrap(tc.text, tc.width) {
				drawn += stripANSI(tu.cell(l, row))
			}
			// Drop every space: the layout inserts its own margins and the wrap replaces the
			// spaces it breaks on, so spaces carry no information here. What is left is the
			// text itself, and it must match character for character.
			want := squash(tc.text)
			if got := squash(drawn); got != want {
				t.Errorf("width %d: the drawn text lost or altered characters\ngot:  %q\nwant: %q\ndrawn: %q",
					tc.width, got, want, drawn)
			}
		})
	}
}

// squash removes every run of whitespace, so two renderings of the same text can be compared
// without depending on where a line was broken or how wide the margins are.
func squash(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestSplitAtWidthCutsOnAColumnBoundary covers both ways the cut can end: stopping once the
// requested number of columns has been counted, and running out of text while there were still
// fewer columns than asked for. The second is the case a word that fits on a line hits, and it
// must return the whole string with an offset of len(s) so the caller's remainder is empty.
func TestSplitAtWidthCutsOnAColumnBoundary(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		width  int
		wantAt int
		want   string
	}{
		{"exactly the width", "abcdef", 3, 3, "abc"},
		{"wider than the width", "abcdef", 4, 4, "abcd"},
		{"narrower than the width returns everything", "ab", 5, 2, "ab"},
		{"empty returns everything", "", 3, 0, ""},
		{"multi-byte runes are not cut in half", "café", 3, 3, "caf"},
		{"a rune that spans the boundary stays whole", "aá", 1, 1, "a"},
		{"an invalid byte still advances the cut", "\xff\xfe\xfd", 1, 1, "\xff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, piece := splitAtWidth(tc.in, tc.width)
			if at != tc.wantAt {
				t.Errorf("splitAtWidth(%q, %d) cut at %d, want %d", tc.in, tc.width, at, tc.wantAt)
			}
			if piece != tc.want {
				t.Errorf("splitAtWidth(%q, %d) = %q, want %q", tc.in, tc.width, piece, tc.want)
			}
			// Whatever is cut must leave a well-formed remainder: the caller re-wraps it, and
			// half an encoding would be measured as replacement characters. This only holds
			// when the input was valid to begin with — it cannot be repaired here, and this
			// function's job is to not make it worse.
			if utf8.ValidString(tc.in) {
				if rest := tc.in[at:]; !utf8.ValidString(rest) {
					t.Errorf("the remainder %q is not valid UTF-8", rest)
				}
			}
		})
	}
}

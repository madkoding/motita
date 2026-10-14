package termcap

import (
	"io"
	"sync"
	"unicode/utf8"
)

// maxPending caps what the filter holds back while it waits for the rest of an escape sequence or
// a character split across two writes. Nothing this program writes comes close; a run of bytes
// that never ends a sequence is passed on as it is rather than buffered without limit.
const maxPending = 64

// Writer puts a filter in front of w that makes what is written fit the profile: colour escapes
// are dropped when the profile has no colour, and the glyphs a legacy console cannot show become
// ASCII stand-ins of the same width. w itself is returned when there is nothing to change.
//
// The filter is for a writer whose author writes colour unconditionally, like the setup wizard.
// The interface decides about colour itself and is handed the filter only for the glyphs.
func (p Profile) Writer(w io.Writer) io.Writer {
	if p.Color() && !p.ASCII {
		return w
	}
	return &filter{w: w, stripSGR: !p.Color(), ascii: p.ASCII}
}

type filter struct {
	mu       sync.Mutex
	w        io.Writer
	stripSGR bool
	ascii    bool
	pending  []byte
}

// Unwrap is the writer behind the filter, so IsTerminal still sees the console.
func (f *filter) Unwrap() io.Writer { return f.w }

// Write filters p. It reports len(p) on success even when it held bytes back or dropped them: the
// caller wrote all of them, and a short count would make it retry.
func (f *filter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data := append(f.pending, p...)
	f.pending = nil
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		c := data[i]
		switch {
		case c == 0x1b:
			end, complete := sequenceEnd(data, i)
			if !complete {
				if len(data)-i <= maxPending {
					f.pending = append([]byte(nil), data[i:]...)
					i = len(data)
					continue
				}
				end = i + 1 // not a sequence that ever ends: pass the ESC on as a byte
			}
			if !(f.stripSGR && end-i > 2 && data[i+1] == '[' && data[end-1] == 'm') {
				out = append(out, data[i:end]...)
			}
			i = end
		case c < utf8.RuneSelf || !f.ascii:
			out = append(out, c)
			i++
		case !utf8.FullRune(data[i:]):
			f.pending = append([]byte(nil), data[i:]...)
			i = len(data)
		default:
			r, size := utf8.DecodeRune(data[i:])
			out = append(out, asciiFor(r)...)
			i += size
		}
	}
	if len(out) > 0 {
		if _, err := f.w.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// sequenceEnd finds the end of the escape sequence that starts at data[i]. A CSI sequence
// (ESC [ params final) ends at its final byte; any other escape is ESC and the byte after it.
// complete=false means the data ran out first.
func sequenceEnd(data []byte, i int) (int, bool) {
	if i+1 >= len(data) {
		return 0, false
	}
	if data[i+1] != '[' {
		return i + 2, true
	}
	for j := i + 2; j < len(data); j++ {
		if data[j] >= 0x40 && data[j] <= 0x7e {
			return j + 1, true
		}
	}
	return 0, false
}

// asciiFor is the stand-in for a character a legacy console may not show. Each is as wide as what
// it replaces, so the frame the interface measured still lines up. Letters, accents and the
// Spanish punctuation pass through: every console font has Latin-1.
func asciiFor(r rune) string {
	if s, ok := glyphs[r]; ok {
		return s
	}
	switch {
	case r >= 0x2500 && r <= 0x257f:
		return boxDrawing(r)
	case r >= 0x2580 && r <= 0x259f:
		return "#" // block elements
	case r >= 0x1f000:
		return "??" // emoji and pictographs are two columns wide
	case r >= 0x2000:
		return "?"
	}
	return string(r)
}

var glyphs = map[rune]string{
	'\u00b7': ".",
	'\u00b0': "o",
	'\u00bb': ">",
	'\u00ab': "<",
	'\u2022': "*",
	'\u2026': ".",
	'\u2013': "-",
	'\u2014': "-",
	'\u2018': "'",
	'\u2019': "'",
	'\u201c': `"`,
	'\u201d': `"`,
	'\u2039': "<",
	'\u203a': ">",
	'\u2190': "<",
	'\u2191': "^",
	'\u2192': ">",
	'\u2193': "v",
	'\u221a': "v",
	'\u2261': "=",
	'\u25b6': ">",
	'\u25b8': ">",
	'\u25cf': "*",
	'\u25cb': "o",
	'\u26a0': "!",
	'\u2713': "v",
	'\u2714': "v",
	'\u2717': "x",
	'\u2718': "x",
}

// boxDrawing maps the box-drawing block: horizontal lines to '-', vertical ones to '|', and every
// corner, tee and cross to '+'.
func boxDrawing(r rune) string {
	switch r {
	case 0x2500, 0x2501, 0x2504, 0x2505, 0x2508, 0x2509, 0x254c, 0x254d, 0x2550,
		0x2574, 0x2576, 0x2578, 0x257a, 0x257c, 0x257e:
		return "-"
	case 0x2502, 0x2503, 0x2506, 0x2507, 0x250a, 0x250b, 0x254e, 0x254f, 0x2551,
		0x2575, 0x2577, 0x2579, 0x257b, 0x257d, 0x257f:
		return "|"
	}
	return "+"
}

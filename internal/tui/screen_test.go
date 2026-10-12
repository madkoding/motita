package tui

import (
	"strconv"
	"strings"
)

// screen is a minimal terminal emulator, just enough to answer the only question that matters
// about a frame: what is ON THE SCREEN once it has been written.
//
// It exists because reading a frame's own text is not the same thing. A frame that writes
// "› " leaves the previous "› hazlo" on the screen, and its text looks perfectly correct. Every
// check that inspected the bytes of the last frame passed while the user watched their text sit
// there — the frame was right and the screen was wrong.
//
// So the tests that care about the screen use this: feed it the written frames and read the
// resulting cells, the way a terminal would. It only implements what the interface writes
// (position, erase, relative moves) and ignores the rest, which is enough to catch stale cells.
type screen struct {
	w, h int
	grid [][]rune
	x, y int
}

func newScreen(w, h int) *screen {
	s := &screen{w: w, h: h, grid: make([][]rune, h)}
	for i := range s.grid {
		s.grid[i] = []rune(strings.Repeat(" ", w))
	}
	return s
}

// feed writes a stream of output into the screen.
func (s *screen) feed(data string) {
	for i := 0; i < len(data); {
		c := data[i]
		switch {
		case c == 0x1b && i+1 < len(data) && data[i+1] == '[':
			// CSI: parameters then a final byte in 0x40..0x7e.
			j := csiFinal(data, i+2)
			if j >= len(data) {
				return
			}
			s.applyCSI(data[i+2:j], data[j])
			i = j + 1
		case c == 0x1b:
			i += 2 // a two-byte escape with no parameters
		case c == '\n':
			s.newline()
			i++
		case c == '\r':
			s.x = 0
			i++
		case c < 0x20:
			i++ // other control bytes have no effect here
		default:
			// Decode one rune so multibyte text lands as one cell.
			r, size := runeAt(data, i)
			s.put(r)
			i += size
		}
	}
}

// csiFinal is the index of the final byte (0x40..0x7e) of a CSI whose parameters start at j,
// or len(data) when the sequence is cut short.
func csiFinal(data string, j int) int {
	for j < len(data) && (data[j] < 0x40 || data[j] > 0x7e) {
		j++
	}
	return j
}

// runeAt decodes the rune at data[i] and the number of bytes it takes.
func runeAt(data string, i int) (rune, int) {
	c := data[i]
	r := rune(c)
	size := 1
	if c >= 0x80 {
		for size < 4 && i+size < len(data) && data[i+size]&0xc0 == 0x80 {
			size++
		}
		r = []rune(data[i : i+size])[0]
	}
	return r, size
}

// newline moves to the start of the next row, staying on the last one at the bottom.
func (s *screen) newline() {
	s.y++
	s.x = 0
	if s.y >= s.h {
		s.y = s.h - 1
	}
}

func (s *screen) applyCSI(params string, final byte) {
	// Private modes (?25l/h) carry a '?' and mean nothing to the cells.
	if strings.HasPrefix(params, "?") {
		return
	}
	n := 0
	if params != "" {
		n, _ = strconv.Atoi(strings.TrimSuffix(params, ";"))
	}
	switch final {
	case 'H', 'f':
		// Absolute position, and the parameters matter: "CSI 5;1H" is row 5, not the origin.
		// Ignoring them made this emulator land every row at the top, which is exactly the
		// mistake that hid the reason the incremental frame failed here.
		s.cursorTo(params)
	case 'A':
		s.cursorUp(n)
	case 'B':
		s.cursorDown(n)
	case 'G':
		s.cursorColumn(n)
	case 'K':
		// Erase in line: 0 (default) to the end, 1 to the start, 2 the whole line.
		s.eraseLine(n)
	case 'J':
		// Erase in display: 0 (default) from the cursor down.
		s.eraseDown()
	default:
		// the other sequences do not change the cells
	}
}

// cursorTo is CSI row;col H: an absolute position, the origin when there are no parameters.
func (s *screen) cursorTo(params string) {
	if params == "" {
		s.y, s.x = 0, 0
		return
	}
	parts := strings.Split(params, ";")
	row, col := 0, 0
	if len(parts) > 0 && parts[0] != "" {
		row, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 && parts[1] != "" {
		col, _ = strconv.Atoi(parts[1])
	}
	if row > 0 {
		s.y = row - 1
	}
	if col > 0 {
		s.x = col - 1
	}
	if s.y >= s.h {
		s.y = s.h - 1
	}
	if s.x >= s.w {
		s.x = s.w - 1
	}
}

// cursorUp is CSI n A: n rows up (one when n is 0), stopping at the top.
func (s *screen) cursorUp(n int) {
	if n == 0 {
		n = 1
	}
	s.y -= n
	if s.y < 0 {
		s.y = 0
	}
}

// cursorDown is CSI n B: n rows down (one when n is 0), stopping at the bottom.
func (s *screen) cursorDown(n int) {
	if n == 0 {
		n = 1
	}
	s.y += n
	if s.y >= s.h {
		s.y = s.h - 1
	}
}

// cursorColumn is CSI n G: column n (the first when n is 0), kept inside the row.
func (s *screen) cursorColumn(n int) {
	if n == 0 {
		n = 1
	}
	s.x = n - 1
	if s.x < 0 {
		s.x = 0
	}
	if s.x >= s.w {
		s.x = s.w - 1
	}
}

// eraseLine is CSI n K: 0 (default) to the end, 1 to the start, 2 the whole line.
func (s *screen) eraseLine(n int) {
	switch n {
	case 1:
		for i := 0; i <= s.x && i < s.w; i++ {
			s.grid[s.y][i] = ' '
		}
	case 2:
		for i := 0; i < s.w; i++ {
			s.grid[s.y][i] = ' '
		}
	default:
		for i := s.x; i < s.w; i++ {
			s.grid[s.y][i] = ' '
		}
	}
}

// eraseDown is CSI J: everything from the cursor down.
func (s *screen) eraseDown() {
	for yy := s.y; yy < s.h; yy++ {
		from := 0
		if yy == s.y {
			from = s.x
		}
		for i := from; i < s.w; i++ {
			s.grid[yy][i] = ' '
		}
	}
}

func (s *screen) put(r rune) {
	if s.y < 0 || s.y >= s.h || s.x < 0 || s.x >= s.w {
		s.x++
		return
	}
	s.grid[s.y][s.x] = r
	s.x++
}

// rows returns the screen as trimmed lines, top to bottom.
func (s *screen) rows() []string {
	out := make([]string, s.h)
	for i, r := range s.grid {
		out[i] = strings.TrimRight(string(r), " ")
	}
	return out
}

// text returns the whole screen as one string, for a substring check.
func (s *screen) text() string {
	return strings.Join(s.rows(), "\n")
}

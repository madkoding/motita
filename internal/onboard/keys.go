package onboard

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// KeyMode puts the terminal into character-at-a-time input with no echo, and returns the function
// that puts it back. ok is false when it could not (no terminal, a platform without the mode): the
// setup then reads whole lines, the way it always has.
//
// It is a parameter rather than something this package does itself because the terminal belongs to
// the caller: the interface already knows how to switch modes, and a second implementation here
// would be a second place for a terminal to be left unusable.
type KeyMode func() (restore func(), ok bool)

// RunWithKeys is Run with the menus driven by the keyboard: ↑ and ↓ move a highlight, Enter takes
// the highlighted option, and anything typed is still read as before - a number, a name, a model id
// - so nothing a user could do with whole lines is lost. A key being pasted is masked as it is
// typed.
//
// With a nil keys, or one that cannot switch the terminal, it is exactly Run.
func RunWithKeys(ctx context.Context, in io.Reader, out io.Writer, configPath string, preset Answers, now time.Time, keys KeyMode) (Result, error) {
	return runSetup(ctx, in, out, configPath, preset, now, keys)
}

// menu is a numbered list on screen that the arrows move a highlight through.
type menu struct {
	labels, notes []string
	width         int
	// first is the line the first row was written on, counted by the session's lineCounter, so a
	// redraw knows how far above the prompt the rows are.
	first int
	sel   int
}

// lineCounter counts the lines written through it. The menus are redrawn in place by moving the
// cursor up to their first row, and the distance is only known if every line since is counted.
type lineCounter struct {
	w io.Writer
	n int
}

func (c *lineCounter) Write(p []byte) (int, error) {
	c.n += bytes.Count(p, []byte{'\n'})
	return c.w.Write(p)
}

// showMenu writes a numbered list. With the keyboard driving it, the highlighted row carries the
// marker; without, the rows are written as they always were.
//
// The labels and notes are translated here, once, so the redraws (which write to the terminal
// directly) repeat the translated rows; the column is measured on what is actually shown.
func (s *session) showMenu(labels, notes []string) *menu {
	shownLabels := make([]string, len(labels))
	shownNotes := make([]string, len(notes))
	for i := range labels {
		shownLabels[i], shownNotes[i] = tr(s.out, labels[i]), tr(s.out, notes[i])
	}
	labels, notes = shownLabels, shownNotes
	m := &menu{labels: labels, notes: notes, width: labelWidth(labels)}
	if s.lines != nil {
		m.first = s.lines.n
	}
	for i := range labels {
		fmt.Fprintln(s.out, s.menuRow(m, i))
	}
	return m
}

// menuRow is one row of a menu, without its line break.
func (s *session) menuRow(m *menu, i int) string {
	return optionRow(i+1, m.labels[i], m.width, m.notes[i], s.lines != nil && i == m.sel)
}

// choose asks for an answer to a menu. With the keyboard it is the highlighted option unless
// something was typed; without, it is ask.
func (s *session) choose(ctx context.Context, prompt string, m *menu) (string, error) {
	return s.question(ctx, prompt, m, false)
}

// question is ask, with the menu it answers and whether the answer is a secret.
func (s *session) question(ctx context.Context, prompt string, m *menu, secret bool) (string, error) {
	// Translated here, before the keyboard path writes it to the raw terminal; a translation keeps
	// the "[1" that withDefault rewrites.
	prompt = tr(s.out, prompt)
	if s.lines == nil {
		return s.askLine(ctx, prompt)
	}
	restore, ok := s.keys()
	if !ok {
		return s.askLine(ctx, prompt)
	}
	defer restore()
	s.say("")
	text, err := s.edit(ctx, prompt, m, secret)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if strings.EqualFold(text, "q") || strings.EqualFold(text, "quit") {
		return "", ErrCancelled
	}
	return text, nil
}

// edit reads one answer a key at a time, drawing the line itself (the terminal's echo is off).
func (s *session) edit(ctx context.Context, prompt string, m *menu, secret bool) (string, error) {
	var buf []rune
	draw := func() {
		shown := string(buf)
		if secret {
			shown = strings.Repeat("•", len(buf))
		}
		fmt.Fprint(s.lines.w, "\r\x1b[2K")
		printPrompt(s.lines.w, withDefault(prompt, m))
		fmt.Fprint(s.lines.w, shown)
	}
	draw()
	for {
		b, err := s.readByte(ctx)
		if err != nil {
			return "", ErrCancelled
		}
		switch {
		case b == '\r' || b == '\n':
			// Enter on an empty line takes the highlighted option, and writes it on the line, so
			// what was chosen stays readable above the next question.
			if len(buf) == 0 && m != nil {
				buf = []rune(strconv.Itoa(m.sel + 1))
				draw()
			}
			fmt.Fprintln(s.out)
			return string(buf), nil
		case b == 0x03 || (b == 0x04 && len(buf) == 0):
			// Ctrl+C reaches the read only if the terminal does not turn it into a signal, and
			// Ctrl+D on an empty line is the end of the input: both leave, as EOF always has.
			fmt.Fprintln(s.out)
			return "", ErrCancelled
		case b == 0x7f || b == 0x08:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
			draw()
		case b == 0x1b:
			if m != nil && s.moveSelection(m, s.readEscape()) {
				s.redrawMenu(m)
				draw()
			}
		case b < 0x20:
			// Any other control key is not text.
		default:
			buf = append(buf, s.readRune(b))
			draw()
		}
	}
}

// withDefault writes the highlighted option into a prompt's "[1" default, so the prompt names what
// Enter will take as the highlight moves.
func withDefault(prompt string, m *menu) string {
	if m == nil {
		return prompt
	}
	return strings.Replace(prompt, "[1", "["+strconv.Itoa(m.sel+1), 1)
}

// moveSelection applies an arrow to the highlight, wrapping at both ends, and reports whether it
// moved.
func (s *session) moveSelection(m *menu, seq string) bool {
	n := len(m.labels)
	switch seq {
	case "\x1b[A", "\x1bOA":
		m.sel = (m.sel - 1 + n) % n
	case "\x1b[B", "\x1bOB":
		m.sel = (m.sel + 1) % n
	default:
		return false
	}
	return true
}

// redrawMenu rewrites the rows of a menu in place: up to the first row, each row erased and
// written, and back down to the prompt. The moves go to the terminal directly, uncounted, because
// they add no line.
func (s *session) redrawMenu(m *menu) {
	up := s.lines.n - m.first
	w := s.lines.w
	fmt.Fprintf(w, "\r\x1b[%dA", up)
	for i := range m.labels {
		fmt.Fprintf(w, "\x1b[2K%s\r\x1b[1B", s.menuRow(m, i))
	}
	if rest := up - len(m.labels); rest > 0 {
		fmt.Fprintf(w, "\x1b[%dB", rest)
	}
}

// readByte reads one key, giving up when the context is cancelled.
func (s *session) readByte(ctx context.Context) (byte, error) {
	ch := make(chan byteResult, 1)
	go func() {
		b, err := s.in.ReadByte()
		ch <- byteResult{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

type byteResult struct {
	b   byte
	err error
}

// readEscape completes a key that started with ESC: the terminal sends a whole sequence in one
// write, so what is already buffered is the rest of it, and a bare Escape has nothing behind it.
func (s *session) readEscape() string {
	seq := []byte{0x1b}
	if s.in.Buffered() == 0 {
		return string(seq)
	}
	intro, _ := s.in.ReadByte()
	if intro != '[' && intro != 'O' {
		// Not a sequence: a bare Escape, and the key typed after it is put back to be read as
		// itself.
		s.in.UnreadByte()
		return string(seq)
	}
	seq = append(seq, intro)
	for s.in.Buffered() > 0 && len(seq) < 16 {
		c, _ := s.in.ReadByte()
		seq = append(seq, c)
		if c >= 0x40 && c <= 0x7e {
			break
		}
	}
	return string(seq)
}

// readRune assembles one character from its first byte and the continuation bytes behind it. A
// broken sequence is kept as the replacement character rather than dropped silently.
func (s *session) readRune(first byte) rune {
	if first < utf8.RuneSelf {
		return rune(first)
	}
	buf := []byte{first}
	for !utf8.FullRune(buf) && s.in.Buffered() > 0 {
		c, _ := s.in.ReadByte()
		buf = append(buf, c)
	}
	r, _ := utf8.DecodeRune(buf)
	return r
}

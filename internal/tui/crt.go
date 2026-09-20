package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/config"
)

// crt draws the effect over streamed text: the phosphor colour and the typewriter reveal.
//
// It is deliberately small. An earlier version of this file also drew scanlines, a flicker, a
// vignette, static and a glow — five more effects that a character grid cannot really do and
// that, on a real screen, fought the text for attention. The verdict on seeing it was immediate:
// it looked like noise and made everything worse.
//
// What is left is what was asked for and what a terminal does WELL: colour, and text arriving one
// character at a time. The rest was REMOVED rather than switched off, because a feature nobody
// uses is not worth carrying, testing or reading.
type crt struct {
	cfg config.CRT
	// r, g, b are the phosphor, resolved once instead of parsed per glyph.
	r, g, b int
	// typedTarget is the full text being revealed, and typedAt how much of it has been shown, in
	// runes. typedAtFrac carries the fraction of a character left over between frames: without
	// the carry a reveal slower than one character per frame would stall at zero for ever,
	// because each frame's step rounds down.
	typedTarget string
	typedAt     int
	typedAtFrac float64
	// typing says whether a reveal is still catching up with its target.
	typing bool
	// speed is characters per second, kept as a float so a slow reveal does not round to zero.
	speed float64
	// lastAt is when the reveal was last advanced, which is how the elapsed time between frames
	// is measured without a timer of its own.
	lastAt time.Time
}

// newCRT builds the effect from its configuration. It returns nil when the effect is off, which
// is how every caller asks "is there anything to do?" with a single nil check.
func newCRT(c config.CRT) *crt {
	if !c.Enabled {
		return nil
	}
	r, g, b := c.RGB()
	speed := c.TypewriterCPS
	if speed <= 0 {
		speed = config.Default().CRT.TypewriterCPS
	}
	return &crt{cfg: c, r: r, g: g, b: b, speed: speed}
}

// --- colour -----------------------------------------------------------------

// paint colours a message body in the phosphor.
//
// It returns the text with the interface's own colour codes replaced, so a message reads in one
// colour instead of the interface's palette showing through. Empty in, empty out: a blank block
// stays blank rather than becoming an escape with nothing in it.
func (c *crt) paint(text string) string {
	if c == nil || text == "" {
		return text
	}
	var b strings.Builder
	// The existing escapes are dropped rather than nested: the phosphor is the point, and keeping
	// the interface's colours inside a green screen looks like a bug.
	for _, seg := range splitSGR(text) {
		b.WriteString(c.phosphor(seg.text))
	}
	return b.String()
}

// phosphor wraps one run of text in the screen colour.
func (c *crt) phosphor(s string) string {
	if s == "" {
		return ""
	}
	return "\x1b[38;2;" + strconv.Itoa(c.r) + ";" + strconv.Itoa(c.g) + ";" + strconv.Itoa(c.b) +
		"m" + s + "\x1b[0m"
}

// revealFrameInterval is how often a revealed frame is redrawn. It is the tick of the typewriter:
// short enough to look continuous, long enough that the reveal is not one frame after another with
// no time between them — which would show the answer at once and defeat the effect.
const revealFrameInterval = 30 * time.Millisecond

// --- the typewriter ---------------------------------------------------------

// startTyping begins revealing text, or retargets a reveal already running.
//
// Retargeting rather than restarting is what keeps a streamed reply readable: the text grows as
// chunks arrive, and a reveal that restarted on every chunk would stutter back to the beginning.
// The characters already shown stay shown.
func (c *crt) startTyping(text string) {
	c.typedTarget = text
	c.typing = true
	// A target shorter than what has been revealed means the text was replaced rather than
	// extended, so the reveal starts over from what is actually there.
	if c.typedAt > len([]rune(text)) {
		c.typedAt = 0
	}
}

// stopTyping ends the reveal, so the text is shown whole from then on.
func (c *crt) stopTyping() {
	c.typing = false
	c.typedTarget = ""
	c.typedAt = 0
}

// reveal advances the reveal by the time that has passed and returns the text to show so far.
func (c *crt) reveal(elapsed time.Duration) string {
	if !c.cfg.Typewriter || !c.typing {
		return c.typedTarget
	}
	runes := []rune(c.typedTarget)
	step := int(c.speed * elapsed.Seconds())
	if step < 1 {
		// A frame shorter than one character's worth of time still reveals one character at a
		// time once the interval has passed; the fraction is carried so a fast frame rate does
		// not stall the reveal at zero.
		c.typedAtFrac += c.speed * elapsed.Seconds()
		if c.typedAtFrac >= 1 {
			step = int(c.typedAtFrac)
			c.typedAtFrac -= float64(step)
		}
	}
	if step > 0 {
		c.typedAt += step
		if c.typedAt > len(runes) {
			c.typedAt = len(runes)
		}
	}
	if c.typedAt >= len(runes) {
		c.typing = false
	}
	return string(runes[:c.typedAt])
}

// typingInProgress reports whether the reveal is still catching up with its target.
func (c *crt) typingInProgress() bool {
	return c.cfg.Typewriter && c.typing
}

// tick returns the time since the previous reveal, and records this one.
//
// The reveal is driven by the repaints the interface already performs rather than by a ticker of
// its own: streamed text arriving IS a repaint, so a timer would only add a second source of
// frames that has to agree with the first. The first call has no previous frame and returns the
// interval that reveals a single character, so a reply never stalls waiting for a second frame.
func (c *crt) tick() time.Duration {
	now := time.Now()
	if c.lastAt.IsZero() {
		c.lastAt = now
		if c.speed > 0 {
			return time.Duration(float64(time.Second) / c.speed)
		}
		return time.Second
	}
	elapsed := now.Sub(c.lastAt)
	c.lastAt = now
	return elapsed
}

// --- splitting a line into its runs -----------------------------------------

// sgrSeg is a piece of a line between its escape codes.
type sgrSeg struct {
	text string
}

// splitSGR splits a line into the text between its SGR escapes, dropping the escapes themselves.
//
// The interface colours its own labels, and those codes cannot be nested inside the phosphor's:
// the last reset wins, so a line built by wrapping would lose its green at the first label. Taking
// the line apart and re-colouring each piece is what keeps the effect uniform.
func splitSGR(line string) []sgrSeg {
	var out []sgrSeg
	var cur strings.Builder
	for i := 0; i < len(line); {
		if line[i] == 0x1b {
			// An SGR sequence runs to its final byte, which is a letter.
			//
			// The '[' of a CSI sequence is NOT that byte, even though it falls in the same
			// range: it introduces the sequence. Skipping it explicitly is what the first
			// version missed, and the result was that the banner came out carrying its own
			// colours — "\x1b[0;97m" was cut after the bracket, leaving "0;97m" as text to be
			// printed. It was found by looking at the captured screen, not by any unit test.
			j := i + 1
			if j < len(line) && line[j] == '[' {
				j++
			}
			for j < len(line) && !isFinalByte(line[j]) {
				j++
			}
			if j < len(line) {
				j++
			}
			// Flush what came before the code and skip the code itself.
			if cur.Len() > 0 {
				out = append(out, sgrSeg{text: cur.String()})
				cur.Reset()
			}
			i = j
			continue
		}
		cur.WriteByte(line[i])
		i++
	}
	if cur.Len() > 0 {
		out = append(out, sgrSeg{text: cur.String()})
	}
	return out
}

// isFinalByte reports whether b ends a CSI sequence.
func isFinalByte(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}

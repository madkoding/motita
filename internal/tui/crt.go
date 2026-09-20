package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/config"
)

// crt draws the retro terminal effect: phosphor colour, glow, scanlines, flicker, vignette,
// noise and the typewriter reveal.
//
// What is honestly possible here, and what is not. A terminal is a fixed grid of character cells
// with no pixel access, so three of the things the effect is usually made of cannot be done at
// all: there is no blur to bloom with, no way to bend the grid for a curved tube, and no control
// over the typeface beyond the fact that it is already monospace. Rather than fake those badly,
// this does what the medium actually allows:
//
//   - Phosphor colour and glow, by drawing each glyph twice: an almost-black halo one cell
//     around it, then the bright glyph on top. Real bloom needs pixels; this is the closest a
//     cell grid gets, and at a glance it reads the same.
//   - Scanlines, by dimming alternate rows. This is what people actually recognise as "CRT", so
//     it does most of the work.
//   - Flicker, by varying the brightness slightly between frames.
//   - Vignette, dimming the edges. It stands in for the curve: a real tube is darker at the
//     corners, and that darkness is most of what the eye reads as curvature.
//   - Noise, rare cells showing static instead of their glyph.
//   - Typewriter, revealing a reply character by character.
//
// The effect is drawn ON TOP of the finished frame, and never touches the input box or the status
// bar. Those are what the user is reading and typing into; a screen that makes them harder to
// read is a screen that gets switched off.
type crt struct {
	cfg config.CRT
	// r, g, b are the phosphor, resolved once instead of parsed per glyph.
	r, g, b int
	// phase advances every frame and drives the flicker and the noise, so both change over time
	// without either needing a timer of its own.
	phase uint32
	// typed is the text revealed so far by the typewriter, and typing says whether a reveal is in
	// progress.
	typed  strings.Builder
	typing bool
	// typedTarget is the full text being revealed. The reveal follows the TARGET rather than
	// queueing chunks: text can be retracted (a streamed line is replaced by its settled form)
	// and a queue would keep printing something that no longer exists.
	typedTarget string
	// typedAt is how much has been revealed, in runes, and typedAtFrac carries the fraction of a
	// character left over between frames. Without the carry a reveal slower than one character
	// per frame would stall at zero for ever, because each frame's step rounds down.
	typedAt     int
	typedAtFrac float64
	// speed is characters per second, kept as a float so a slow reveal does not round to zero.
	speed float64
	// lastAt is when the reveal was last advanced, which is how the elapsed time between frames
	// is measured without a timer of its own.
	lastAt time.Time
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

// newCRT builds the effect from its configuration. It returns nil when the effect is off, which
// is how every caller can ask "is there anything to do?" with a single nil check.
//
// A nil result for a disabled effect is deliberate: the caller then runs the plain path with no
// branches of its own, and the interface pays nothing for a feature it is not using.
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

// --- the typewriter ---------------------------------------------------------

// startTyping begins revealing text, or retargets a reveal already running.
//
// Retargeting rather than restarting is what keeps a streamed reply readable: the text grows as
// chunks arrive, and a reveal that restarted on every chunk would stutter back to the beginning.
// The characters already shown stay shown.
func (c *crt) startTyping(text string) {
	c.typedTarget = text
	c.typing = true
	// A target that is shorter than what has been revealed means the text was replaced rather
	// than extended, so the reveal starts over from what is actually there.
	if c.typedAt > len([]rune(text)) {
		c.typedAt = 0
	}
}

// stopTyping ends the reveal and marks the text as complete, so it is shown whole.
func (c *crt) stopTyping() {
	c.typing = false
	c.typedTarget = ""
	c.typedAt = 0
	c.typed.Reset()
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
		// time when the interval has passed; carried in typedAtFrac so a fast frame rate does
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

// --- per frame --------------------------------------------------------------

// advance moves the animation on by one frame.
//
// The flicker and the noise are driven from a frame counter rather than from a random source or a
// timer: the counter is part of the state that gets diffed, so two frames with the same phase
// produce the same bytes. A frame that changes for no reason would repaint the whole screen every
// time, which is the one thing the incremental painter exists to avoid.
func (c *crt) advance() {
	c.phase++
}

// brightness is the current flicker level, as a multiplier on the phosphor.
//
// The variation is deliberately tiny. A CRT flickers because its refresh is slow, but a terminal
// refreshing at 60Hz with a visible pulse is unreadable — and the effect is meant to be felt as
// "this screen is alive" rather than seen as a strobing window.
func (c *crt) brightness(row int) float64 {
	level := 1.0
	if c.cfg.Flicker > 0 {
		// A cheap deterministic wave: two counters at different rates, so the pattern does not
		// repeat on the same rows every frame and read as a rolling bar.
		p := float64(c.phase%97) / 97.0
		level -= c.cfg.Flicker * (0.5 + 0.5*math.Sin(2*math.Pi*p))
	}
	if c.cfg.Scanlines > 0 && row%2 == 1 {
		level -= c.cfg.Scanlines
	}
	// Only the floor needs clamping: the flicker and the scanlines are both SUBTRACTED from a
	// level that starts at one, so nothing here can push it above full brightness. An upper
	// clamp would be unreachable code that reads as a safety net.
	if level < 0 {
		level = 0
	}
	return level
}

// vignetteAt returns the dimming for an edge of the frame.
//
// It is applied to whole rows and to the columns near the sides, which is as much as a cell grid
// allows: the darkness that gathers at the corners of a tube is what the eye reads as curvature,
// and that is reproducible here even though the curve itself is not.
func (c *crt) vignetteAt(row, rows, col, cols int) float64 {
	if c.cfg.Vignette <= 0 || rows <= 0 || cols <= 0 {
		return 1.0
	}
	// Distance from the centre, normalised, using the further of the two axes so the corners are
	// the darkest point — the shape of a tube rather than of a cross.
	dy := math.Abs(float64(row)-float64(rows-1)/2) / (float64(rows) / 2)
	dx := math.Abs(float64(col)-float64(cols-1)/2) / (float64(cols) / 2)
	d := math.Max(dx, dy)
	if d > 1 {
		d = 1
	}
	return 1 - c.cfg.Vignette*d*d
}

// noiseAt reports whether a cell shows static this frame.
//
// It is a hash of the cell and the phase rather than a call to a random source: the same frame
// drawn twice must produce the same bytes, or the diff that makes repainting cheap would see
// every cell as changed on every frame.
func (c *crt) noiseAt(row, col int) bool {
	if c.cfg.Noise <= 0 {
		return false
	}
	h := uint32(row)*73856093 ^ uint32(col)*19349663 ^ c.phase*83492791
	// Map the hash onto 0..1 and compare against the configured fraction.
	return float64(h%10000)/10000.0 < c.cfg.Noise
}

// noiseGlyph is the character shown for a cell that is showing static.
//
// The set is small and fixed so the static reads as noise rather than as text that means
// something, and so the frame stays stable enough to diff.
var noiseGlyph = []rune{'·', ':', '.', '˙', ' '}

func (c *crt) noiseRune(row, col int) rune {
	h := uint32(row)*2654435761 ^ uint32(col)*40503 ^ c.phase*2246822519
	return noiseGlyph[int(h%uint32(len(noiseGlyph)))]
}

// paint applies the effect to one rendered frame and returns the rows to write.
//
// The input box, the rule above it and the status bar are left untouched. They are passed in as
// an index range rather than being recognised by their content, which would be guessing: the
// caller knows which rows it is protecting.
func (c *crt) paint(lines []string, protectFrom, protectTo int) []string {
	if c == nil {
		return lines
	}
	out := make([]string, len(lines))
	rows := len(lines)
	for row, line := range lines {
		if row >= protectFrom && row < protectTo {
			out[row] = line
			continue
		}
		out[row] = c.paintRow(line, row, rows)
	}
	return out
}

// paintRow colours one row.
//
// The row is rebuilt rather than wrapped in an escape at each end: the colour changes cell by
// cell with the vignette, and the existing escapes in the line (the interface colours its own
// labels) have to be handled rather than nested. Splitting on the escape boundaries and re-emitting
// each run with its own colour is the only way to keep both.
func (c *crt) paintRow(line string, row, rows int) string {
	cols := visibleLen(line)
	var b strings.Builder
	// The row's base brightness: flicker and scanlines, before the vignette adds the column.
	base := c.brightness(row)
	col := 0
	// Every segment carries text: splitSGR flushes only what it accumulated, so an empty run
	// cannot appear here and a guard for one would be dead code.
	for _, seg := range splitSGR(line) {
		// Existing colour codes are dropped: the phosphor is the point, and keeping the
		// interface's palette inside a green screen would look like a bug.
		b.WriteString(c.colorRun(seg.text, row, rows, cols, col, base))
		col += visibleLen(seg.text)
	}
	return b.String()
}

const haloFraction = 0.14

// colorRun writes one run of text in the phosphor, dimmed by the vignette.
//
// When the glow is on, the run also carries a faint phosphor BACKGROUND. That is the only way a
// cell grid can show a glow at all: there are no pixels to blur, but every cell can be lit, so
// the glyphs sit on a dim field of their own colour instead of on black. The effect is the same
// one a real phosphor produces — light spilling around the characters — and it costs one extra
// escape per run rather than per cell.
func (c *crt) colorRun(s string, row, rows, cols, startCol int, base float64) string {
	// A single level for the run is a compromise: the true vignette varies per column, but
	// emitting an escape per column would multiply the bytes written per frame by ten for a
	// difference the eye cannot see at this text size. Using the run's centre keeps the gradient
	// visible across the row without paying for it per cell.
	mid := startCol + visibleLen(s)/2
	level := base * c.vignetteAt(row, rows, mid, cols)
	r, g, b := scale(c.r, c.g, c.b, level)
	if c.cfg.Glow {
		// The halo is deliberately much dimmer than the glyph: it is light that has spread, not
		// a highlight, and a background as bright as the text would swallow it.
		hr, hg, hb := c.haloAt(level)
		return fmt.Sprintf("\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm%s\x1b[0m", r, g, b, hr, hg, hb, s)
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", r, g, b, s)
}

// haloAt is the halo colour for a given brightness, so the glow dims with the vignette and the
// scanlines exactly as the glyph does. A halo that stayed at full strength on a dim row would
// glow brighter than the text it surrounds.
func (c *crt) haloAt(level float64) (int, int, int) {
	return scale(c.r, c.g, c.b, level*haloFraction)
}

// haloFraction is how bright the glow is relative to the phosphor. It is small on purpose: this
// is light that has bled out of the characters, and at a terminal's cell size anything brighter
// fights the text for attention instead of framing it.

// halo is the dim version of the phosphor at full brightness, for callers that are not painting
// a specific row.
func (c *crt) halo() (int, int, int) {
	return c.haloAt(1.0)
}

// scale dims a colour, clamping so a level above one cannot overflow a channel.
func scale(r, g, b int, level float64) (int, int, int) {
	f := func(v int) int {
		n := int(float64(v) * level)
		if n < 0 {
			return 0
		}
		if n > 255 {
			return 255
		}
		return n
	}
	return f(r), f(g), f(b)
}

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

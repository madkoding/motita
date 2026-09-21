package tui

import (
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/config"
)

// crt draws the typewriter sweep over streamed text.
//
// It is deliberately small. An earlier version of this file also drew a phosphor green, then a
// phosphor green with a glow, then scanlines, a flicker, a vignette and static on top — five
// more effects that a character grid cannot really do and that fought the text for attention.
// The verdict on seeing it was always the same: it looked like noise and made everything
// worse. Each round of feedback was followed by the same instruction: "remove it, I do not
// want dead code". What is left is the typewriter, because that is what was asked for and
// what a terminal does WELL: text arriving one character at a time, with a sweep of light
// running over the leading edge so the eye can follow the reveal.
type crt struct {
	cfg config.CRT
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
	// calls is how many times reveal() was invoked: tests assert the typewriter was given frames
	// to run by checking the counter is non-zero, which is what the previous shape broke.
	calls int
}

// newCRT builds the effect from its configuration. It returns nil when the effect is off, which
// is how every caller asks "is there anything to do?" with a single nil check.
func newCRT(c config.CRT) *crt {
	if !c.Enabled {
		return nil
	}
	speed := c.TypewriterCPS
	if speed <= 0 {
		speed = config.Default().CRT.TypewriterCPS
	}
	return &crt{cfg: c, speed: speed}
}

// --- colour -----------------------------------------------------------------

// paint colours a message body in the typewriter sweep: every character sits at #CCC, except
// the last three revealed ones, which brighten toward the leading edge (#DDD, #EEE, #FFF) so
// the reveal shows a sweep of light running over the text.
//
// The sweep is drawn only while the reveal is running. It marks the LEADING EDGE of text
// arriving, so once there is no edge to mark the whole body sits at the base colour: leaving
// the last three characters bright meant a finished answer ended in a pale patch that never
// went away, which reads as a rendering fault rather than as an effect. "It changes colour at
// the end" was exactly that patch.
//
// The sweep is the only effect on the message. Phosphor used to colour the whole body in
// green, which made the bright tail look like a separate highlight; making the whole body
// greyscale lets the sweep be the only colour.
//
// Empty in, empty out: a blank block stays blank rather than becoming an escape with nothing
// in it.
func (c *crt) paint(text string, sweeping bool) string {
	if c == nil || text == "" {
		return text
	}
	// The text reaches paint AFTER the typewriter has trimmed it to the revealed portion. The
	// sweep runs over those runes, not over the original message, because nothing past the
	// reveal is on the screen yet.
	runes := []rune(text)
	// cut splits the text into the quiet body and the bright tail. Without the sweep there is
	// no tail at all, so cut lands at the end and the whole body is painted at the base.
	cut := len(runes)
	if sweeping {
		cut = len(runes) - sweepWidth
		if cut < 0 {
			cut = 0
		}
	}
	head := runes[:cut]
	tail := runes[cut:]

	var b strings.Builder
	if len(head) > 0 {
		// The body sits at #CCC: visible but quiet, so the sweep reads as the only motion.
		b.WriteString(sweepBase)
		for _, seg := range splitSGR(string(head)) {
			b.WriteString(seg.text)
		}
	}
	for i, r := range tail {
		// i=0 is the OLDEST of the highlighted characters; the newest (the one just revealed)
		// gets the brightest colour. The progression fades back to the base.
		b.WriteString(sweepColour(i))
		b.WriteRune(r)
	}
	if len(tail) > 0 || len(head) > 0 {
		// Close the colour the sweep opened so the next plain row does not inherit it.
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// sweepWidth is how many characters the bright tail covers. Three is enough for the eye to see
// the leading edge without painting a long stripe of pale text that looks like a cursor glitch.
const sweepWidth = 3

// sweepBase is the colour of every revealed character that is NOT in the bright tail. #CCC is
// what makes the sweep the only colour on the screen.
const sweepBase = "\x1b[38;2;204;204;204m"

// sweepColour is the 24-bit colour of the i-th character of the sweep, counted from the oldest
// highlighted character toward the newest. The newest is the brightest.
func sweepColour(i int) string {
	switch i {
	case 0:
		return "\x1b[38;2;221;221;221m"
	case 1:
		return "\x1b[38;2;238;238;238m"
	default:
		return "\x1b[38;2;255;255;255m"
	}
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
//
// A reveal that STARTS also starts its clock. Time that passed before there was any text is not
// time spent revealing: the model spent it thinking, and the network spent it in flight.
// Counting it here is what made the FIRST LINE of an answer appear whole — the wait for the
// first token was converted into enough characters to fill a line, so the typewriter was
// invisible exactly where the reader looks first, and only the later lines looked typed.
//
// The clock is only reset on the idle→typing transition: while a reveal is catching up, the
// elapsed time is real revealing time and is what keeps the speed honest.
//
// A target that does not EXTEND the previous one is a replacement, and the reveal restarts from
// zero. A pending block is reused for the run's progress labels while the model works, and the
// answer then REPLACES that label — measured on the real interface, "a" followed by the whole
// answer kept two characters "already revealed", so the answer's first line came out pre-written
// and only the lines after it looked typed. Testing "is the new text shorter" catches only the
// case where the replacement is shorter, which is why the answer (longer than the label) slipped
// through. The question is not length but continuity: does the new text begin with what was
// already there?
func (c *crt) startTyping(text string) {
	// The characters already shown stay shown only while the new target still starts with
	// them; anything else means the block was rewritten, so the reveal starts over.
	if !strings.HasPrefix(text, c.typedTarget) {
		c.typedAt = 0
		c.typedAtFrac = 0
	}
	if !c.typing {
		c.lastAt = time.Time{}
	}
	c.typedTarget = text
	c.typing = true
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
//
// The advance is capped at one frame's worth of time. A repaint can be late for reasons that have
// nothing to do with revealing — the model thinking, the network, the terminal — and the clock
// measures that gap faithfully. Converting the whole gap into characters is what showed an entire
// answer on a single frame, and it is why the FIRST LINE of a reply looked pre-written while the
// lines after it were typed: the frame that carried the first token had been preceded by seconds
// of silence, and that silence was spent as if it had been revealing time.
//
// Measured on the real interface: a 3.5 s gap between frames revealed 76 of 76 characters at once,
// and the answer's first line was complete before the second frame was ever drawn.
//
// So the reveal is a RATE, not a catch-up. Each frame advances by at most the characters one frame
// is worth, and the frames come from the repaint loop that already exists (revealPending drives
// them at revealFrameInterval). The steady-state speed is therefore exactly the configured rate,
// and silence advances nothing.
func (c *crt) reveal(elapsed time.Duration) string {
	c.calls++
	if !c.cfg.Typewriter || !c.typing {
		return c.typedTarget
	}
	// A frame accounts for at most one frame's worth of time; a longer gap is silence.
	if elapsed > revealFrameInterval {
		elapsed = revealFrameInterval
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

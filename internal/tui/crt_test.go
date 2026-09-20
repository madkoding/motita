package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
)

func crtFor(t *testing.T, tweak func(*config.CRT)) *crt {
	t.Helper()
	c := config.Default().CRT
	if tweak != nil {
		tweak(&c)
	}
	got := newCRT(c)
	if got == nil {
		t.Fatal("the effect should be on by default")
	}
	return got
}

// --- switching it on and off ------------------------------------------------

// Off means nil, and a nil effect is what every caller checks: the interface then runs the plain
// path with no branches of its own and pays nothing for a feature it is not using.
func TestTheEffectIsOnByDefaultAndCanBeSwitchedOff(t *testing.T) {
	if newCRT(config.Default().CRT) == nil {
		t.Fatal("the effect must be on by default: it is the look the author intends")
	}
	off := config.Default().CRT
	off.Enabled = false
	if got := newCRT(off); got != nil {
		t.Fatalf("a disabled effect must produce nothing to do, got %+v", got)
	}
}

// A nil effect leaves the text alone, which is what makes "off" indistinguishable from the
// interface as it was before the feature existed.
func TestNilEffectLeavesTheTextAlone(t *testing.T) {
	var c *crt
	if got := c.paint("hola"); got != "hola" {
		t.Fatalf("a nil effect must not alter the text, got %q", got)
	}
}

// --- the phosphor -----------------------------------------------------------

// The text is drawn in the phosphor, and the interface's own colours are dropped: keeping them
// inside a green screen would look like a bug.
func TestTheTextIsDrawnInThePhosphor(t *testing.T) {
	c := crtFor(t, nil)
	got := c.paint("\x1b[36;47mhola\x1b[0m mundo")
	if !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("the text should carry true-colour phosphor, got %q", got)
	}
	if strings.Contains(got, "36;47") {
		t.Fatalf("the interface's own colour must be dropped, got %q", got)
	}
	if !strings.Contains(got, "hola") || !strings.Contains(got, "mundo") {
		t.Fatalf("the text must survive, got %q", got)
	}
}

// The colour is the configured one, channel by channel, and it is emitted as ONE escape per run
// rather than per character: this is the hot path of every frame.
func TestThePhosphorIsTheConfiguredColour(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Color = "#00ff00" })
	got := c.paint("abc")
	if !strings.HasPrefix(got, "\x1b[38;2;0;255;0m") {
		t.Fatalf("got %q, want the configured green", got)
	}
	if n := strings.Count(got, "\x1b[38;2;"); n != 1 {
		t.Fatalf("one run of text is one escape, got %d", n)
	}
}

// Empty in, empty out: a blank block stays blank rather than becoming an escape with nothing in
// it, which would add bytes to every frame for no visible change.
func TestEmptyTextStaysEmpty(t *testing.T) {
	c := crtFor(t, nil)
	if got := c.paint(""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := c.phosphor(""); got != "" {
		t.Fatalf("phosphor(\"\") = %q, want empty", got)
	}
}

// The escape scanner must skip the '[' that introduces a CSI sequence. Skipping only to the next
// "final byte" cut "\x1b[0;97m" after the bracket and left "0;97m" as text to be printed, which
// is how the banner came out carrying its own colours on the first real run. Found by looking at
// the captured screen, not by any unit test.
func TestTheEscapeScannerSkipsTheBracket(t *testing.T) {
	segs := splitSGR("\x1b[0;97mA\x1b[0;37mB")
	var texts []string
	for _, s := range segs {
		texts = append(texts, s.text)
	}
	if joined := strings.Join(texts, "|"); joined != "A|B" {
		t.Fatalf("segments = %q, want A and B with no escape debris", joined)
	}
	for _, s := range texts {
		if strings.Contains(s, "[") || strings.Contains(s, "m") {
			t.Fatalf("an escape leaked into the text: %q", s)
		}
	}
	// A line with no escapes is one segment, and an empty line is none.
	if got := splitSGR("solo texto"); len(got) != 1 || got[0].text != "solo texto" {
		t.Fatalf("got %+v", got)
	}
	if got := splitSGR(""); len(got) != 0 {
		t.Fatalf("an empty line has no segments, got %+v", got)
	}
}

// --- the typewriter ---------------------------------------------------------

// The reveal shows the text a character at a time rather than all at once.
//
// The target is much longer than one frame's worth of characters, because at a teletype speed a
// frame of a tenth of a second reveals about twenty of them: the point is that a long reply
// appears progressively, which is what a reader notices, rather than that any single frame is
// partial.
func TestTypewriterRevealsProgressively(t *testing.T) {
	c := crtFor(t, nil)
	long := strings.Repeat("x", 4000)
	c.startTyping(long)
	first := c.reveal(time.Second / 10)
	if n := len([]rune(first)); n >= 4000 || n < 2 {
		t.Fatalf("the first frame should show some but not all, got %d chars", n)
	}
	if !c.typingInProgress() {
		t.Fatal("a reveal catching up is in progress")
	}
	if last := c.reveal(time.Second * 60); last != long {
		t.Fatalf("the reveal must finish on the full text, got %d chars", len([]rune(last)))
	}
	if c.typingInProgress() {
		t.Fatal("a finished reveal is not in progress")
	}
}

// A growing target must not restart the reveal. Streamed text arrives in chunks, and a reveal
// that started over on each chunk would stutter back to the beginning.
func TestTypewriterRetargetsWithoutRestarting(t *testing.T) {
	c := crtFor(t, nil)
	c.startTyping("abcdef")
	c.reveal(time.Second)
	shown := len([]rune(c.reveal(0)))
	c.startTyping("abcdefghij")
	after := c.reveal(time.Second / 100)
	if len([]rune(after)) < shown {
		t.Fatalf("the reveal must not go backwards: %d chars then %d", shown, len([]rune(after)))
	}
}

// A target SHORTER than what has been shown means the text was replaced rather than extended, so
// the reveal starts over — otherwise it would show characters that are no longer there.
func TestTypewriterRestartsWhenTheTextIsReplaced(t *testing.T) {
	c := crtFor(t, nil)
	c.startTyping("una respuesta larga")
	c.reveal(time.Second * 5)
	if c.typedAt == 0 {
		t.Fatal("the reveal should have finished")
	}
	c.startTyping("corta")
	if c.typedAt != 0 {
		t.Fatalf("a shorter target must restart the reveal, typedAt = %d", c.typedAt)
	}
}

// Stopping clears the reveal, so the text is drawn whole from then on.
func TestStoppingTheTypewriterEndsTheReveal(t *testing.T) {
	c := crtFor(t, nil)
	c.startTyping("hola")
	c.reveal(time.Second / 1000)
	c.stopTyping()
	if c.typingInProgress() {
		t.Fatal("a stopped reveal is not in progress")
	}
	if got := c.reveal(time.Second); got != "" {
		t.Fatalf("after stopping, the target is cleared, got %q", got)
	}
}

// With the typewriter off the full text is returned immediately: the reveal is a presentation
// choice, not a buffer.
func TestTypewriterOffReturnsTheWholeText(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Typewriter = false })
	c.startTyping("completo")
	if got := c.reveal(time.Nanosecond); got != "completo" {
		t.Fatalf("got %q, want the whole text", got)
	}
	if c.typingInProgress() {
		t.Fatal("with the typewriter off nothing is in progress")
	}
}

// A speed of zero falls back to the default instead of dividing by zero or freezing the reveal.
func TestTypewriterSpeedFallsBackToTheDefault(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.TypewriterCPS = 0 })
	if c.speed != config.Default().CRT.TypewriterCPS {
		t.Fatalf("speed = %v, want the default", c.speed)
	}
}

// A reveal SLOWER than one character per frame must still advance. Each frame's step rounds down
// to zero, so the fraction is carried between frames; without the carry the reveal would stall at
// zero for ever on a fast repainting terminal.
func TestASlowRevealStillAdvances(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.TypewriterCPS = 5 })
	c.startTyping(strings.Repeat("y", 100))
	for i := 0; i < 220; i++ {
		c.reveal(time.Millisecond)
	}
	if c.typedAt == 0 {
		t.Fatal("a slow reveal must still advance: the fraction is carried between frames")
	}
}

// The reveal is driven by the repaints the interface already does, so the first call has no
// previous frame and must still move one character along rather than stalling.
func TestTheFirstTickStillAdvances(t *testing.T) {
	c := crtFor(t, nil)
	if got := c.tick(); got <= 0 {
		t.Fatalf("the first tick must be positive, got %v", got)
	}
	if c.lastAt.IsZero() {
		t.Fatal("the first tick must record the time")
	}
	if got := c.tick(); got < 0 {
		t.Fatalf("a subsequent tick must not be negative, got %v", got)
	}
}

// A nil effect has no speed to measure, and the tick must still answer with a usable interval
// rather than zero: a zero interval would make the first reveal show nothing at all.
func TestTickWithoutASpeed(t *testing.T) {
	c := &crt{speed: 0}
	if got := c.tick(); got != time.Second {
		t.Fatalf("tick = %v, want one second", got)
	}
}

// --- inside the interface ---------------------------------------------------

// A settled message is drawn whole, and a pending one is revealed: revealing finished text again
// would make a completed answer flicker back and forth.
func TestCrtTextRevealsOnlyThePendingMessage(t *testing.T) {
	c := crtFor(t, nil)
	tui := &TUI{crt: c}
	settled := Message{Author: AuthorAgent, Text: "ya terminado", Pending: false}
	if got := tui.crtText(settled); !strings.Contains(got, "ya terminado") {
		t.Fatalf("a settled message must be shown whole, got %q", got)
	}
	pending := Message{Author: AuthorAgent, Text: strings.Repeat("z", 2000), Pending: true}
	if got := tui.crtText(pending); len([]rune(got)) >= 2000 {
		t.Fatalf("a pending message must be revealed, got %d chars", len([]rune(got)))
	}
}

// A settled message stops a reveal that was still running, so the typewriter cannot be left
// chasing a target that will never grow again.
func TestASettledMessageStopsARunningReveal(t *testing.T) {
	c := crtFor(t, nil)
	c.startTyping(strings.Repeat("z", 5000))
	c.reveal(time.Second / 100)
	if !c.typingInProgress() {
		t.Fatal("the reveal should be running")
	}
	tui := &TUI{crt: c}
	tui.crtText(Message{Text: "terminado", Pending: false})
	if c.typingInProgress() {
		t.Fatal("a settled message must stop the reveal")
	}
}

// The colour is applied to a settled message too: it is the screen colour, not a decoration of
// the reveal.
func TestASettledMessageIsStillColoured(t *testing.T) {
	c := crtFor(t, nil)
	tui := &TUI{crt: c}
	if got := tui.crtText(Message{Text: "listo", Pending: false}); !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("a settled message must carry the phosphor, got %q", got)
	}
}

// With no effect there is nothing to reveal and nothing to colour: the text passes through.
func TestCrtTextWithoutAnEffect(t *testing.T) {
	tui := &TUI{}
	if got := tui.crtText(Message{Text: "texto", Pending: true}); got != "texto" {
		t.Fatalf("got %q", got)
	}
}

// The typewriter switched off still colours the text: they are two settings, and turning one off
// must not silently turn off the other.
func TestCrtTextWithTheTypewriterOffStillColours(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Typewriter = false })
	tui := &TUI{crt: c}
	got := tui.crtText(Message{Text: "texto", Pending: true})
	if !strings.Contains(got, "texto") || !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("got %q, want the text in the phosphor", got)
	}
}

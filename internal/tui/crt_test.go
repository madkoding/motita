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
	c.Flicker = 0
	c.Noise = 0
	c.Vignette = 0
	c.Scanlines = 0
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

// A nil effect passes the frame through untouched, which is what makes "off" indistinguishable
// from the interface as it was before the feature existed.
func TestNilEffectLeavesTheFrameAlone(t *testing.T) {
	var c *crt
	lines := []string{"uno", "dos"}
	if got := c.paint(lines, 0, 0); len(got) != 2 || got[0] != "uno" {
		t.Fatalf("a nil effect must not alter the frame, got %q", got)
	}
}

// --- what is protected ------------------------------------------------------

// The rows the user reads and types into are never touched. A screen that makes the input harder
// to read is a screen that gets switched off.
func TestInputAndStatusAreNeverPainted(t *testing.T) {
	c := crtFor(t, nil)
	lines := []string{"conversacion", "otra linea", "regla", "  input", "  status"}
	got := c.paint(lines, 3, 5)
	if got[3] != "  input" || got[4] != "  status" {
		t.Fatalf("the protected rows must be byte-identical, got %q and %q", got[3], got[4])
	}
	if got[0] == lines[0] {
		t.Fatal("the conversation should have been painted")
	}
}

// The boundary is derived from the end of the frame, which is how the layout composes it and the
// only definition that survives a popup or a resize.
func TestTheProtectedBoundaryIsMeasuredFromTheEnd(t *testing.T) {
	tui := &TUI{Width: 80, Height: 40}
	total := 30
	from := tui.crtProtectedFrom(total)
	if from != total-(inputRows+rowsBelowComposer+1) {
		t.Fatalf("from = %d, want the composer rows protected", from)
	}
	// It can never go negative, however small the frame is.
	if got := tui.crtProtectedFrom(1); got != 0 {
		t.Fatalf("from = %d, want 0 for a tiny frame", got)
	}
	if got := tui.crtProtectedFrom(0); got != 0 {
		t.Fatalf("from = %d, want 0 for an empty frame", got)
	}
}

// --- the phosphor -----------------------------------------------------------

// The whole line is painted in the phosphor, and the interface's own colours are dropped: keeping
// them inside a green screen would look like a bug.
func TestTheLineIsPaintedInThePhosphor(t *testing.T) {
	c := crtFor(t, nil)
	got := c.paintRow("\x1b[36;47mhola\x1b[0m mundo", 0, 10)
	if !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("the row should carry true-colour phosphor, got %q", got)
	}
	if strings.Contains(got, "36;47") {
		t.Fatalf("the interface's own colour must be dropped, got %q", got)
	}
	if !strings.Contains(got, "hola") || !strings.Contains(got, "mundo") {
		t.Fatalf("the text must survive, got %q", got)
	}
}

// The escape scanner must skip the '[' that introduces a CSI sequence. Skipping only to the next
// "final byte" cut "\x1b[0;97m" after the bracket and left "0;97m" as text to be printed, which
// is how the banner came out carrying its own colours on the first real run. Found by looking at
// the captured screen.
func TestTheEscapeScannerSkipsTheBracket(t *testing.T) {
	segs := splitSGR("\x1b[0;97mA\x1b[0;37mB")
	var texts []string
	for _, s := range segs {
		texts = append(texts, s.text)
	}
	joined := strings.Join(texts, "|")
	if joined != "A|B" {
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

// --- scanlines and the vignette ---------------------------------------------

// Alternate rows are dimmer, which is the effect people actually recognise as "CRT".
func TestScanlinesDimAlternateRows(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Scanlines = 0.5 })
	even := c.brightness(0)
	odd := c.brightness(1)
	if !(odd < even) {
		t.Fatalf("an odd row must be dimmer: even=%v odd=%v", even, odd)
	}
	if even != 1.0 {
		t.Fatalf("an even row without flicker must be full brightness, got %v", even)
	}
}

// The corners are the darkest point, which is the shape of a tube rather than of a cross — and it
// is the darkness the eye reads as curvature, since the curve itself cannot be drawn.
func TestVignetteDarkensTheCornersMost(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Vignette = 0.5 })
	centre := c.vignetteAt(5, 11, 10, 21)
	edge := c.vignetteAt(0, 11, 10, 21)
	corner := c.vignetteAt(0, 11, 0, 21)
	if !(corner < edge && edge < centre) {
		t.Fatalf("darkness must grow towards the corner: centre=%v edge=%v corner=%v", centre, edge, corner)
	}
	if centre > 1.0 {
		t.Fatalf("the centre must not be brightened, got %v", centre)
	}
}

// A vignette of zero leaves every cell at full brightness, so it costs nothing when off.
func TestNoVignetteMeansNoDimming(t *testing.T) {
	c := crtFor(t, nil)
	if got := c.vignetteAt(0, 11, 0, 21); got != 1.0 {
		t.Fatalf("vignetteAt = %v, want 1", got)
	}
	// A degenerate geometry must not divide by zero.
	if got := c.vignetteAt(0, 0, 0, 0); got != 1.0 {
		t.Fatalf("vignetteAt with no size = %v, want 1", got)
	}
}

// --- flicker ----------------------------------------------------------------

// The flicker stays a multiplier of the phosphor: it dims, never brightens past full, and never
// goes negative however it is configured.
func TestFlickerStaysWithinRange(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Flicker = 1.0 })
	for i := 0; i < 300; i++ {
		c.advance()
		v := c.brightness(0)
		if v < 0 || v > 1 {
			t.Fatalf("phase %d: brightness %v out of range", i, v)
		}
	}
	// With flicker off, every row is full brightness.
	plain := crtFor(t, nil)
	if got := plain.brightness(3); got != 1.0 {
		t.Fatalf("without flicker brightness must be 1, got %v", got)
	}
}

// The frame counter is what drives the flicker, and it must actually move or the screen is
// static; it must also be part of the state, so the same phase draws the same bytes.
func TestAdvanceMovesTheAnimation(t *testing.T) {
	c := crtFor(t, nil)
	before := c.phase
	c.advance()
	if c.phase == before {
		t.Fatal("advance must move the phase")
	}
}

// --- noise ------------------------------------------------------------------

// Noise is off at zero, and present in roughly the configured fraction of cells otherwise: it is
// a fraction rather than a switch because a film-level static would make the interface unreadable.
func TestNoiseIsOffAtZeroAndProportionalOtherwise(t *testing.T) {
	off := crtFor(t, nil)
	for i := 0; i < 50; i++ {
		if off.noiseAt(1, i) {
			t.Fatal("noise must be off when it is zero")
		}
	}
	c := crtFor(t, func(c *config.CRT) { c.Noise = 0.5 })
	hits := 0
	for row := 0; row < 40; row++ {
		for col := 0; col < 40; col++ {
			if c.noiseAt(row, col) {
				hits++
			}
		}
	}
	total := 40 * 40
	if hits == 0 || hits == total {
		t.Fatalf("noise at 0.5 should hit about half the cells, got %d/%d", hits, total)
	}
}

// The same frame must draw the same bytes: the incremental painter compares frames, and a noise
// source that changed on every read would make every cell differ every time.
func TestNoiseAndItsGlyphAreDeterministic(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Noise = 0.5 })
	first := make([]bool, 20)
	glyphs := make([]rune, 20)
	for i := range first {
		first[i] = c.noiseAt(3, i)
		glyphs[i] = c.noiseRune(3, i)
	}
	for i := range first {
		if c.noiseAt(3, i) != first[i] {
			t.Fatalf("noiseAt(%d) changed within the same phase", i)
		}
		if c.noiseRune(3, i) != glyphs[i] {
			t.Fatalf("noiseRune(%d) changed within the same phase", i)
		}
	}
	// And it does move with the phase, or the static would be frozen.
	c.advance()
	different := false
	for i := range first {
		if c.noiseAt(3, i) != first[i] {
			different = true
			break
		}
	}
	if !different {
		t.Fatal("the noise must change between frames")
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
	if len([]rune(first)) >= 4000 {
		t.Fatalf("the first frame must not show everything, got %d chars", len([]rune(first)))
	}
	if len([]rune(first)) < 2 {
		t.Fatalf("the first frame must show something, got %d chars", len([]rune(first)))
	}
	if !c.typingInProgress() {
		t.Fatal("a reveal catching up is in progress")
	}
	last := c.reveal(time.Second * 60)
	if last != long {
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

// Stopping shows the text whole from then on, which is what a settled message needs.
func TestStoppingTheTypewriterShowsEverything(t *testing.T) {
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

// --- inside the interface ---------------------------------------------------

// A settled message is drawn whole, and a pending one is revealed: revealing finished text again
// would make a completed answer flicker back and forth.
func TestCrtTextRevealsOnlyThePendingMessage(t *testing.T) {
	c := crtFor(t, nil)
	tui := &TUI{crt: c}
	settled := Message{Author: AuthorAgent, Text: "ya terminado", Pending: false}
	if got := tui.crtText(settled); got != "ya terminado" {
		t.Fatalf("a settled message must be shown whole, got %q", got)
	}
	pending := Message{Author: AuthorAgent, Text: "escribiendo ahora mismo", Pending: true}
	if got := tui.crtText(pending); len([]rune(got)) >= len([]rune(pending.Text)) {
		t.Fatalf("a pending message must be revealed, got %q", got)
	}
}

// With no effect there is nothing to reveal, and the text passes through.
func TestCrtTextWithoutAnEffect(t *testing.T) {
	tui := &TUI{}
	if got := tui.crtText(Message{Text: "texto", Pending: true}); got != "texto" {
		t.Fatalf("got %q", got)
	}
}

// The typewriter switched off must also pass text through, even with the effect on.
func TestCrtTextWithTheTypewriterOff(t *testing.T) {
	tui := &TUI{crt: crtFor(t, func(c *config.CRT) { c.Typewriter = false })}
	if got := tui.crtText(Message{Text: "texto", Pending: true}); got != "texto" {
		t.Fatalf("got %q", got)
	}
}

// --- drawing helpers --------------------------------------------------------

// scale clamps, so a level above one cannot overflow a channel and a negative one cannot wrap.
func TestScaleClamps(t *testing.T) {
	if r, g, b := scale(255, 255, 255, 2.0); r != 255 || g != 255 || b != 255 {
		t.Fatalf("scale must clamp upwards, got %d %d %d", r, g, b)
	}
	if r, g, b := scale(255, 255, 255, -1.0); r != 0 || g != 0 || b != 0 {
		t.Fatalf("scale must clamp downwards, got %d %d %d", r, g, b)
	}
	if r, g, b := scale(100, 100, 100, 0.5); r != 50 || g != 50 || b != 50 {
		t.Fatalf("scale must be proportional, got %d %d %d", r, g, b)
	}
}

// The halo is a dim version of the phosphor: never brighter, and the same hue — every channel
// scaled by the same factor, which is what keeps it a glow of the same colour rather than a
// different one.
func TestHaloIsADimmerPhosphor(t *testing.T) {
	c := crtFor(t, nil)
	hr, hg, hb := c.halo()
	wantR, wantG, wantB := scale(c.r, c.g, c.b, haloFraction)
	if hr != wantR || hg != wantG || hb != wantB {
		t.Fatalf("halo = %d,%d,%d, want %d,%d,%d", hr, hg, hb, wantR, wantG, wantB)
	}
	if c.g > 0 && hg >= c.g {
		t.Fatalf("the halo must be dimmer: halo=%d phosphor=%d", hg, c.g)
	}
}

// The halo dims with the row, exactly as the glyph does. A halo that kept full strength on a
// scanline row would glow brighter than the text it is supposed to surround.
func TestTheHaloFollowsTheRowBrightness(t *testing.T) {
	c := crtFor(t, nil)
	_, bright, _ := c.haloAt(1.0)
	_, dim, _ := c.haloAt(0.4)
	if !(dim < bright) {
		t.Fatalf("the halo must dim with its row: bright=%d dim=%d", bright, dim)
	}
}

// The glow is what makes the halo visible, and it has to actually be drawn: the option existed
// and was never read, so turning it on changed nothing. Measured on the real binary: with it on
// the output carries a phosphor background on every painted run, and with it off none.
func TestTheGlowPutsTheHaloBehindTheText(t *testing.T) {
	withGlow := crtFor(t, func(c *config.CRT) { c.Glow = true })
	got := withGlow.colorRun("hola", 0, 10, 20, 0, 1.0)
	if !strings.Contains(got, "48;2;") {
		t.Fatalf("the glow must paint a background, got %q", got)
	}
	// Foreground and background in ONE escape: two escapes would reset between them and the
	// halo would be lost on the second half of the run.
	if strings.Count(got, "\x1b[") != 2 {
		t.Fatalf("the run should be one escape, the text and a reset, got %q", got)
	}

	noGlow := crtFor(t, func(c *config.CRT) { c.Glow = false })
	plain := noGlow.colorRun("hola", 0, 10, 20, 0, 1.0)
	if strings.Contains(plain, "48;2;") {
		t.Fatalf("without the glow there must be no background, got %q", plain)
	}
}

// paintRow over an empty line produces an empty line, which is what keeps blank rows blank.
func TestPaintingAnEmptyRow(t *testing.T) {
	c := crtFor(t, nil)
	if got := c.paintRow("", 0, 10); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// --- the branches that keep the effect robust -------------------------------

// A reveal SLOWER than one character per frame must still advance. Each frame's step rounds down
// to zero, so the fraction is carried between frames; without the carry the reveal would stall at
// zero for ever on a fast repainting terminal.
func TestASlowRevealStillAdvances(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.TypewriterCPS = 5 })
	c.startTyping(strings.Repeat("y", 100))
	// Twenty frames of 1ms: at 5 cps that is a tenth of a character, so nothing should appear
	// from any single frame, but the carry has to accumulate.
	for i := 0; i < 20; i++ {
		c.reveal(time.Millisecond)
	}
	// Two hundred more frames is a full character's worth of time.
	for i := 0; i < 200; i++ {
		c.reveal(time.Millisecond)
	}
	if c.typedAt == 0 {
		t.Fatal("a slow reveal must still advance: the fraction is carried between frames")
	}
}

// The flicker and the scanlines together cannot push the level above full or below zero, however
// both are configured: a level outside that range would produce an invalid colour channel.
func TestBrightnessIsClampedWithEverythingAtMaximum(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) {
		c.Flicker = 1.0
		c.Scanlines = 1.0
	})
	for i := 0; i < 200; i++ {
		c.advance()
		if v := c.brightness(1); v < 0 || v > 1 {
			t.Fatalf("phase %d: brightness %v out of range with everything at maximum", i, v)
		}
	}
}

// The distance saturates at the corner, so a cell far outside the frame is never darker than the
// floor — and nothing here indexes past anything, because the distance is computed and clamped
// rather than looked up.
//
// The corner of an NxN frame is NOT at distance 1: it is at (N-1)/N, which only reaches 1 as N
// grows. That is why the cell beyond the frame is the darker of the two, and comparing them for
// equality was the first version of this test getting the geometry wrong.
func TestVignetteDistanceSaturates(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Vignette = 0.5 })
	floor := 1 - c.cfg.Vignette
	corner := c.vignetteAt(0, 10, 0, 10)
	far := c.vignetteAt(99, 10, 99, 10)

	if far != floor {
		t.Fatalf("a cell past the corner must sit at the floor %v, got %v", floor, far)
	}
	if corner < floor {
		t.Fatalf("nothing may be darker than the floor: corner=%v floor=%v", corner, floor)
	}
	if corner > 1 || far > 1 {
		t.Fatalf("dimming out of range: corner=%v far=%v", corner, far)
	}
	// And a bigger frame gets closer to the floor at its corner, which is the shape of a tube.
	wide := c.vignetteAt(0, 100, 0, 100)
	if wide >= corner {
		t.Fatalf("a larger frame's corner should be darker: %v vs %v", wide, corner)
	}
}

// An empty segment inside a line is skipped rather than emitted as an escape with no text, which
// would add bytes to every frame for nothing.
func TestEmptySegmentsAreSkipped(t *testing.T) {
	c := crtFor(t, nil)
	// Two escapes back to back produce an empty segment between them.
	got := c.paintRow("a\x1b[0m\x1b[32mb", 0, 10)
	if strings.Count(got, "\x1b[38;2;") != 2 {
		t.Fatalf("only the two runs with text should be painted, got %q", got)
	}
}

// The effect is built from the configuration the runner holds, and NoColor wins over it: an
// interface told to render without colour cannot draw a phosphor screen.
func TestTheEffectIsBuiltFromTheRunnerUnlessColourIsOff(t *testing.T) {
	r := &stubConfigRunner{cfg: config.Default()}
	tui := &TUI{Out: &strings.Builder{}, Width: 80, Height: 40, Runner: r}
	if got := newCRT(tui.Runner.Config().CRT); got == nil {
		t.Fatal("a default configuration has the effect on")
	}
	tui.NoColor = true
	// The rule Run() applies: NoColor suppresses the effect whatever the configuration says.
	var built *crt
	if !tui.NoColor {
		built = newCRT(tui.Runner.Config().CRT)
	}
	if built != nil {
		t.Fatal("NoColor must suppress the effect")
	}
}

// stubConfigRunner answers Config() and nothing else, for the tests that only need the settings.
type stubConfigRunner struct {
	stubRunner
	cfg config.Config
}

func (s *stubConfigRunner) Config() config.Config { return s.cfg }

// A nil effect has no speed to measure, and the tick must still answer with a usable interval
// rather than zero: a zero interval would make the first reveal show nothing at all.
func TestTickWithoutASpeed(t *testing.T) {
	c := &crt{speed: 0}
	if got := c.tick(); got != time.Second {
		t.Fatalf("tick = %v, want one second", got)
	}
}

// A level above one is clamped rather than emitted: a channel over 255 is not a colour, and the
// scanlines and the flicker are subtracted rather than measured, so the combination can go below
// zero on a row that is both dark and dim.
func TestBrightnessIsClampedInBothDirections(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) {
		c.Flicker = 1.0
		c.Scanlines = 1.0
	})
	sawFloor := false
	for i := 0; i < 200; i++ {
		c.advance()
		v := c.brightness(1)
		if v < 0 || v > 1 {
			t.Fatalf("phase %d: brightness %v out of range", i, v)
		}
		if v == 0 {
			sawFloor = true
		}
	}
	if !sawFloor {
		t.Error("a row with the scanline and the flicker both at maximum should reach the floor")
	}
	// The ceiling is reached when nothing dims the row: with flicker at zero an even row is full
	// brightness, which is the top of the range the clamp has to protect.
	plain := crtFor(t, func(c *config.CRT) { c.Scanlines = 1.0 })
	if got := plain.brightness(0); got != 1.0 {
		t.Fatalf("an even row without flicker must be full brightness, got %v", got)
	}
}

// A segment that carries no text is skipped, which is what keeps a line of back-to-back escapes
// from emitting empty colour runs on every frame.
func TestPaintRowSkipsEmptySegments(t *testing.T) {
	c := crtFor(t, nil)
	// The middle escape produces an empty segment between the two runs of text.
	got := c.paintRow("a\x1b[0m\x1b[0mb", 0, 10)
	if strings.Count(got, "\x1b[38;2;") != 2 {
		t.Fatalf("only runs with text should be painted, got %q", got)
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
	if got := tui.crtText(Message{Text: "terminado", Pending: false}); got != "terminado" {
		t.Fatalf("got %q", got)
	}
	if c.typingInProgress() {
		t.Fatal("a settled message must stop the reveal")
	}
}

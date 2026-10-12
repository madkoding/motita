package tui

import (
	"strings"
	"testing"
)

// TestTheStatusIsReadableWithoutColour: the design guide counts colour-only
// indicators as an accessibility failure. Each state must differ in SHAPE as well
// as colour, so the line still means something with the escapes stripped — which
// is exactly what NO_COLOR produces.
func TestTheStatusIsReadableWithoutColour(t *testing.T) {
	// cfgSet matters: the fake substitutes config.Default() whenever no
	// configuration was supplied, so a test that assigns cfg without setting it
	// silently gets the defaults and asserts nothing about its own fixture.
	ready, _ := newKeyTUI("")
	ready.Runner = &fakeRunner{cfg: configWithKey("sk-live"), cfgSet: true}
	missing, _ := newKeyTUI("")
	missing.Runner = &fakeRunner{cfg: configWithKey(""), cfgSet: true}
	busy, _ := newKeyTUI("")
	busy.Runner = &fakeRunner{cfg: configWithKey("sk-live"), cfgSet: true}
	busy.busy = true

	readyGlyph := stripANSI(ready.stateGlyph())
	missingGlyph := stripANSI(missing.stateGlyph())

	if readyGlyph == missingGlyph {
		t.Errorf("ready and missing look identical without colour: %q", readyGlyph)
	}
	if readyGlyph != glyphReady {
		t.Errorf("ready glyph = %q, want %q", readyGlyph, glyphReady)
	}
	if missingGlyph != glyphMissing {
		t.Errorf("missing glyph = %q, want %q", missingGlyph, glyphMissing)
	}
	// A running turn is said in words where the work is - on the input box - rather than by a
	// third shape in the corner.
	if top := stripANSI(busy.boxTop(60)); !strings.Contains(top, "working") {
		t.Errorf("a running turn must say so on the input box without colour: %q", top)
	}
}

// TestAProviderWithItsOwnLoginIsReady: claude-code has no key of motita's by design, so a
// missing key is not "nothing to talk to" there.
func TestAProviderWithItsOwnLoginIsReady(t *testing.T) {
	cfg := configWithKey("")
	cfg.LLM.Provider = "claude-code"
	tu, _ := newKeyTUI("")
	tu.Runner = &fakeRunner{cfg: cfg, cfgSet: true}
	if got := stripANSI(tu.stateGlyph()); got != glyphReady {
		t.Errorf("glyph = %q, want %q", got, glyphReady)
	}
}

// TestTheWholeStatusLineSurvivesNoColour: stripping the escapes must leave the
// information intact, not an empty line. This is the frame a user with NO_COLOR
// actually reads.
func TestTheWholeStatusLineSurvivesNoColour(t *testing.T) {
	tu, out := newKeyTUI("")
	tu.Runner = &fakeRunner{cfg: configWithKey("sk-live"), cfgSet: true}
	tu.NoColor = true
	tu.Width, tu.Height = 100, 30

	out.Reset()
	tu.drawFrame()
	body := out.String()

	// NO_COLOR governs COLOUR, not the cursor and erase sequences a frame needs in
	// order to be redrawn at all. The check is for SGR sequences specifically: an
	// assertion of "no escape at all" would fail on the very redraw that makes the
	// interface work.
	if sgr.MatchString(body) {
		t.Errorf("NO_COLOR must not emit colour: %q", body)
	}
	// The status line carries the model and the reasoning level; the key is deliberately not
	// reported any more, so it is not asserted here.
	for _, want := range []string{glyphReady, "reasoning", "gpt-4o-mini"} {
		if !strings.Contains(body, want) {
			t.Errorf("the status line lost %q without colour: %q", want, body)
		}
	}
}

// TestTheWordmarkLosesItsOwnColourToo: the wordmark is coloured with the brand
// colour. NO_COLOR has to strip that as well, or the "no colour" mode still emits
// sequences the terminal will interpret.
func TestTheWordmarkLosesItsOwnColourToo(t *testing.T) {
	tu, out := newKeyTUI("")
	tu.Width, tu.Height = 100, 30

	tu.NoColor = false
	out.Reset()
	tu.drawFrame()
	if !strings.Contains(out.String(), "motita") {
		t.Error("the wordmark text must be present in colour mode")
	}

	tu.NoColor = true
	out.Reset()
	tu.drawFrame()
	if sgr.MatchString(out.String()) {
		t.Errorf("NO_COLOR must strip the wordmark's own colours: %q", out.String())
	}
	// The wordmark text itself must still be there, just uncoloured.
	if !strings.Contains(stripANSI(out.String()), "motita") {
		t.Error("the wordmark text must survive the strip")
	}
}

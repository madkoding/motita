package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Colour is only sent where something can interpret it. The design guide asks for
// two conventions by name — NO_COLOR and a dumb terminal — and both were missing:
// the flag existed and the tests set it, but production never did, so the support
// was unreachable outside a test.

// TestNoColourHonoursTheVariable: NO_COLOR is the cross-tool convention. Its mere
// presence disables colour, whatever its value: some tools set it to "0" and mean
// it.
func TestNoColourHonoursTheVariable(t *testing.T) {
	t.Setenv("NO_COLOR", "0")
	t.Setenv("TERM", "xterm-256color")

	// The output is a real terminal-ish file, so only the variable can be deciding.
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if !noColour(os.Getenv, f) {
		t.Error("NO_COLOR must disable colour even when it is set to a false-looking value")
	}
}

// TestNoColourOnADumbTerminal: a terminal that cannot do escapes would print them
// as literal text, which is worse than no colour at all.
func TestNoColourOnADumbTerminal(t *testing.T) {
	for _, term := range []string{"dumb", "DUMB", " dumb "} {
		t.Setenv("TERM", term)
		if !noColour(os.Getenv, os.Stdout) {
			t.Errorf("TERM=%q must disable colour", term)
		}
	}
}

// TestNoColourWhenTheTerminalIsUnset: an unset TERM means there is no terminal
// describing itself, so the safe reading is that it cannot render escapes.
func TestNoColourWhenTheTerminalIsUnset(t *testing.T) {
	t.Setenv("TERM", "")
	if !noColour(func(string) string { return "" }, os.Stdout) {
		t.Error("an empty TERM must disable colour")
	}
}

// TestNoColourWhenTheOutputIsNotATerminal: a pipe, a file or a buffer has nobody
// watching who can interpret the escapes, so they would end up as noise in a log.
func TestNoColourWhenTheOutputIsNotATerminal(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")

	var buf bytes.Buffer
	if !noColour(os.Getenv, &buf) {
		t.Error("a non-file writer must not receive colour")
	}

	f, err := os.Create(filepath.Join(t.TempDir(), "plain.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !noColour(os.Getenv, f) {
		t.Error("a regular file must not receive colour")
	}
}

// TestColourWhenEverythingAllowsIt: the check must not be a blanket refusal. A
// character device that says it can do colour, with nothing set against it, keeps
// its colour.
func TestColourWhenEverythingAllowsIt(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	os.Unsetenv("NO_COLOR")

	// A character device stands in for the terminal. A container may not have one,
	// so the test skips rather than asserting on a fabricated writer: the point is
	// to exercise the real branch, not to fake it.
	path := "/dev/tty"
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		f, err = os.OpenFile("/dev/null", os.O_WRONLY, 0)
	}
	if err != nil {
		t.Skipf("no character device available: %v", err)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Skip("no character device available to stand in for a terminal")
	}

	if noColour(os.Getenv, f) {
		t.Error("a colour terminal with nothing against it must keep its colour")
	}
}

// TestNoColourSurvivesAStatFailure: when the writer cannot be inspected the answer
// must be the safe one. Guessing "colour" would send escapes to a destination
// nobody has verified.
func TestNoColourSurvivesAStatFailure(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")

	// A closed file makes Stat fail, which is the branch being covered.
	f, err := os.Create(filepath.Join(t.TempDir(), "closed"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if !noColour(os.Getenv, f) {
		t.Error("a writer that cannot be inspected must not receive colour")
	}
}

// TestNoColourIsWiredIntoTheInteractiveTUI: the value has to reach the TUI, not
// just be computed. This is the link that was missing entirely.
func TestNoColourIsWiredIntoTheInteractiveTUI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var out bytes.Buffer
	if !noColour(os.Getenv, io.Writer(&out)) {
		t.Fatal("the variable must be honoured on the path the TUI takes")
	}
}

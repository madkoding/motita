package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/termcap"
	"github.com/madkoding/motita/internal/tui"
)

// Colour is only sent where something can interpret it. The rules themselves are termcap's and are
// tested there; these tests pin that the interface is handed the decision for the output it
// actually writes to.

// nullDevice is a character device standing in for a terminal.
func nullDevice(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no character device: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	if info, err := f.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Skip("the null device is not a character device here")
	}
	return f
}

// TestNoColourHonoursTheVariable: NO_COLOR is the cross-tool convention, and some tools set it to
// "0" and mean it.
func TestNoColourHonoursTheVariable(t *testing.T) {
	t.Setenv("NO_COLOR", "0")
	t.Setenv("TERM", "xterm-256color")
	if outputProfile(nullDevice(t)).Color() {
		t.Error("NO_COLOR must disable colour even when it is set to a false-looking value")
	}
}

// TestNoColourWhenTheOutputIsNotATerminal: a pipe, a file or a buffer has nobody watching who can
// interpret the escapes, so they would end up as noise in a log.
func TestNoColourWhenTheOutputIsNotATerminal(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	os.Unsetenv("NO_COLOR")
	os.Unsetenv("FORCE_COLOR")
	os.Unsetenv("MOTITA_COLOR")
	if outputProfile(&bytes.Buffer{}).Color() {
		t.Error("a non-file writer must not receive colour")
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "plain.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if outputProfile(f).Color() {
		t.Error("a regular file must not receive colour")
	}
}

// TestColourReachesTheInterface: a colour terminal with nothing against it keeps its colour, and
// the interface writes straight to it.
func TestColourReachesTheInterface(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	os.Unsetenv("NO_COLOR")
	os.Unsetenv("FORCE_COLOR")
	os.Unsetenv("MOTITA_COLOR")
	os.Unsetenv("MOTITA_ASCII")
	tty := nullDevice(t)
	ui := tui.New(nil)
	ui.NoColor = true
	ui.Out = tty
	applyProfile(ui, tty)
	if ui.NoColor || ui.Out != tty {
		t.Errorf("a colour terminal: NoColor=%v, Out replaced=%v", ui.NoColor, ui.Out != tty)
	}
}

// TestTheASCIIFilterReachesTheInterface: when the glyphs cannot be trusted the interface writes
// through the filter, which still counts as the terminal behind it.
func TestTheASCIIFilterReachesTheInterface(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("MOTITA_ASCII", "1")
	os.Unsetenv("NO_COLOR")
	tty := nullDevice(t)
	ui := tui.New(nil)
	ui.Out = tty
	applyProfile(ui, tty)
	if ui.Out == tty {
		t.Fatal("the interface must write through the ASCII filter")
	}
	if !termcap.IsTerminal(ui.Out) {
		t.Error("the filter must not hide the terminal from the mouse code")
	}
}

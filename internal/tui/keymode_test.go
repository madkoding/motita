package tui

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"testing"
)

// The setup is driven by the keys only from a terminal: from a pipe, a file or a test's reader it
// reads lines, which is what a script answering it needs.
func TestKeyModeIsOnlyForATerminal(t *testing.T) {
	if KeyModeFor(strings.NewReader("")) != nil || KeyModeFor(&bytes.Buffer{}) != nil {
		t.Error("a reader that is not a file is not a terminal")
	}
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipeR.Close()
	defer pipeW.Close()
	if KeyModeFor(pipeR) != nil {
		t.Error("a pipe is not a terminal")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the null device is not a character device here")
	}
	// The null device IS a character device, so it gets the switch - which, with no controlling
	// terminal to switch, reports that it could not and restores nothing.
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	keys := KeyModeFor(null)
	if keys == nil {
		t.Fatal("a character device must be offered the switch")
	}
	restore, _ := keys()
	restore()
}

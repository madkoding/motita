package tui

import (
	"context"
	"strings"
	"testing"
)

// TestAMultiLinePasteIsOneMessage: in character mode every newline ended the line, so a pasted
// "line1\nline2\nline3" was sent as three messages and the queue kept only the last one. A
// bracketed paste is draft text: its newlines become spaces and only Enter sends it.
func TestAMultiLinePasteIsOneMessage(t *testing.T) {
	tu, _ := newKeyTUI(pasteStart + "line1\nline2\r\nq\n" + pasteEnd + "\r")
	tu.charMode = true

	line, ok := tu.readLineLive(context.Background())
	if !ok || line != "line1 line2 q" {
		t.Fatalf("readLineLive = %q, %v; want the whole paste as one line", line, ok)
	}
}

// TestAPasteWithoutEnterIsNotSent: the paste only fills the draft, so input that ends right after
// it sends nothing.
func TestAPasteWithoutEnterIsNotSent(t *testing.T) {
	tu, _ := newKeyTUI(pasteStart + "/quit\n" + pasteEnd)
	tu.charMode = true

	if line, ok := tu.readLineLive(context.Background()); ok {
		t.Fatalf("a paste with no Enter was dispatched as %q", line)
	}
}

// TestAPasteCutShortKeepsWhatArrived: input that ends inside a paste must not hang the reader.
func TestAPasteCutShortKeepsWhatArrived(t *testing.T) {
	tu, _ := newKeyTUI(pasteStart + "abc")
	tu.charMode = true

	if _, ok := tu.readLineLive(context.Background()); ok {
		t.Fatal("the input ended, so no line must be dispatched")
	}
}

// TestPasteMarksAreOnlyRequestedOnATerminal: a buffer is not a terminal, so it gets no request.
func TestPasteMarksAreOnlyRequestedOnATerminal(t *testing.T) {
	tu, out := newKeyTUI("")
	tu.setPaste(true)
	tu.setPaste(false)
	if strings.Contains(out.String(), "2004") {
		t.Errorf("a buffer must not receive the paste request: %q", out.String())
	}

	dev, cleanup, ok := openCharacterDevice(t)
	if !ok {
		t.Skip("no character device available to write to")
	}
	defer cleanup()
	tu = &TUI{Out: dev}
	tu.setPaste(true)
	tu.setPaste(false)
}

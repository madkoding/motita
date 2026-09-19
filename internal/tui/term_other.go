//go:build !unix

package tui

// Windows has no `stty` and no /dev/tty: its console is put into character mode through
// ReadConsoleInput, which the standard library does not expose.
//
// So the interface keeps reading whole lines there, and the live completion popup — which
// needs every keystroke as it is typed — does not appear. The slash commands still work when
// typed in full and run with Enter, because that path goes through the ordinary line reader.
// Claiming more than that would be a promise the platform cannot keep.
type terminalMode struct {
	// active is always false here, and it is part of the type rather than omitted so the
	// portable code can ask the question without a build-tagged branch: the TUI reads it to
	// choose between the live reader and the whole-line one. Leaving it out is what broke the
	// Windows build — the field existed only on the Unix side of the tag, and a build tag hides
	// a symbol from the platforms it excludes rather than defaulting it.
	active bool
}

func enterRaw() *terminalMode { return &terminalMode{} }

func (m *terminalMode) restore() {}

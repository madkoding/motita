//go:build !unix

package tui

// Mouse reporting is a Unix terminal feature. Windows consoles deliver mouse input
// through ReadConsoleInput, which the standard library does not expose portably, and its
// console does not interpret ANSI mouse-mode sequences.
//
// The functions exist so the package compiles and behaves identically everywhere: on
// this platform they do nothing, and the interface is driven from the keyboard — which
// is the only requirement the design guide places on it.
func (t *TUI) enableMouse()  {}
func (t *TUI) disableMouse() {}

// mouseScroll lives in terminal.go, with no build tag: it is pure string parsing, and
// giving it a second implementation here would mean the parser under test on Unix is not
// the parser that ships on Windows.

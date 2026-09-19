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

// mouseScroll is never reached here, because no report can arrive, but it is defined so
// the parser and its tests are the same code on every platform rather than a branch that
// only exists in one build.
func mouseScroll(string) (int, bool) { return 0, false }

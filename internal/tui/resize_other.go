//go:build !unix

package tui

// watchResize is a no-op where there is no SIGWINCH. Windows delivers console resizes
// through ReadConsoleInput, which the standard library does not expose portably, and
// its console has no /dev/tty for ttySize to ask either.
//
// The channel is nil, which never delivers, and the stop function is inert. A closed
// channel would be ready forever and spin the selects the run loop watches it from.
func watchResize() (<-chan struct{}, func()) {
	return nil, func() {}
}

//go:build !unix

package tui

// watchResize is a no-op where there is no SIGWINCH. Windows delivers console resizes
// through ReadConsoleInput, which the standard library does not expose portably, and
// its console has no /dev/tty for ttySize to ask either.
//
// The channel is closed immediately and the stop function is inert, so the caller's
// "stop, then wait for the painter" sequence terminates here exactly as on Unix.
func watchResize() (<-chan struct{}, func()) {
	ch := make(chan struct{})
	close(ch)
	return ch, func() {}
}

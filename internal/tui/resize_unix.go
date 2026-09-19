//go:build unix

package tui

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// watchResize delivers on the returned channel whenever the terminal changes size.
//
// SIGWINCH is the signal a terminal sends for exactly this: the kernel raises it on
// the foreground process group when the window changes. It only became useful once
// ttySize could ask the driver for the CURRENT size — an earlier version of this
// watcher repainted on the signal but re-read the environment, which is frozen at
// exec, so it redrew an identical frame (measured: raised at 60x20, not a pixel
// changed). The signal says WHEN; the terminal says HOW BIG.
//
// The returned function stops the watch and CLOSES the delivery channel, so the
// caller can wait for its own painter:
//
//	stop()
//	painting.Wait()
//
// Without the close, a `for range` over the channel would block forever and the wait
// would deadlock. Without the wait, a paint already in flight could write its frame
// after the interface exited — a frame printed over the shell prompt.
//
// The signal channel is buffered, because signal.Notify drops signals when nobody is
// listening, and delivery to the caller is non-blocking: a burst of resizes must not
// leave the watcher blocked on a redraw that is already pending.
func watchResize() (<-chan struct{}, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGWINCH)

	out := make(chan struct{}, 1)
	stop := make(chan struct{})
	// finished is what makes stop synchronous: it returns only once the channel is
	// closed, so a caller doing "stop, then wait for the painter" cannot hang on a
	// goroutine the scheduler has not run yet.
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		defer close(out)
		for {
			select {
			case <-signals:
				select {
				case out <- struct{}{}:
				default:
					// One notification is already pending; a redraw covers both.
				}
			case <-stop:
				return
			}
		}
	}()

	var once sync.Once
	return out, func() {
		once.Do(func() {
			signal.Stop(signals)
			close(stop)
			<-finished
		})
	}
}

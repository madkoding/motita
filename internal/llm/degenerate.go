package llm

import (
	"bytes"
	"errors"
)

// ErrRepeating is the error of a reply cut short because the model was stuck saying the same
// thing over and over: a small or heavily quantised model can do it until the context is full,
// and nothing else would stop it.
var ErrRepeating = errors.New("the model got stuck repeating itself, so the reply was cut")

// The reply is stuck when its tail is one run of text, at least repeatMinBytes long, made of
// repeatMinCopies or more identical copies. Text that long and that regular is not an answer.
const (
	repeatMinCopies = 8
	repeatMinBytes  = 400
	repeatMaxUnit   = 512
	// repeatWindow is how much of the reply is kept to look at, and repeatEvery how many new
	// bytes pass between two looks.
	repeatWindow = repeatMaxUnit*repeatMinCopies + 64
	repeatEvery  = 128
)

// repeatWatch looks at a reply as it streams in.
type repeatWatch struct {
	tail  []byte
	fresh int
}

// feed adds text and reports whether the reply is now stuck in a loop.
func (w *repeatWatch) feed(text string) bool {
	w.tail = append(w.tail, text...)
	if extra := len(w.tail) - repeatWindow; extra > 0 {
		w.tail = append(w.tail[:0], w.tail[extra:]...)
	}
	w.fresh += len(text)
	if w.fresh < repeatEvery {
		return false
	}
	w.fresh = 0
	return stuck(w.tail)
}

// stuck says whether b ends in a run of repeatMinCopies identical copies of a unit of up to
// repeatMaxUnit bytes, covering at least repeatMinBytes.
func stuck(b []byte) bool {
	for unit := 1; unit <= repeatMaxUnit && unit*repeatMinCopies <= len(b); unit++ {
		// A short unit needs more copies to cover repeatMinBytes.
		if copies(b, unit) >= max(repeatMinCopies, (repeatMinBytes+unit-1)/unit) {
			return true
		}
	}
	return false
}

// copies counts how many identical copies of the last unit bytes of b end it.
func copies(b []byte, unit int) int {
	last := b[len(b)-unit:]
	n := 1
	for end := len(b) - unit; end-unit >= 0; end -= unit {
		if !bytes.Equal(b[end-unit:end], last) {
			break
		}
		n++
	}
	return n
}

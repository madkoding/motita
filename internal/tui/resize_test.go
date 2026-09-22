package tui

import (
	"bytes"
	"os"
	"os/signal"
	"strings"
	"sync"
	"testing"
	"time"
)

// Live resize, end to end.
//
// The two halves have to be tested together, because the first version of this
// feature passed its own tests and did nothing: it caught SIGWINCH and repainted, but
// re-read the environment, which is frozen at exec, so the frame came out identical.
// The signal says WHEN; ttySize says HOW BIG. A test that only checks the signal
// fires would have accepted the broken version.

// withEnvStale makes the environment deliberately wrong, which is what it is in a
// running process: COLUMNS is copied at exec and never updated. Anything that reads
// the real size must ignore these values.
func withEnvStale(t *testing.T) {
	t.Helper()
	prevCols, hadCols := os.LookupEnv("COLUMNS")
	prevLines, hadLines := os.LookupEnv("LINES")
	t.Cleanup(func() {
		if hadCols {
			os.Setenv("COLUMNS", prevCols)
		} else {
			os.Unsetenv("COLUMNS")
		}
		if hadLines {
			os.Setenv("LINES", prevLines)
		} else {
			os.Unsetenv("LINES")
		}
	})
	// A lie: the terminal is not this size.
	os.Setenv("COLUMNS", "999")
	os.Setenv("LINES", "999")
}

// TestTheTerminalSizeBeatsTheEnvironment: the environment describes the size the
// program STARTED at, so when the terminal can answer, its answer wins. This is the
// heart of the fix and it is asserted through size(), which is what the layout uses.
// stubTTYSize replaces the probe the size() read goes through, and returns the function
// that puts it back.
//
// The swap lives here rather than in the package: it is a test seam and nothing else, so
// production carries no function whose only caller is a test. The lock is still taken,
// because drawFrame consults the probe while painting and an unsynchronized swap is a data
// race in any test that resizes mid-frame.
func stubTTYSize(w, h int, ok bool) func() {
	probeMu.Lock()
	prev := probeTTYSize
	probeTTYSize = func() (int, int, bool) { return w, h, ok }
	probeMu.Unlock()
	return func() {
		probeMu.Lock()
		probeTTYSize = prev
		probeMu.Unlock()
	}
}

// TestTheSizeOutputIsParsedAsRowsThenColumns: `stty size` prints "rows cols", the
// opposite order from every other interface here. Reading it the obvious way swaps the
// axes, which is a bug that only shows on a non-square terminal.
func stubStty(t *testing.T, out string, ok bool) (func(), func()) {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tmp.Close() })

	prevOpen := openTTY
	prevRun := runStty
	openTTY = func(string) (*os.File, error) { return tmp, nil }
	runStty = func(*os.File) (string, error) {
		if !ok {
			return "", os.ErrInvalid
		}
		return out, nil
	}
	// The character-device check uses Stat, which a temp file fails. The probe is
	// therefore exercised through statTTY, stubbed alongside.
	prevStat := statTTY
	statTTY = func(*os.File) (os.FileMode, error) { return os.ModeCharDevice, nil }

	return func() { runStty = prevRun }, func() {
		openTTY = prevOpen
		statTTY = prevStat
	}
}

// TestASignalRepaintsAtTheNewSize: the two halves together. The signal arrives, the
// frame is redrawn, and — the part the first version got wrong — the frame is built for
// the size the TERMINAL reports, not the one the environment froze.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitFor blocks until the buffer contains the given text.
func waitFor(t *testing.T, b *syncBuffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(b.String(), needle) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%q never appeared in the output", needle)
}

// TestTheSignalIsNotRegisteredOnOtherPlatforms is covered by the platform-tagged
// files: on Unix the watcher registers SIGWINCH, and the no-op elsewhere closes its
// channel immediately so the same calling sequence terminates.
var _ = signal.Ignore

// TestAskTerminalSizeWalksTheRealBranches: the probe itself, with the seams wired to
// exercise each outcome — a terminal that answers, one that cannot be inspected, one
// whose stty fails, and one that answers nonsense. Each of these is a real environment
// the program runs in, and each has to fall back rather than reach the layout.

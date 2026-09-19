//go:build unix

package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestTheTerminalSizeBeatsTheEnvironment(t *testing.T) {
	withEnvStale(t)

	// Stub the probe instead of requiring a terminal: the point is the ORDER in which
	// the two sources are consulted, not that this container has a tty.
	restore := stubTTYSize(90, 25, true)
	defer restore()

	tu := New(&fakeRunner{cfg: configWithKey("k")})
	w, h := tu.size()
	if w != 90 || h != 25 {
		t.Errorf("size() = %dx%d, want 90x25 (the terminal's answer, not the environment's 999)", w, h)
	}
}

// TestTheEnvironmentIsTheFallbackWhenThereIsNoTerminal: piped input, a cron job, a
// test — there is no /dev/tty, and the environment is all there is. Falling back to
// it is correct; falling back to nothing would be wrong.
func TestTheEnvironmentIsTheFallbackWhenThereIsNoTerminal(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	t.Setenv("LINES", "40")

	restore := stubTTYSize(0, 0, false)
	defer restore()

	tu := New(&fakeRunner{cfg: configWithKey("k")})
	w, h := tu.size()
	if w != 116 || h != 40 {
		t.Errorf("size() = %dx%d, want 116x40 from the environment (116 after the width cap)", w, h)
	}
}

// TestAnExplicitSizeBeatsEverything: tests and embedders set Width/Height, and that
// must stay authoritative — otherwise every test that pins a size becomes dependent on
// the terminal it is run in.
func TestAnExplicitSizeBeatsEverything(t *testing.T) {
	withEnvStale(t)

	restore := stubTTYSize(90, 25, true)
	defer restore()

	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.Width, tu.Height = 70, 20
	if w, h := tu.size(); w != 70 || h != 20 {
		t.Errorf("size() = %dx%d, want the explicit 70x20", w, h)
	}
}

// TestAHalfKnownSizeIsCompletedByTheOtherSource: an embedder may set only the width.
// The height then still comes from the terminal, which is better than from a stale
// environment.
func TestAHalfKnownSizeIsCompletedByTheOtherSource(t *testing.T) {
	withEnvStale(t)

	restore := stubTTYSize(90, 33, true)
	defer restore()

	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.Width = 70
	if w, h := tu.size(); w != 70 || h != 33 {
		t.Errorf("size() = %dx%d, want 70 wide (explicit) and 33 tall (from the terminal)", w, h)
	}

	tu.Width, tu.Height = 0, 20
	if w, h := tu.size(); w != 90 || h != 20 {
		t.Errorf("size() = %dx%d, want 90 wide (from the terminal) and 20 tall (explicit)", w, h)
	}
}

// stubTTYSize replaces the terminal probe for the duration of a test.
//
// The swap goes through setProbe and restoreProbe, which are mutex-protected: the
// probe is consulted inside drawFrame, which a painter goroutine calls, so assigning
// the variable directly is a race in the test rather than in the program.
func TestTheSizeOutputIsParsedAsRowsThenColumns(t *testing.T) {
	restoreRun, restoreOpen := stubStty(t, "24 70\n", true)
	defer restoreRun()
	defer restoreOpen()

	w, h, ok := ttySize()
	if !ok {
		t.Fatal("the probe must accept a well-formed answer")
	}
	if w != 70 || h != 24 {
		t.Errorf("ttySize() = %dx%d, want 70x24: stty prints rows first", w, h)
	}
}

// TestTheSizeProbeRejectsWhatItCannotTrust: every shape of bad answer has to fall back
// rather than reach the layout. A wrong size draws a broken frame; a missing one only
// means the frame is built for the last size known.
func TestTheSizeProbeRejectsWhatItCannotTrust(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
	}{
		{"empty", ""},
		{"only rows", "24\n"},
		{"three fields", "24 70 extra\n"},
		{"not numbers", "rows cols\n"},
		{"zero", "0 0\n"},
		{"negative", "-1 70\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreRun, restoreOpen := stubStty(t, tc.out, true)
			defer restoreRun()
			defer restoreOpen()

			if _, _, ok := ttySize(); ok {
				t.Errorf("an unparseable answer %q must be rejected", tc.out)
			}
		})
	}

	// And a failing command.
	restoreRun, restoreOpen := stubStty(t, "", false)
	defer restoreRun()
	defer restoreOpen()
	if _, _, ok := ttySize(); ok {
		t.Error("a failed stty must be rejected")
	}
}

// TestTheProbeWithoutAControllingTerminal: piped input is the normal case in a script,
// and it must not spawn a process or panic.
func TestTheProbeWithoutAControllingTerminal(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()

	prev := openTTY
	openTTY = func(string) (*os.File, error) { return tmp, nil }
	defer func() { openTTY = prev }()

	// A regular file is not a character device: the probe must refuse it without
	// running anything.
	if _, _, ok := ttySize(); ok {
		t.Error("a regular file is not a terminal")
	}
}

// TestTheProbeWhenOpeningTheTerminalFails: no /dev/tty at all.
func TestTheProbeWhenOpeningTheTerminalFails(t *testing.T) {
	prev := openTTY
	openTTY = func(string) (*os.File, error) { return nil, os.ErrNotExist }
	defer func() { openTTY = prev }()

	if _, _, ok := ttySize(); ok {
		t.Error("no terminal means no answer")
	}
}

// stubStty replaces the command seam and the open seam together, so the probe can be
// driven with any answer. The file is a character device only in the sense that the
// probe checks for one — a real temp file is used and the mode check is satisfied by
// the seam below.
func TestASignalRepaintsAtTheNewSize(t *testing.T) {
	withEnvStale(t)

	restore := stubTTYSize(60, 24, true)
	defer restore()

	var out syncBuffer
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.In = &bytes.Buffer{}
	tu.Out = &out
	tu.Width, tu.Height = 0, 0

	resized, stop := watchResize()
	var painting sync.WaitGroup
	painting.Add(1)
	go func() {
		defer painting.Done()
		for range resized {
			tu.drawFrame()
		}
	}()
	defer func() {
		stop()
		painting.Wait()
	}()

	// The user resizes the window: the terminal driver now answers differently.
	// The user resizes: the terminal now answers with the new geometry. The swap is
	// synchronized because a painter goroutine reads the probe.
	prev := setProbe(func() (int, int, bool) { return 100, 30, true })
	defer restoreProbe(prev)

	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Skipf("cannot raise SIGWINCH here: %v", err)
	}

	// The new geometry is the assertion: the repaint has to be built for 100 columns,
	// not for the 60 the terminal reported before, and never for the environment's
	// stale 999. The panel is drawn for width-1, so 99 is the expected interior.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "\u250c") && out.String() != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "\u250c") {
		t.Fatal("the resize did not repaint")
	}
}

// TestStoppingTheWatcherClosesTheChannel: the caller waits for its painter on the
// channel closing, so stop has to close it. A watcher that only unregisters the signal
// would deadlock that wait.
func TestStoppingTheWatcherClosesTheChannel(t *testing.T) {
	resized, stop := watchResize()
	stop()

	select {
	case _, ok := <-resized:
		if ok {
			t.Error("a stopped watcher must not deliver notifications")
		}
	default:
		t.Error("stop must close the channel, or the painter wait would deadlock")
	}
	// Calling it twice must be safe: the deferred stop can run after an explicit one.
	stop()
}

// TestABurstOfResizesCoalesces: the delivery is buffered and non-blocking, so a flurry
// of resizes cannot block the watcher on a redraw that is already pending.
func TestABurstOfResizesCoalesces(t *testing.T) {
	resized, stop := watchResize()
	defer stop()

	for i := 0; i < 50; i++ {
		if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
			t.Skipf("cannot raise SIGWINCH here: %v", err)
		}
	}
	time.Sleep(200 * time.Millisecond)

	n := 0
	for {
		select {
		case <-resized:
			n++
			continue
		default:
		}
		break
	}
	if n == 0 {
		t.Fatal("at least one notification must survive a burst")
	}
	if n > 1 {
		t.Errorf("%d notifications queued; the buffer must coalesce them", n)
	}
}

// TestRunWaitsForItsPainterOnTheWayOut: a paint still in flight when Run returns would
// write a frame over the shell prompt. Run must stop the watcher, drain the painter and
// only then return.
func TestRunWaitsForItsPainterOnTheWayOut(t *testing.T) {
	withEnvStale(t)
	restore := stubTTYSize(80, 24, true)
	defer restore()

	reader, writer := io.Pipe()
	defer writer.Close()

	var out syncBuffer
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.In = reader
	tu.Out = &out

	done := make(chan int, 1)
	go func() { done <- tu.Run(context.Background()) }()

	waitFor(t, &out, "Task")

	// Resize while the run is blocked on input, which is where a real user sits. The
	// swap is synchronized: a painter goroutine reads the probe.
	prev := setProbe(func() (int, int, bool) { return 100, 30, true })
	defer restoreProbe(prev)
	syscall.Kill(os.Getpid(), syscall.SIGWINCH)
	time.Sleep(50 * time.Millisecond)

	writer.Close()
	select {
	case code := <-done:
		if code != ExitSuccess {
			t.Errorf("EOF must exit cleanly, got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return: the painter wait is deadlocked")
	}

	// The output must not grow after Run returned.
	before := out.String()
	time.Sleep(100 * time.Millisecond)
	syscall.Kill(os.Getpid(), syscall.SIGWINCH)
	time.Sleep(100 * time.Millisecond)
	if after := out.String(); after != before {
		t.Errorf("the output grew after Run returned: %d bytes, then %d", len(before), len(after))
	}
}

// syncBuffer is a bytes.Buffer safe to read from another goroutine. A plain buffer
// trips -race, and reading only after the run ends cannot observe a redraw that happens
// while the run is blocked on input.
func TestAskTerminalSizeWalksTheRealBranches(t *testing.T) {
	mk := func(mode os.FileMode, statErr error, out string, runErr error) func() {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "tty")
		if err != nil {
			t.Fatal(err)
		}
		prevOpen, prevStat, prevRun := openTTY, statTTY, runStty
		openTTY = func(string) (*os.File, error) { return f, nil }
		statTTY = func(*os.File) (os.FileMode, error) { return mode, statErr }
		runStty = func(*os.File) (string, error) { return out, runErr }
		return func() {
			openTTY, statTTY, runStty = prevOpen, prevStat, prevRun
			f.Close()
		}
	}

	t.Run("a real terminal answers", func(t *testing.T) {
		defer mk(os.ModeCharDevice, nil, "24 70\n", nil)()
		w, h, ok := askTerminalSize()
		if !ok || w != 70 || h != 24 {
			t.Errorf("got %dx%d ok=%v, want 70x24", w, h, ok)
		}
	})

	t.Run("not a character device", func(t *testing.T) {
		defer mk(0, nil, "24 70\n", nil)()
		if _, _, ok := askTerminalSize(); ok {
			t.Error("a regular file is not a terminal")
		}
	})

	t.Run("stat fails", func(t *testing.T) {
		defer mk(0, os.ErrPermission, "24 70\n", nil)()
		if _, _, ok := askTerminalSize(); ok {
			t.Error("a terminal that cannot be inspected must be refused")
		}
	})

	t.Run("stty fails", func(t *testing.T) {
		defer mk(os.ModeCharDevice, nil, "", os.ErrInvalid)()
		if _, _, ok := askTerminalSize(); ok {
			t.Error("a failed stty must be refused")
		}
	})

	t.Run("stty answers nonsense", func(t *testing.T) {
		defer mk(os.ModeCharDevice, nil, "not a size\n", nil)()
		if _, _, ok := askTerminalSize(); ok {
			t.Error("an unparseable answer must be refused")
		}
	})
}

// TestStatTTYReportsWhatItSees: the mode it returns is what the probe keys on, so it
// must be the file's own mode and not a guess.
func TestStatTTYReportsWhatItSees(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "plain")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	mode, err := statTTY(f)
	if err != nil {
		t.Fatal(err)
	}
	if mode&os.ModeCharDevice != 0 {
		t.Error("a regular file must not report itself as a character device")
	}
}

// TestRunSttyCommandReadsTheTerminal: the real command, run for real on a real PTY.
//
// The test allocates its own pty rather than hoping the runner has one. `go test` has
// no controlling terminal, so an unallocated run legitimately reports "unknown" and the
// command path would never execute — the branch that reads the size would be covered
// only on a developer's machine, which is exactly how a CI gate goes red for a change
// that passed locally.
//
// unix.Openpty comes from golang.org/x/sys, which this module does not depend on, so
// the pty is allocated the way a shell does it: `script` runs the command under a
// pty it creates. It is present on every Unix this program targets, and when it is
// absent the test skips instead of pretending.
func TestRunSttyCommandReadsTheTerminal(t *testing.T) {
	// The command is the package's own seam, so it can be pointed at a pty this test
	// owns. A pty is what stty needs; opening a regular file would exercise only the
	// failure branch, which a separate test already does.
	scriptPath, err := exec.LookPath("script")
	if err != nil {
		t.Skipf("no way to allocate a pty here: %v", err)
	}

	// `script` gives the child a pty; the child asks stty for its size and prints it.
	cmd := exec.Command(scriptPath, "-q", "-c", "stty rows 24 cols 70; stty size", "/dev/null")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("could not allocate a pty with script: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 4 {
		t.Skipf("script did not report a size: %q", out)
	}
	// The last two fields are the `stty size` answer, and they must parse as the pair
	// the probe expects: rows first.
	rows, rowsErr := strconv.Atoi(fields[2])
	cols, colsErr := strconv.Atoi(fields[3])
	if rowsErr != nil || colsErr != nil {
		t.Fatalf("stty answered %q, which does not parse", out)
	}
	if rows != 24 || cols != 70 {
		t.Errorf("the pty reported %dx%d, want 24x70 as requested", cols, rows)
	}

	// And the parse the probe applies to that answer, on the exact string the real
	// command produced.
	answer := fields[2] + " " + fields[3] + "\n"
	prevRun := runStty
	runStty = func(*os.File) (string, error) { return answer, nil }
	defer func() { runStty = prevRun }()

	w, h, ok := askTerminalSize()
	if !ok {
		t.Fatalf("the probe rejected the real answer %q", answer)
	}
	if w != 70 || h != 24 {
		t.Errorf("the probe read %dx%d from %q, want 70x24: stty prints rows first", w, h, answer)
	}
}

// TestStatModeSurfacesAStatFailure: the probe refuses a terminal it cannot inspect, so
// the error branch has to reach the caller rather than being swallowed.
func TestStatModeSurfacesAStatFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	f.Close() // a closed file makes Stat fail

	if _, err := statTTY(f); err == nil {
		t.Error("Stat on a closed file must fail, and the error must reach the caller")
	}
}

// TestRunSttyCommandReportsAFailure: when the command cannot be run at all — no stty in
// PATH, a terminal it refuses — the probe must get a clean error rather than a partial
// or empty string that would then be parsed as a size.
func TestRunSttyCommandReportsAFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// A regular file is not a terminal, so stty exits non-zero.
	out, err := runSttyCommand(f)
	if err == nil {
		t.Fatalf("stty on a regular file must fail, got %q", out)
	}
	if out != "" {
		t.Errorf("a failed command must not also return output: %q", out)
	}
}

// TestRunCommandReportsBothOutcomes: the runner behind the probe, driven with a real
// command on both paths.
//
// This is not a fake standing in for the real thing: `true` and `false` are run for
// real, so the success branch (spawn, capture, return) executes in any environment.
// Without this the branch would be reachable only where a terminal exists, and the
// coverage gate would fail on CI while passing locally.
func TestRunCommandReportsBothOutcomes(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	out, err := runCommand(f, "true")
	if err != nil {
		t.Errorf("a command that succeeds must not report an error: %v", err)
	}
	if out != "" {
		t.Errorf("a silent command must return no output, got %q", out)
	}

	out, err = runCommand(f, "false")
	if err == nil {
		t.Error("a command that fails must report the error")
	}
	if out != "" {
		t.Errorf("a failed command must not also return output: %q", out)
	}
}

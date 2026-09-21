//go:build unix

package tui

// The terminal mode itself. These drive the Unix driver seams — stty and /dev/tty — which is
// why the file carries the build tag: on a platform with no stty those symbols do not exist, and
// the package would not compile there. The portable half of the live composer lives in
// live_test.go; keeping these here is what lets `make test-matrix` build the suite everywhere.

import (
	"os"
	"testing"
)

// The terminal mode itself.

// TestRestoringIsIdempotent: restore runs from the deferred call AND from the panic path, and
// the second one must not try to close an already-closed handle.
func TestRestoringIsIdempotent(t *testing.T) {
	var calls int
	old := runSttyMode
	runSttyMode = func(f *os.File, args ...string) error { calls++; return nil }
	defer func() { runSttyMode = old }()

	f, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	m := &terminalMode{tty: f, active: true}

	m.restore()
	m.restore()
	m.restore()

	if calls != 1 {
		t.Errorf("restore ran %d times, want exactly 1", calls)
	}
	if m.active {
		t.Error("the mode must be marked inactive after restoring")
	}
}

// TestRestoringANilOrInactiveModeIsSafe: the caller should not have to branch on whether the
// terminal was ever changed.
func TestRestoringANilOrInactiveModeIsSafe(t *testing.T) {
	var nilMode *terminalMode
	nilMode.restore()
	(&terminalMode{}).restore()
}

// TestEnteringTheModeAsksTheDriverForCBreakWithoutEcho: cbreak is what makes a keystroke arrive
// as it is typed, and -echo is what stops the driver drawing the line a second time underneath
// the one the interface draws.
func TestEnteringTheModeAsksTheDriverForCBreakWithoutEcho(t *testing.T) {
	var got []string
	oldRun, oldOpen, oldStat := runSttyMode, openTTYMode, statTTY
	defer func() { runSttyMode, openTTYMode, statTTY = oldRun, oldOpen, oldStat }()

	runSttyMode = func(f *os.File, args ...string) error { got = args; return nil }
	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.CreateTemp(t.TempDir(), "tty")
	}
	statTTY = func(*os.File) (os.FileMode, error) { return os.ModeCharDevice, nil }

	m := enterRaw()
	defer m.restore()

	if !m.active {
		t.Fatal("the mode must be active when the driver accepts it")
	}
	if len(got) != 2 || got[0] != "cbreak" || got[1] != "-echo" {
		t.Errorf("stty args = %v, want cbreak -echo", got)
	}
}

// TestNoTerminalDegradesToWholeLines: piped input has no controlling terminal, and the
// interface must keep working there — it just cannot offer live completion.
func TestNoTerminalDegradesToWholeLines(t *testing.T) {
	oldOpen := openTTYMode
	defer func() { openTTYMode = oldOpen }()
	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, os.ErrNotExist
	}

	m := enterRaw()
	defer m.restore()
	if m.active {
		t.Error("without a terminal the mode must stay inactive")
	}
}

// TestANonTerminalFileDegrades: a redirection to a file looks like a terminal to an open call
// but is not one, and asking it for cbreak would fail noisily.
func TestANonTerminalFileDegrades(t *testing.T) {
	oldOpen, oldStat := openTTYMode, statTTY
	defer func() { openTTYMode, statTTY = oldOpen, oldStat }()
	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.CreateTemp(t.TempDir(), "file")
	}
	statTTY = func(*os.File) (os.FileMode, error) { return 0, nil }

	m := enterRaw()
	defer m.restore()
	if m.active {
		t.Error("a regular file must not be put into character mode")
	}
}

// TestADriverThatRefusesTheModeDegrades: a terminal that will not take the mode leaves the
// behaviour we had — whole lines, no popup, working interface. Degrading is right; failing
// would make the program unusable on such a terminal.
func TestADriverThatRefusesTheModeDegrades(t *testing.T) {
	oldRun, oldOpen, oldStat := runSttyMode, openTTYMode, statTTY
	defer func() { runSttyMode, openTTYMode, statTTY = oldRun, oldOpen, oldStat }()

	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.CreateTemp(t.TempDir(), "tty")
	}
	statTTY = func(*os.File) (os.FileMode, error) { return os.ModeCharDevice, nil }
	runSttyMode = func(f *os.File, args ...string) error { return os.ErrInvalid }

	m := enterRaw()
	defer m.restore()
	if m.active {
		t.Error("a refused mode must leave the terminal alone")
	}
}

// TestTheTerminalIsRestoredBeforeAPanicContinues: a session that dies while the terminal is in
// cbreak with echo off gives the user back a shell that shows nothing they type. The restore
// must happen on the way out of a panic, before it keeps unwinding.
func TestTheTerminalIsRestoredBeforeAPanicContinues(t *testing.T) {
	oldRun, oldOpen, oldStat := runSttyMode, openTTYMode, statTTY
	defer func() { runSttyMode, openTTYMode, statTTY = oldRun, oldOpen, oldStat }()

	var restored bool
	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.CreateTemp(t.TempDir(), "tty")
	}
	statTTY = func(*os.File) (os.FileMode, error) { return os.ModeCharDevice, nil }
	runSttyMode = func(f *os.File, args ...string) error {
		if len(args) == 1 && args[0] == "sane" {
			restored = true
		}
		return nil
	}

	m := enterRaw()
	func() {
		// The panic is CAUGHT here, after recoverRaw has had its turn: recoverRaw re-panics by
		// design, so the outer recover is what lets the test observe both facts — that the
		// terminal was put back, and that the panic was not swallowed.
		defer func() { _ = recover() }()
		defer recoverRaw(m)()
		panic("something went wrong deep in the paint")
	}()

	if !restored {
		t.Error("the terminal must be put back before the panic continues")
	}
}

// TestThePanicIsNotSwallowed: recoverRaw restores the terminal and then lets the panic keep
// going. A version that swallowed it would turn a crash into a silent, wrong result.
func TestThePanicIsNotSwallowed(t *testing.T) {
	var seen any
	func() {
		defer func() { seen = recover() }()
		defer recoverRaw(&terminalMode{})()
		panic("boom")
	}()

	if seen != "boom" {
		t.Errorf("the panic must continue, got %v", seen)
	}
}

// TestASuccessfulExitDoesNotPanic: recoverRaw must re-panic ONLY when there was a panic. A
// version that panicked unconditionally would tear down every normal exit.
func TestASuccessfulExitDoesNotPanic(t *testing.T) {
	func() {
		defer recoverRaw(&terminalMode{})()
	}()
}

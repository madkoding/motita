package logx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Remaining branches -----------------------------------------------------

func TestLevelStringUnknown(t *testing.T) {
	if got := Level(99).String(); got != "unknown" {
		t.Errorf("String() = %q, expected \"unknown\"", got)
	}
	for level, want := range map[Level]string{Debug: "debug", Info: "info", Warn: "warn", Error: "error"} {
		if got := level.String(); got != want {
			t.Errorf("String() = %q, expected %q", got, want)
		}
	}
}

func TestParseLevelWarningAlias(t *testing.T) {
	if n, err := ParseLevel("warning"); err != nil || n != Warn {
		t.Errorf("warning -> %v %v", n, err)
	}
	if n, err := ParseLevel("  DEBUG  "); err != nil || n != Debug {
		t.Errorf("trimming and case must not matter: %v %v", n, err)
	}
	if n, err := ParseLevel("error"); err != nil || n != Error {
		t.Errorf("error -> %v %v", n, err)
	}
}

// TestNewCreatesMissingDirectory: the log's parent directory is created when it
// does not exist, because a deployment should not fail just for that.
func TestNewCreatesMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "log.log")
	l, err := New(Options{Path: path, Level: Info})
	if err != nil {
		t.Fatalf("it must create the directory: %v", err)
	}
	l.Info("inside the nested directory")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the log was not created: %v", err)
	}
}

// TestInstallAndGlobal: the global log can be replaced and read back, which is
// what every package relies on to log without being handed a logger.
func TestInstallAndGlobal(t *testing.T) {
	original := Global()
	defer Install(original)

	custom, err := New(Options{Level: Warn, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	Install(custom)
	if got := Global(); got != custom {
		t.Error("Install did not replace the global log")
	}
	// The package-level shortcuts must use the installed logger.
	Debugf("debug message")
	Infof("info message")
	Warnf("warn message")
	Errorf("error message")
	Install(original)
	if Global() != original {
		t.Error("the original log was not restored")
	}
}

// TestOddFieldDoesNotLoseTheMessage: a field whose name is not a string (which
// would break the pairs) must not make the message disappear.
func TestOddFieldDoesNotLoseTheMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "odd.log")
	l, err := New(Options{Path: path, Level: Debug, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	// Odd number of fields plus a non-string key: both are skipped.
	l.Info("the message survives", 42, "value")
	l.Info("another one", "key")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	if !strings.Contains(lines[0], "the message survives") || !strings.Contains(lines[1], "another one") {
		t.Errorf("the messages were lost: %v", lines)
	}
}

// TestUnserialisableFieldDegrades: a field that cannot be serialised (a channel,
// a func) must not lose the message either.
func TestUnserialisableFieldDegrades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "odd2.log")
	l, err := New(Options{Path: path, Level: Debug, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("it must not be lost", "bad", make(chan int))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	if !strings.Contains(lines[0], "it must not be lost") {
		t.Errorf("the message was lost: %q", lines[0])
	}
}

// TestRotationWithNoBackups: with backups=0 the rotated file is dropped instead
// of shifted.
func TestRotationWithNoBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nobak.log")
	l, err := New(Options{Path: path, Level: Info, Console: false, MaxMB: 1, Backups: 0})
	if err != nil {
		t.Fatal(err)
	}
	l.maxByte = 512
	for i := 0; i < 40; i++ {
		l.Info("filler to force rotation", "i", i, "pad", strings.Repeat("y", 60))
	}
	l.Close()

	if rotated := l.RotatedFiles(); len(rotated) != 0 {
		t.Errorf("with backups=0 nothing must be kept: %v", rotated)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the current log must still exist: %v", err)
	}
}

// TestRotatedFilesWithoutPath: with no path there is nothing to list.
func TestRotatedFilesWithoutPath(t *testing.T) {
	l, _ := New(Options{Level: Info})
	if got := l.RotatedFiles(); got != nil {
		t.Errorf("RotatedFiles = %v, expected nil", got)
	}
}

// TestJsonValue: the conversion of values that are not directly serialisable.
func TestJsonValue(t *testing.T) {
	if got := jsonValue(nil); got != nil {
		t.Errorf("a nil error must stay nil: %#v", got)
	}
	if got := jsonValue(errors.New("boom")); got != "boom" {
		t.Errorf("an error must become text: %#v", got)
	}
	if got := jsonValue(3 * time.Second); got != "3s" {
		t.Errorf("a duration must become text: %#v", got)
	}
	if got := jsonValue(7); got != 7 {
		t.Errorf("anything else must pass through: %#v", got)
	}
	// A typed nil error must not panic.
	var typedErr error
	if got := jsonValue(typedErr); got != nil {
		t.Errorf("a typed nil error must stay nil: %#v", got)
	}
}

// TestNewFailsWhenParentIsAFile: if the parent "directory" is really a file, the
// creation must fail with a clear error instead of silently logging nothing.
func TestNewFailsWhenParentIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Path: filepath.Join(file, "log.log"), Level: Info}); err == nil {
		t.Error("creating the directory must fail when the parent is a file")
	}
}

// TestRotationFallsBackToConsole: if the log cannot be reopened after rotating,
// the agent must keep running with the console instead of dying.
func TestRotationFallsBackToConsole(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root almost everything is writable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "rotating.log")
	l, err := New(Options{Path: path, Level: Info, Console: true, MaxMB: 1, Backups: 1})
	if err != nil {
		t.Fatal(err)
	}
	l.maxByte = 128

	// Point the logger at a path that cannot be reopened: a file inside a
	// read-only directory.
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	l.path = filepath.Join(locked, "log.log")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)

	// This write triggers the rotation, whose reopen fails.
	l.Info("filler to force rotation", "pad", strings.Repeat("z", 200))

	if l.file != nil {
		t.Error("with no reopenable file, it must continue without a file")
	}
	if err := l.Close(); err != nil {
		t.Errorf("closing must not fail: %v", err)
	}
}

// TestNewFailsWhenTheLogCannotBeOpened: a path that exists but cannot be opened
// (a directory) must be an error, not a silent no-op.
func TestNewFailsWhenTheLogCannotBeOpened(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(Options{Path: dir, Level: Info}); err == nil {
		t.Error("opening a directory as the log must fail")
	}
}

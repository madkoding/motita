package logx

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogIsJSONLines: every line must be a parseable JSON object, which is what
// makes it consumable with jq or a collector.
func TestLogIsJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	l, err := New(Options{Path: path, Level: Debug, Console: false, MaxMB: 10, Backups: 2})
	if err != nil {
		t.Fatal(err)
	}

	l.Debug("starting", "model", "gpt")
	l.Info("task received", "description", "something", "attempt", 1)
	l.Warn("careful", "detail", "x")
	l.Error("it failed", "error", os.ErrNotExist)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, path)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}
	for i, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d is not JSON: %v (%q)", i, err, line)
		}
		for _, field := range []string{"ts", "level", "msg"} {
			if _, ok := event[field]; !ok {
				t.Errorf("line %d has no %q field: %s", i, field, line)
			}
		}
	}

	var first map[string]any
	json.Unmarshal([]byte(lines[0]), &first)
	if first["level"] != "debug" {
		t.Errorf("level = %v", first["level"])
	}

	// An error must be serialised as text, not break the JSON.
	var fourth map[string]any
	json.Unmarshal([]byte(lines[3]), &fourth)
	if _, ok := fourth["error"].(string); !ok {
		t.Errorf("the error must be stored as text: %#v", fourth["error"])
	}
}

// TestLevelFilters: logging at debug must not appear when the level is info.
func TestLevelFilters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filtered.log")
	l, _ := New(Options{Path: path, Level: Info, Console: false})
	l.Debug("must not appear")
	l.Info("must appear")
	l.Close()

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	if !strings.Contains(lines[0], "must appear") {
		t.Errorf("line = %s", lines[0])
	}
}

// TestRotationBySize: once the limit is exceeded the file rotates and the
// configured backups are kept, without losing records along the way.
func TestRotationBySize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rotating.log")

	// MaxMB of 0 would disable rotation; a tiny size is used by setting the
	// limit directly.
	l, err := New(Options{Path: path, Level: Info, Console: false, MaxMB: 1, Backups: 2})
	if err != nil {
		t.Fatal(err)
	}
	l.maxByte = 2048 // small limit for the test

	for i := 0; i < 200; i++ {
		l.Info("padding message to force rotation", "i", i, "padding", strings.Repeat("x", 100))
	}
	l.Close()

	rotated := l.RotatedFiles()
	if len(rotated) == 0 {
		t.Fatal("the file did not rotate")
	}
	if len(rotated) > 2 {
		t.Errorf("more backups are kept than configured: %v", rotated)
	}
	// The current file and the backups must be valid JSON.
	for _, r := range append([]string{path}, rotated...) {
		for i, line := range readLines(t, r) {
			var event map[string]any
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Errorf("%s line %d is unreadable: %v", r, i, err)
			}
		}
	}
}

// TestNoRotationWhenNotNeeded: it must not create backups if it never fills up.
func TestNoRotationWhenNotNeeded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.log")
	l, _ := New(Options{Path: path, Level: Info, Console: false, MaxMB: 10, Backups: 3})
	l.Info("a single line")
	l.Close()

	if rotated := l.RotatedFiles(); len(rotated) != 0 {
		t.Errorf("it should not have rotated: %v", rotated)
	}
}

// TestInvalidLevel: a misspelled level is detected.
func TestInvalidLevel(t *testing.T) {
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("an error was expected")
	}
	if n, err := ParseLevel("WARN"); err != nil || n != Warn {
		t.Errorf("WARN -> %v %v", n, err)
	}
	if n, err := ParseLevel(""); err != nil || n != Info {
		t.Errorf("empty must be info: %v %v", n, err)
	}
}

// TestConsoleOnly: without a file the log does not fail and writes to the output.
func TestConsoleOnly(t *testing.T) {
	var sb strings.Builder
	l, err := New(Options{Level: Info, Console: true})
	if err != nil {
		t.Fatal(err)
	}
	l.out = &sb
	l.Info("console only")
	if !strings.Contains(sb.String(), "console only") {
		t.Errorf("output = %q", sb.String())
	}
	if err := l.Close(); err != nil {
		t.Errorf("closing without a file must not fail: %v", err)
	}
}

// TestConcurrency: the log is used from several goroutines (the agent's loop and
// shutdown), so it must be safe.
func TestConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.log")
	l, _ := New(Options{Path: path, Level: Info, Console: false})

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			for j := 0; j < 50; j++ {
				l.Info("from goroutine", "g", n, "j", j)
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	l.Close()

	lines := readLines(t, path)
	if len(lines) != 400 {
		t.Errorf("lines = %d, expected 400 (were records lost?)", len(lines))
	}
	for i, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d is corrupt (concurrent write): %v", i, err)
		}
	}
}

// TestUnwritablePath: an impossible directory must give a clear error when the
// logger is built, not a silent log.
func TestUnwritablePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root almost everything is writable")
	}
	if _, err := New(Options{Path: "/proc/1/you-cannot/log.log", Level: Info}); err == nil {
		t.Fatal("an error was expected when opening an impossible path")
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("could not open %s: %v", path, err)
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			lines = append(lines, sc.Text())
		}
	}
	return lines
}

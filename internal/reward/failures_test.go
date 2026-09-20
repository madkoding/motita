package reward

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The failure branches. A ledger is the only record of what the user thought of the library,
// and every one of these paths is a way to lose it or to silently pretend it was written —
// so each has to be reached and checked rather than assumed unreachable.

// TestAReadErrorIsReportedNotTreatedAsMissing: "the file is not there" and "the file cannot be
// read" are different, and only the first is a fresh start. Treating a permission error as
// emptiness would wipe the history without a word.
func TestAReadErrorIsReportedNotTreatedAsMissing(t *testing.T) {
	dir := t.TempDir()
	// A directory where the ledger file should be: it exists, and reading it fails.
	path := filepath.Join(dir, "scores.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("a ledger that cannot be read must be reported, not replaced with an empty one")
	}
}

func TestSaveReportsADirectoryItCannotCreate(t *testing.T) {
	// The parent becomes a file AFTER Open, so the read succeeds and only the directory
	// creation fails. Doing it before Open made the read fail instead and the test asserted
	// the wrong branch.
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent")
	l, err := Open(filepath.Join(parent, "scores.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")

	// A file where the directory should be: MkdirAll cannot create it.
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := l.Save(); err == nil {
		t.Fatal("a directory that cannot be created must be reported")
	}
}

func TestSaveReportsATempFileThatCannotBeOpened(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "scores.json"))
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")

	restore := createTemp
	createTemp = func(string, string) (*os.File, error) { return nil, errors.New("no descriptor") }
	defer func() { createTemp = restore }()

	if err := l.Save(); err == nil || !strings.Contains(err.Error(), "no descriptor") {
		t.Fatalf("expected the temp-file error, got %v", err)
	}
}

func TestSaveReportsAWriteThatFails(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "scores.json"))
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")

	restore := writeLedger
	writeLedger = func(*os.File, []byte) (int, error) { return 0, errors.New("disk full") }
	defer func() { writeLedger = restore }()

	if err := l.Save(); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected the write error, got %v", err)
	}
}

func TestSaveReportsACloseThatFails(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "scores.json"))
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")

	restore := closeLedger
	closeLedger = func(*os.File) error { return errors.New("close failed") }
	defer func() { closeLedger = restore }()

	if err := l.Save(); err == nil || !strings.Contains(err.Error(), "close failed") {
		t.Fatalf("expected the close error, got %v", err)
	}
}

func TestSaveReportsARenameThatFails(t *testing.T) {
	// The rename is the atomic step: if it fails, the ledger on disk is the OLD one, and the
	// caller has to know the verdict did not land.
	l, _ := Open(filepath.Join(t.TempDir(), "scores.json"))
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")

	restore := renameFile
	renameFile = func(string, string) error { return errors.New("cross-device link") }
	defer func() { renameFile = restore }()

	if err := l.Save(); err == nil || !strings.Contains(err.Error(), "cross-device") {
		t.Fatalf("expected the rename error, got %v", err)
	}
}

// TestAFailedSaveLeavesTheLedgerDirty: the value must not be reported as persisted when it is
// not, or a later Save would skip the write and the verdict would be lost silently.
func TestAFailedSaveLeavesTheLedgerDirty(t *testing.T) {
	l, _ := Open(filepath.Join(t.TempDir(), "scores.json"))
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")

	restore := renameFile
	renameFile = func(string, string) error { return errors.New("nope") }
	defer func() { renameFile = restore }()

	if err := l.Save(); err == nil {
		t.Fatal("expected the save to fail")
	}
	if !l.dirty {
		t.Error("a failed save must leave the ledger dirty so a retry writes it")
	}
}

// TestACloseFailureStillRemovesTheTempFile: a leftover .scores.*.tmp would be read as nothing,
// but it never stops being written on every attempt.
func TestACloseFailureStillRemovesTheTempFile(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(filepath.Join(dir, "scores.json"))
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")

	restore := closeLedger
	closeLedger = func(*os.File) error { return errors.New("close failed") }
	defer func() { closeLedger = restore }()
	_ = l.Save()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".scores.") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// TestValueOfIsTheNumberWithoutTheFlag: callers that only want the figure get 0 for "never
// used", which is what makes it safe to compare directly.
func TestValueOfIsTheNumberWithoutTheFlag(t *testing.T) {
	l := ledgerAt(t)
	if got := l.ValueOf("unknown"); got != 0 {
		t.Errorf("an unknown skill must report 0, got %v", got)
	}
	_ = l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")
	if got := l.ValueOf("a"); got <= 0 {
		t.Errorf("a known skill must report its value, got %v", got)
	}
}

// TestAttributeSetsTheClockWhenNoneWasInjected: the ledger is usable straight out of Open,
// without the caller having to remember the clock.
func TestAttributeSetsTheClockWhenNoneWasInjected(t *testing.T) {
	l := &Ledger{Scores: map[string]Score{}} // no clock
	if err := l.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "x"); err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	s, _ := l.Get("a")
	if s.Updated.IsZero() {
		t.Error("the ledger must stamp the verdict even with no injected clock")
	}
}

// TestTrimNoteBoundsAndStaysValidUTF8: reached directly, because through Attribute the branch
// only runs for notes long enough to be trimmed.
func TestTrimNoteBoundsAndStaysValidUTF8(t *testing.T) {
	short := TrimNote("brief")
	if short != "brief" {
		t.Errorf("a short note must pass through unchanged, got %q", short)
	}
	if TrimNote("   ") != "" {
		t.Errorf("whitespace only must collapse to nothing, got %q", TrimNote("   "))
	}
	long := strings.Repeat("ñ", noteCap+50)
	got := TrimNote(long)
	if len([]rune(got)) != noteCap+1 { // the cap plus the ellipsis
		t.Errorf("expected %d runes, got %d", noteCap+1, len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("a trimmed note must say it was trimmed")
	}
	if !utf8.ValidString(got) {
		t.Error("the trim must keep the string valid UTF-8")
	}
	// Exactly at the cap is not over it.
	exact := strings.Repeat("a", noteCap)
	if TrimNote(exact) != exact {
		t.Error("a note at the cap must not be trimmed")
	}
}

// TestAZeroCountIsFlooredToOne: the credit share is computed from the total of the counts, and
// a named skill with a count of zero would contribute nothing to it. The per-skill floor of 1
// is what keeps the total positive for any non-empty list, so a division by zero is not a case
// that can be reached — this asserts the floor that makes it so.
func TestAZeroCountIsFlooredToOne(t *testing.T) {
	l := ledgerAt(t)
	if err := l.Attribute([]string{"a"}, map[string]int{"a": 0}, true, ""); err != nil {
		t.Fatalf("a zero count must be treated as one use, not as an error: %v", err)
	}
	v, ok := l.Value("a")
	if !ok || v <= 0 {
		t.Errorf("the skill must gain something: (%v, %v)", v, ok)
	}
	// And the share is the full signal divided by one, i.e. the floor is what was used.
	full := ledgerAt(t)
	_ = full.Attribute([]string{"a"}, map[string]int{"a": 1}, true, "")
	fv, _ := full.Value("a")
	if v != fv {
		t.Errorf("a zero count must give the same credit as one use: %v vs %v", v, fv)
	}
}

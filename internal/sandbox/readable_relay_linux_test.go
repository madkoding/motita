//go:build linux

package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// countRules replaces the Landlock calls with ones that succeed, and counts the rules added.
func countRules(t *testing.T) *int {
	t.Helper()
	real := landlockSyscall
	t.Cleanup(func() { landlockSyscall = real })
	rules := 0
	landlockSyscall = func(trap, a1, a2, a3 uintptr) (uintptr, uintptr, syscall.Errno) {
		switch {
		case trap == sysLandlockCreateRuleset && a1 == 0:
			return 3, 0, 0
		case trap == sysLandlockCreateRuleset:
			return uintptr(mustOpenDevNull(t)), 0, 0
		case trap == sysLandlockAddRule:
			rules++
		default:
			// any other call succeeds
		}
		return 0, 0, 0
	}
	return &rules
}

// failListings makes every directory listing fail, as it does from inside a user namespace on a
// kernel that restricts them.
func failListings(t *testing.T) {
	t.Helper()
	real := readDir
	t.Cleanup(func() { readDir = real })
	readDir = func(string) ([]os.DirEntry, error) { return nil, syscall.EACCES }
}

// leftFile writes a list the way the isolation child leaves it for the child in the namespaces.
func leftFile(t *testing.T, content string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "readable")
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

// What the isolation child already worked out is used as it is: the child in the namespaces lists
// nothing, so a directory it cannot list does not stop the command.
func TestConfineUsesWhatWasWorkedOutBeforeTheRelay(t *testing.T) {
	needLandlock(t)
	rules := countRules(t)
	failListings(t)
	granted := t.TempDir()
	spec := Spec{WriteRoots: []string{t.TempDir()}, PrivatePaths: []string{"/home"},
		ReadableFile: leftFile(t, `["`+granted+`"]`)}
	if err := confine(spec); err != nil {
		t.Fatalf("a listing that fails inside the namespace stopped the command: %v", err)
	}
	if *rules != 3 { // the workspace (written and read) and the one entry granted around the private tree
		t.Errorf("rules = %d, want 3", *rules)
	}
	if _, err := os.Stat(spec.ReadableFile); !os.IsNotExist(err) {
		t.Errorf("the file was not removed: %v", err)
	}
	// Nothing left readable is an answer too, not a failure.
	spec.ReadableFile = leftFile(t, "[]")
	if err := confine(spec); err != nil {
		t.Errorf("an empty answer was taken for a failure: %v", err)
	}
	spec.ReadableFile = leftFile(t, "null")
	if err := confine(spec); err != nil {
		t.Errorf("a null answer was taken for a failure: %v", err)
	}
}

// Without a usable answer the child lists for itself, and fails closed when it cannot.
func TestConfineListsForItselfWithoutAUsableAnswer(t *testing.T) {
	needLandlock(t)
	countRules(t)
	failListings(t)
	for _, file := range []string{"", filepath.Join(t.TempDir(), "gone"), leftFile(t, "not json")} {
		spec := Spec{WriteRoots: []string{t.TempDir()}, PrivatePaths: []string{"/home"}, ReadableFile: file}
		if err := confine(spec); err == nil || !strings.Contains(err.Error(), "stays readable") {
			t.Errorf("file %q: got %v", file, err)
		}
	}
}

// The isolation child lists what stays readable before it relays, and leaves it to the child in the
// namespaces only when it cannot.
func TestWorkOutReadableLeavesTheListForTheRelay(t *testing.T) {
	home, work := userHome(t)
	// The home is under /tmp, which other packages' tests write to while this one runs, so a list
	// and the expectation taken a moment apart can differ by a directory that came or went. Take
	// them again until they agree; a list that is really wrong never does.
	var list, want []string
	var known bool
	for range 5 {
		spec := Spec{WriteRoots: []string{work}, PrivatePaths: []string{home}}
		workOutReadable(&spec)
		list, known = takeReadableFile(spec.ReadableFile)
		want = readableAround([]string{home})
		if known && strings.Join(list, ",") == strings.Join(want, ",") {
			break
		}
	}
	if !known || strings.Join(list, ",") != strings.Join(want, ",") {
		t.Errorf("list = %v (known %v), want %v", list, known, want)
	}
	noRoots := Spec{PrivatePaths: []string{home}}
	workOutReadable(&noRoots)
	if noRoots.ReadableFile != "" {
		t.Error("it worked out a list for a command that is not confined")
	}
}

func TestWorkOutReadableGivesUpQuietly(t *testing.T) {
	home, work := userHome(t)
	spec := Spec{WriteRoots: []string{work}, PrivatePaths: []string{home}}

	t.Run("a listing that fails", func(t *testing.T) {
		failListings(t)
		s := spec
		workOutReadable(&s)
		if s.ReadableFile != "" {
			t.Errorf("a failed listing was passed on: %q", s.ReadableFile)
		}
	})
	t.Run("a list that cannot be written down", func(t *testing.T) {
		prev := jsonMarshal
		t.Cleanup(func() { jsonMarshal = prev })
		jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("no") }
		s := spec
		workOutReadable(&s)
		if s.ReadableFile != "" {
			t.Errorf("got %q", s.ReadableFile)
		}
	})
	t.Run("nowhere to put it", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
		s := spec
		workOutReadable(&s)
		if s.ReadableFile != "" {
			t.Errorf("got %q", s.ReadableFile)
		}
	})
	t.Run("a write that fails", func(t *testing.T) {
		prev := writeReadable
		t.Cleanup(func() { writeReadable = prev })
		writeReadable = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
		s := spec
		workOutReadable(&s)
		if s.ReadableFile != "" {
			t.Errorf("got %q", s.ReadableFile)
		}
	})
}

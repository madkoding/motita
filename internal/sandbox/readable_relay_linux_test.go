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

// leftFD leaves a list the way the isolation child does for the child in the namespaces: in a file
// with no name, on a descriptor the reader owns and closes.
func leftFD(t *testing.T, content string) int {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "readable")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

// What the isolation child already worked out is used as it is: the child in the namespaces lists
// nothing, so a directory it cannot list does not stop the command.
func TestConfineUsesWhatWasWorkedOutBeforeTheRelay(t *testing.T) {
	needLandlock(t)
	rules := countRules(t)
	failListings(t)
	granted := t.TempDir()
	spec := Spec{WriteRoots: []string{t.TempDir()}, PrivatePaths: []string{"/home"},
		ReadableFD: leftFD(t, `["`+granted+`"]`)}
	if err := confine(spec); err != nil {
		t.Fatalf("a listing that fails inside the namespace stopped the command: %v", err)
	}
	if *rules != 3 { // the workspace (written and read) and the one entry granted around the private tree
		t.Errorf("rules = %d, want 3", *rules)
	}
	if _, err := syscall.Dup(spec.ReadableFD); err == nil {
		t.Error("the descriptor was not closed")
	}
	// Nothing left readable is an answer too, not a failure.
	spec.ReadableFD = leftFD(t, "[]")
	if err := confine(spec); err != nil {
		t.Errorf("an empty answer was taken for a failure: %v", err)
	}
	spec.ReadableFD = leftFD(t, "null")
	if err := confine(spec); err != nil {
		t.Errorf("a null answer was taken for a failure: %v", err)
	}
}

// Without a usable answer the child lists for itself, and fails closed when it cannot.
func TestConfineListsForItselfWithoutAUsableAnswer(t *testing.T) {
	needLandlock(t)
	countRules(t)
	failListings(t)
	for _, fd := range []int{0, 1 << 20, leftFD(t, "not json")} {
		spec := Spec{WriteRoots: []string{t.TempDir()}, PrivatePaths: []string{"/home"}, ReadableFD: fd}
		if err := confine(spec); err == nil || !strings.Contains(err.Error(), "stays readable") {
			t.Errorf("descriptor %d: got %v", fd, err)
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
		file := workOutReadable(&spec)
		if file == nil || spec.ReadableFD != readableFD {
			t.Fatalf("no list was left: %v, descriptor %d", file, spec.ReadableFD)
		}
		if _, err := os.Stat(file.Name()); !os.IsNotExist(err) {
			t.Errorf("the list has a name in a shared directory: %v", err)
		}
		// What the child reads is on the descriptor it inherits: stand in for it with a copy of this one.
		fd, err := syscall.Dup(int(file.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		list, known = takeReadableFile(fd)
		want = readableAround([]string{home})
		if known && strings.Join(list, ",") == strings.Join(want, ",") {
			break
		}
	}
	if !known || strings.Join(list, ",") != strings.Join(want, ",") {
		t.Errorf("list = %v (known %v), want %v", list, known, want)
	}
	noRoots := Spec{PrivatePaths: []string{home}}
	if workOutReadable(&noRoots) != nil || noRoots.ReadableFD != 0 {
		t.Error("it worked out a list for a command that is not confined")
	}
}

func TestWorkOutReadableGivesUpQuietly(t *testing.T) {
	home, work := userHome(t)
	spec := Spec{WriteRoots: []string{work}, PrivatePaths: []string{home}}

	t.Run("a listing that fails", func(t *testing.T) {
		failListings(t)
		s := spec
		if workOutReadable(&s) != nil || s.ReadableFD != 0 {
			t.Error("a failed listing was passed on")
		}
	})
	t.Run("a list that cannot be written down", func(t *testing.T) {
		prev := jsonMarshal
		t.Cleanup(func() { jsonMarshal = prev })
		jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("no") }
		s := spec
		if workOutReadable(&s) != nil || s.ReadableFD != 0 {
			t.Error("a list that could not be left was passed on")
		}
	})
	t.Run("nowhere to put it", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
		s := spec
		if workOutReadable(&s) != nil || s.ReadableFD != 0 {
			t.Error("a list that could not be left was passed on")
		}
	})
	t.Run("a write that fails", func(t *testing.T) {
		prev := writeReadable
		t.Cleanup(func() { writeReadable = prev })
		writeReadable = func(*os.File, []byte) error { return errors.New("disk full") }
		s := spec
		if workOutReadable(&s) != nil || s.ReadableFD != 0 {
			t.Error("a list that could not be left was passed on")
		}
	})
}

// The list reaches the child in the namespaces on the descriptor it inherits, and the command runs
// confined with it.
func TestRelayHandsTheListOnAnInheritedDescriptor(t *testing.T) {
	skipWithoutNamespaces(t)
	needLandlock(t)
	exits := stubChildExit(t)
	home, work := userHome(t)
	spec := Spec{Command: "sh", Args: []string{"-c", "exit 3"}, Limits: Limits{NoNetwork: true},
		WriteRoots: []string{work}, PrivatePaths: []string{home}}
	if err := relayInNamespace(spec); err != nil {
		t.Fatal(err)
	}
	if len(*exits) != 1 || (*exits)[0] != 3 {
		t.Fatalf("exits = %v, want the command's own code", *exits)
	}
}

// A list that cannot be written to its file is not passed on.
func TestWriteReadableReportsAWriteThatFails(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "readable")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := writeReadable(f, []byte("[]")); err == nil {
		t.Error("a write to a closed file was taken for done")
	}
}

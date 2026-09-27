package skills

// Deleting a document. It is the one irreversible operation the library has, which is why it is
// a method of its own rather than a flag on Archive, and why the cases worth testing are the
// refusals: a built-in that no filesystem call can touch, a name that is not there, and a removal
// that failed for a reason the caller has to be told about.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeleteRemovesTheDocument: the operation does exactly what it says and nothing less — the
// file is gone from the directory, the index no longer offers it, and a read by name does not
// find it.
func TestDeleteRemovesTheDocument(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("one", "# One\n\nbody\n"); err != nil {
		t.Fatal(err)
	}

	if err := l.Delete("one"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.Dir, "one.md")); !os.IsNotExist(err) {
		t.Errorf("the document is still on disk: %v", err)
	}
	if _, err := l.Get("one"); err == nil {
		t.Error("a deleted document must not still be readable")
	}
	all, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List = %v, want an empty library", all)
	}
}

// TestDeleteRefusesABuiltinAndSaysWhy: a name that is not on disk resolves to the procedure inside
// the binary, and no call on the filesystem can remove that. Reporting success would be a lie the
// user discovers by looking, so the refusal says what the name actually is. A document of the same
// name written on disk IS deletable, and that is the arrangement that brings the shipped one back.
func TestDeleteRefusesABuiltinAndSaysWhy(t *testing.T) {
	l := builtinLib(t)

	err := l.Delete("files-and-directories")
	if err == nil {
		t.Fatal("deleting a built-in must be refused")
	}
	// The refusal has to say WHY, and the check is on the words rather than on where a hyphen
	// falls: what matters is that the caller is told the name is built in.
	if !strings.Contains(strings.ReplaceAll(err.Error(), "-", " "), "built in") {
		t.Errorf("err = %v, want it to say the skill is built in", err)
	}
	if _, err := l.Get("files-and-directories"); err != nil {
		t.Errorf("the refusal must not have touched the shipped procedure: %v", err)
	}

	// The document on disk shadows the shipped one, and deleting the shadow is how the shipped
	// one comes back: the same order every other operation follows.
	if _, err := l.Save("files-and-directories", "# Mine\n\nshort\n"); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete("files-and-directories"); err != nil {
		t.Fatalf("a document on disk must be deletable: %v", err)
	}
	got, err := l.Get("files-and-directories")
	if err != nil {
		t.Fatalf("the shipped procedure must come back after deleting the shadow: %v", err)
	}
	if !strings.HasPrefix(got.Path, builtinPrefix) {
		t.Errorf("after deleting the shadow the procedure came from %q, want the binary", got.Path)
	}
}

// TestDeleteReportsAMissingNameAndAnEmptyOne: the name goes through the same sanitiser as every
// other call, so a name that sanitises to nothing is refused rather than resolving to a path. And
// a name the library does not hold is a failure to report, not a quiet success.
func TestDeleteReportsAMissingNameAndAnEmptyOne(t *testing.T) {
	l := newLib(t)

	err := l.Delete("")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("Delete(\"\") = %v, want a refusal that says the name is empty", err)
	}
	if err := l.Delete("nothing-here"); err == nil {
		t.Error("deleting a name the library does not hold must fail")
	}
}

// TestDeleteReportsABrokenEmbeddedLibrary: the refusal asks the binary what the name is, and a
// binary that cannot answer is a problem to report rather than a "not found". This is the same
// branch Get takes: a read of the embedded set that fails means the build is broken, and the
// message has to say that instead of blaming the name.
func TestDeleteReportsABrokenEmbeddedLibrary(t *testing.T) {
	l := builtinLib(t)
	boom := errors.New("corrupt binary")

	old := builtinReadDir
	builtinReadDir = func(string) ([]fs.DirEntry, error) { return nil, boom }
	defer func() { builtinReadDir = old }()

	if err := l.Delete("nothing-here"); !errors.Is(err, boom) {
		t.Errorf("Delete = %v, want the read failure rather than a not-found", err)
	}
}

// TestDeleteReportsAFailedRemoval: the name IS there, so "not found" is not the answer — the
// removal itself failed. The path is a directory with a file inside it, which exists for a stat
// and cannot be removed, and the caller has to be told the document is still there.
func TestDeleteReportsAFailedRemoval(t *testing.T) {
	l := newLib(t)
	dir := filepath.Join(l.Dir, "locked.md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inside"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := l.Delete("locked")
	if err == nil {
		t.Fatal("a document that could not be removed must be reported, not reported as deleted")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("err = %v, want it to name the document", err)
	}
}

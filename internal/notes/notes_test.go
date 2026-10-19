package notes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, v *Vault, p, body string) {
	t.Helper()
	if _, err := v.Write(p, body, false); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func TestCleanAcceptsNotePathsOnly(t *testing.T) {
	for _, ok := range []string{"a.md", "dir/a.md", " dir\\a.MD "} {
		if _, err := Clean(ok); err != nil {
			t.Errorf("Clean(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a.txt", "../a.md", "a/../b.md", "/a.md", ".h/a.md", "a//b.md", "./a.md", "a\x00.md", strings.Repeat("x", 300) + ".md"} {
		if _, err := Clean(bad); !errors.Is(err, ErrBadPath) {
			t.Errorf("Clean(%q) = %v, want ErrBadPath", bad, err)
		}
	}
}

func TestWriteReadListDelete(t *testing.T) {
	v := Open(filepath.Join(t.TempDir(), "vault"))
	if list := v.List(); len(list) != 0 {
		t.Fatalf("empty vault: %v", list)
	}
	n, err := v.Write("trips/Japan.md", "# Japan", true)
	if err != nil || n.Title != "Japan" || n.Size != 7 {
		t.Fatalf("write = %+v %v", n, err)
	}
	if _, err := v.Write("trips/Japan.md", "x", true); !errors.Is(err, ErrExists) {
		t.Fatalf("create over existing = %v", err)
	}
	if _, err := v.Write("trips/Japan.md", "# Japan 2", false); err != nil {
		t.Fatal(err)
	}
	body, mod, err := v.Read("trips/Japan.md")
	if err != nil || body != "# Japan 2" || mod.IsZero() {
		t.Fatalf("read = %q %v %v", body, mod, err)
	}
	// Hidden files, other extensions and the writer's temp files are not notes.
	os.WriteFile(filepath.Join(v.Root(), ".hidden.md"), nil, 0o644)
	os.WriteFile(filepath.Join(v.Root(), "x.txt"), nil, 0o644)
	os.MkdirAll(filepath.Join(v.Root(), ".obsidian"), 0o755)
	os.WriteFile(filepath.Join(v.Root(), ".obsidian", "a.md"), nil, 0o644)
	os.Symlink(filepath.Join(v.Root(), "trips"), filepath.Join(v.Root(), "link"))
	if list := v.List(); len(list) != 1 || list[0].Path != "trips/Japan.md" {
		t.Fatalf("list = %+v", list)
	}
	if err := v.Delete("trips/Japan.md"); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete("trips/Japan.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice = %v", err)
	}
	if _, _, err := v.Read("trips/Japan.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read gone = %v", err)
	}
}

func TestBadPathsAreRefusedEverywhere(t *testing.T) {
	v := Open(t.TempDir())
	if _, _, err := v.Read("../x.md"); !errors.Is(err, ErrBadPath) {
		t.Error(err)
	}
	if _, err := v.Write("../x.md", "", false); !errors.Is(err, ErrBadPath) {
		t.Error(err)
	}
	if err := v.Delete("../x.md"); !errors.Is(err, ErrBadPath) {
		t.Error(err)
	}
	if err := v.Rename("../x.md", "a.md"); !errors.Is(err, ErrBadPath) {
		t.Error(err)
	}
	if err := v.Rename("a.md", "../x.md"); !errors.Is(err, ErrBadPath) {
		t.Error(err)
	}
}

func TestSymlinksCannotLeadOutOfTheVault(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.md"), []byte("s"), 0o644)
	v := Open(t.TempDir())
	os.Symlink(outside, filepath.Join(v.Root(), "out"))
	os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(v.Root(), "leak.md"))
	if _, _, err := v.Read("out/secret.md"); !errors.Is(err, ErrBadPath) {
		t.Errorf("read through a folder link = %v", err)
	}
	if _, _, err := v.Read("leak.md"); !errors.Is(err, ErrBadPath) {
		t.Errorf("read a file link = %v", err)
	}
	if _, err := v.Write("out/new/x.md", "x", false); !errors.Is(err, ErrBadPath) {
		t.Errorf("write through a folder link = %v", err)
	}
	if err := v.Delete("out/secret.md"); !errors.Is(err, ErrBadPath) {
		t.Errorf("delete through a folder link = %v", err)
	}
	write(t, v, "mine.md", "m")
	if err := v.Rename("out/secret.md", "x.md"); !errors.Is(err, ErrBadPath) {
		t.Errorf("rename out = %v", err)
	}
	if err := v.Rename("mine.md", "out/x.md"); !errors.Is(err, ErrBadPath) {
		t.Errorf("rename in = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); err == nil {
		t.Error("a folder was created outside the vault")
	}
}

func TestLimits(t *testing.T) {
	v := Open(t.TempDir())
	if _, err := v.Write("big.md", strings.Repeat("x", MaxNoteBytes+1), false); !errors.Is(err, ErrTooBig) {
		t.Errorf("big write = %v", err)
	}
	os.WriteFile(filepath.Join(v.Root(), "huge.md"), make([]byte, MaxNoteBytes+1), 0o644)
	if _, _, err := v.Read("huge.md"); !errors.Is(err, ErrTooBig) {
		t.Errorf("big read = %v", err)
	}
}

func TestTooManyNotes(t *testing.T) {
	v := Open(t.TempDir())
	for i := 0; i < MaxNotes; i++ {
		os.WriteFile(filepath.Join(v.Root(), "n"+strings.Repeat("a", 1)+itoa(i)+".md"), nil, 0o644)
	}
	if _, err := v.Write("one-more.md", "", false); !errors.Is(err, ErrTooMany) {
		t.Errorf("write past the limit = %v", err)
	}
	os.WriteFile(filepath.Join(v.Root(), "extra.md"), nil, 0o644)
	if list := v.List(); len(list) != MaxNotes {
		t.Errorf("list = %d, want the cap", len(list))
	}
	// An existing note can still be saved.
	if _, err := v.Write("na1.md", "edited", false); err != nil {
		t.Errorf("edit at the limit = %v", err)
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(digits[i%10]) + s
	}
	return s
}

func TestRename(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "A")
	write(t, v, "b.md", "B")
	if err := v.Rename("a.md", "b.md"); !errors.Is(err, ErrExists) {
		t.Errorf("onto existing = %v", err)
	}
	if err := v.Rename("zz.md", "c.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing source = %v", err)
	}
	if err := v.Rename("a.md", "deep/er/c.md"); err != nil {
		t.Fatal(err)
	}
	if body, _, err := v.Read("deep/er/c.md"); err != nil || body != "A" {
		t.Errorf("moved = %q %v", body, err)
	}
}

func TestFailuresOfTheDisk(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "file.md", "x")
	// A folder cannot be made where a note is.
	if _, err := v.Write("file.md/in.md", "x", false); err == nil {
		t.Error("write under a note")
	}
	if err := v.Rename("file.md", "file.md/in.md"); err == nil {
		t.Error("rename under a note")
	}
	// The disk calls themselves failing.
	oldRead, oldTemp, oldMove := readFile, createTemp, moveFile
	defer func() { readFile, createTemp, moveFile = oldRead, oldTemp, oldMove }()
	readFile = func(string) ([]byte, error) { return nil, errors.New("boom") }
	if _, _, err := v.Read("file.md"); err == nil {
		t.Error("read failure")
	}
	readFile = oldRead
	createTemp = func(string, string) (*os.File, error) { return nil, errors.New("boom") }
	if _, err := v.Write("n.md", "x", false); err == nil {
		t.Error("temp failure")
	}
	// A file that cannot be written to (already closed).
	createTemp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		f.Close()
		return f, err
	}
	if _, err := v.Write("n.md", "x", false); err == nil {
		t.Error("write failure")
	}
	createTemp = oldTemp
	moveFile = func(string, string) error { return errors.New("boom") }
	if _, err := v.Write("n.md", "x", false); err == nil {
		t.Error("move failure")
	}
	if left, _ := filepath.Glob(filepath.Join(v.Root(), ".motita-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestListSkipsWhatItCannotRead(t *testing.T) {
	// A root that does not exist, and one that is a file, hold no notes.
	if list := Open(filepath.Join(t.TempDir(), "none")).List(); len(list) != 0 {
		t.Errorf("%v", list)
	}
	f := filepath.Join(t.TempDir(), "x.md")
	os.WriteFile(f, nil, 0o644)
	if list := Open(f).List(); len(list) != 0 {
		t.Errorf("%v", list)
	}
}

func TestWriteOverAFolderFailsCleanly(t *testing.T) {
	v := Open(t.TempDir())
	os.MkdirAll(filepath.Join(v.Root(), "d.md"), 0o755)
	if _, err := v.Write("d.md", "x", false); err == nil {
		t.Error("write over a folder")
	}
	if left, _ := filepath.Glob(filepath.Join(v.Root(), ".motita-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

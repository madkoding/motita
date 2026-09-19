package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The failure branches. A library is a directory on a disk that can be full, read-only, or
// pulled out from under the process, and a tool that reports a clear error for those is worth
// more than one that panics or lies.

// TestSearchSkipsADocumentThatCannotBeRead: one unreadable file must not fail the search. The
// model asking for a procedure needs the other results more than it needs a diagnostic.
func TestSearchSkipsADocumentThatCannotBeRead(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("readable", "# Readable\n\nalpha procedure\n"); err != nil {
		t.Fatal(err)
	}
	// A symlink whose target does not exist: it has the right extension and cannot be read.
	if err := os.Symlink(filepath.Join(l.Dir, "gone"), filepath.Join(l.Dir, "broken.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	hits, err := l.Search("alpha", 10)
	if err != nil {
		t.Fatalf("a broken document must not fail the search: %v", err)
	}
	if len(hits) != 1 || hits[0].Name != "readable" {
		t.Errorf("the readable skill must still be found, got %v", hits)
	}
}

// TestSaveReportsACreateFailure: a temporary file that cannot be created means the directory
// is unusable, and the message has to say so rather than reporting success.
func TestSaveReportsACreateFailure(t *testing.T) {
	l := newLib(t)
	boom := errors.New("no space left on device")

	old := createTemp
	createTemp = func(dir, pattern string) (*os.File, error) { return nil, boom }
	defer func() { createTemp = old }()

	_, err := l.Save("x", "# X\n\nbody\n")
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the create failure", err)
	}
}

// TestSaveReportsARenameFailure: the write succeeded but the document was not installed, so
// the library was not changed and the caller must not be told it was.
func TestSaveReportsARenameFailure(t *testing.T) {
	l := newLib(t)
	boom := errors.New("read-only file system")

	old := renameFile
	renameFile = func(oldpath, newpath string) error { return boom }
	defer func() { renameFile = old }()

	_, err := l.Save("x", "# X\n\nbody\n")
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the rename failure", err)
	}
	// The document must not exist: a failed install leaves the library as it was.
	if _, err := l.Get("x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a failed save must not leave a document behind, got %v", err)
	}
}

// TestSaveReportsAMissingDirectoryItCannotCreate: the directory has to be created, and if that
// fails the message names the path, which is what the user needs to fix it.
func TestSaveReportsAMissingDirectoryItCannotCreate(t *testing.T) {
	root := t.TempDir()
	// A FILE where the directory needs to be: MkdirAll cannot succeed over it.
	blocker := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := New(filepath.Join(blocker, "skills"))

	_, err := l.Save("x", "# X\n\nbody\n")
	if err == nil {
		t.Fatal("Save must fail when the directory cannot be created")
	}
	if !strings.Contains(err.Error(), "skills") {
		t.Errorf("the error must name the path: %v", err)
	}
}

// TestListReportsAnUnreadableDirectory: a directory that exists but cannot be read is a real
// problem, unlike one that is simply absent, and the two must not be confused.
func TestListReportsAnUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "locked")
	if err := os.MkdirAll(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	l := New(dir)
	if _, err := l.List(); err == nil {
		t.Error("an unreadable directory must be reported, not reported as empty")
	}
	if _, err := l.Search("x", 10); err == nil {
		t.Error("Search must report it too")
	}
}

// TestSearchReportsAnUnreadableDirectory: same distinction, on the search path.
func TestReadReportsAMissingFile(t *testing.T) {
	l := newLib(t)
	if _, err := l.read(filepath.Join(l.Dir, "nothing.md")); err == nil {
		t.Error("reading a missing file must fail")
	}
}

// TestReadRefusesInvalidUTF8: a binary file with a .md extension is not a procedure, and
// pulling it into the context would waste the window on noise.
func TestReadRefusesInvalidUTF8(t *testing.T) {
	l := newLib(t)
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(l.Dir, "binary.md")
	if err := os.WriteFile(p, []byte{0xff, 0xfe, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.read(p); err == nil {
		t.Error("invalid UTF-8 must be refused")
	}
}

// TestSearchOverAnUnreadableLibrary: the model has to be told the library is broken rather
// than told there is nothing in it, because the two lead to different next steps.
func TestSearchOverAnUnreadableLibrary(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "locked")
	if err := os.MkdirAll(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	if _, err := New(dir).Search("x", 10); err == nil {
		t.Error("an unreadable library must be reported")
	}
}

// TestGetOverAnUnreadableFile: the file exists but cannot be opened, which is not the same as
// it not existing, and the difference reaches the user.
func TestGetOverAnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits")
	}
	l := newLib(t)
	if _, err := l.Save("x", "# X\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(l.Dir, "x.md")
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(p, 0o644)

	if _, err := l.Get("x"); err == nil {
		t.Error("an unreadable document must be reported")
	} else if errors.Is(err, ErrNotFound) {
		t.Error("unreadable is not the same as missing")
	}
}

// TestSaveRefusesADocumentTheIndexWouldRefuse: the cap has to be enforced on the way in as
// well as on the way out, or a session could write what it can never read back.
func TestSaveEnforcesTheCapOnTheWayIn(t *testing.T) {
	l := newLib(t)
	l.MaxFileBytes = 64
	body := "# X\n\n" + strings.Repeat("a", 100)

	if _, err := l.Save("x", body); err == nil {
		t.Error("a document past the cap must be refused at write time")
	}
}

// TestSaveReportsAWriteFailure: the temporary file exists but cannot be written to.
func TestSaveReportsAWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits")
	}
	l := newLib(t)
	// Chmod, not MkdirAll: MkdirAll leaves an existing directory's mode alone.
	if err := os.Chmod(l.Dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(l.Dir, 0o755)

	if _, err := l.Save("x", "# X\n\nbody\n"); err == nil {
		t.Error("a write into a read-only directory must be reported")
	}
}

// TestSearchWhenADocumentVanishesMidSearch: the index read the directory, and by the time the
// bodies are loaded one file is gone. That is a real race — another session editing the library
// — and the branch that skips it exists for exactly this.
func TestSearchWhenADocumentVanishesMidSearch(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("kept", "# Kept\n\nalpha\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("vanished", "# Vanished\n\nalpha\n"); err != nil {
		t.Fatal(err)
	}

	old := readBody
	defer func() { readBody = old }()
	readBody = func(lib *Library, path string) (string, error) {
		if strings.Contains(path, "vanished") {
			return "", errors.New("file disappeared")
		}
		return old(lib, path)
	}

	hits, err := l.Search("alpha", 10)
	if err != nil {
		t.Fatalf("one vanishing document must not fail the search: %v", err)
	}
	if len(hits) != 1 || hits[0].Name != "kept" {
		t.Errorf("the surviving skill must be found, got %v", hits)
	}
}

// TestSaveReportsAWriteFailure covers the write itself, with the temporary file created but
// the disk refusing the bytes.
func TestSaveReportsAWriteFailureOnTheTemporaryFile(t *testing.T) {
	l := newLib(t)
	boom := errors.New("input/output error")

	old := writeTemp
	writeTemp = func(f *os.File, body string) (int, error) { return 0, boom }
	defer func() { writeTemp = old }()

	if _, err := l.Save("x", "# X\n\nbody\n"); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the write failure", err)
	}
}

// TestSaveReportsACloseFailure: the close flushes, so a failure there means the bytes may not
// have reached the disk. Reporting success would be a lie.
func TestSaveReportsACloseFailure(t *testing.T) {
	l := newLib(t)
	boom := errors.New("flush failed")

	old := closeTemp
	closeTemp = func(f *os.File) error { f.Close(); return boom }
	defer func() { closeTemp = old }()

	if _, err := l.Save("x", "# X\n\nbody\n"); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the close failure", err)
	}
}

// TestListOnAMissingDirectoryIsEmpty: the distinction the code makes is between absent and
// unreadable. Absent is where everyone starts, and it is not an error.
func TestListOnAMissingDirectoryIsEmpty(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "never", "created"))

	all, err := l.List()
	if err != nil {
		t.Fatalf("an absent library must not be an error: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("got %d skills from an absent library", len(all))
	}
}

// TestSearchRanksABodyMatchLast: a document that merely mentions the phrase must rank below
// one whose name, title or summary is about it, so the useful result is on top.
func TestSearchRanksABodyMatchLast(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("about-it", "# About it\n\nThis one is about widgets.\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("mentions-it", "# Other thing\n\nA procedure about something else.\n\nLater on it happens to mention widgets once.\n"); err != nil {
		t.Fatal(err)
	}

	hits, err := l.Search("widgets", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].Name != "about-it" {
		t.Errorf("the closer match must come first, got %q", hits[0].Name)
	}
	if hits[1].Name != "mentions-it" {
		t.Errorf("the body-only match must come last, got %q", hits[1].Name)
	}
}

// TestSearchMatchesWordsNotTheLiteralPhrase: the model is told to describe the work in plain
// language, and a description is a sentence — "flash a board over usb" is in no document
// verbatim, while every word of it is. Matching the phrase as one substring answers with
// nothing, which tells the model the library is empty when it holds the procedure it needs.
func TestSearchMatchesWordsNotTheLiteralPhrase(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("nrf", "# NRF firmware\n\nUse west and the SDK to produce a UF2 image for the board.\n"); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{
		"flash a board over USB",
		"board",
		"sdk west",
		"produce an image",
	} {
		hits, err := l.Search(q, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 {
			t.Errorf("Search(%q) found nothing, but the words are in the document", q)
		}
	}
}

// TestSearchDropsWordsTooShortToDiscriminate: "a" and "of" are in every document, so matching
// them would rank the whole library equally and the order would carry no information.
func TestSearchDropsWordsTooShortToDiscriminate(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("alpha", "# Alpha\n\nabout a thing\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("beta", "# Beta\n\nabout a thing\n"); err != nil {
		t.Fatal(err)
	}

	// Only stop-words: nothing to discriminate on, so nothing matches. That is better than
	// returning the library in an order that pretends to be a ranking.
	hits, err := l.Search("a of to", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("a query of nothing but short words must not rank anything, got %v", hits)
	}
}

// TestSearchPrefersTheTitleOverTheBody: a document whose TITLE is about the work outranks one
// that merely mentions the word somewhere in its body.
func TestSearchPrefersTheTitleOverTheBody(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("guide-one", "# Widget handling\n\nHow to handle them.\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("other", "# Something else\n\nA long procedure about nothing much.\n\n## Aside\n\nOne day we saw widgets.\n"); err != nil {
		t.Fatal(err)
	}

	hits, err := l.Search("widgets", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits", len(hits))
	}
	if hits[0].Name != "guide-one" {
		t.Errorf("the title match must come first, got %q", hits[0].Name)
	}
}

// TestSearchPrefersTheDocumentThatCoversMoreOfTheQuery: with the same field matched, the one
// covering more of the request is the one being asked for.
func TestSearchPrefersTheDocumentThatCoversMoreOfTheQuery(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("partial", "# Partial\n\nA note about boards.\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("full", "# Full\n\nAbout boards and flashing them over serial.\n"); err != nil {
		t.Fatal(err)
	}

	hits, err := l.Search("boards flashing serial", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 {
		t.Fatalf("got %d hits", len(hits))
	}
	if hits[0].Name != "full" {
		t.Errorf("the document covering more of the query must come first, got %q", hits[0].Name)
	}
}

// TestSearchBreaksATieOnTheEarliestWord: when two documents cover the same amount, the one
// matching the word the user thought of FIRST is closer to the intent.
func TestSearchBreaksATieOnTheEarliestWord(t *testing.T) {
	l := newLib(t)
	// Each document matches exactly ONE word of the query, and a different one, so they cover
	// the same amount and only the earliest-match rule can separate them.
	if _, err := l.Save("only-first", "# One\n\nnothing else here\n\nflashing\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("only-second", "# Two\n\nnothing else here\n\nserialport\n"); err != nil {
		t.Fatal(err)
	}

	hits, err := l.Search("flashing serialport", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want the two single-word matches", len(hits))
	}
	if hits[0].Name != "only-first" {
		t.Errorf("the earliest matching word must win the tie, got %q first", hits[0].Name)
	}
}

// TestSearchFindsTheSingularForAPlural: the model describes work in the words it has, and the
// document was written in the words someone else had. "widgets" against "widget" is the case
// that actually happens, and a lookup that misses it misses constantly.
func TestSearchFindsTheSingularForAPlural(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("parts", "# Parts\n\nHow to handle a widget.\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("boxes", "# Boxes\n\nHow to open a box.\n"); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"widgets", "boxes"} {
		hits, err := l.Search(q, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 {
			t.Errorf("Search(%q) found nothing, but the document has the singular", q)
		}
	}
}

// TestFormsLeavesShortWordsAlone: stripping an "s" from "was" or "es" from "does" would make a
// short word match half the library, so short words are matched as written.
func TestFormsLeavesShortWordsAlone(t *testing.T) {
	for _, w := range []string{"was", "gas", "its"} {
		if got := forms(w); len(got) != 1 || got[0] != w {
			t.Errorf("forms(%q) = %v, want it unchanged", w, got)
		}
	}
	if got := forms("goes"); len(got) != 2 {
		t.Errorf("forms(goes) = %v, want both spellings", got)
	}
	if got := forms("widgets"); len(got) != 2 || got[1] != "widget" {
		t.Errorf("forms(widgets) = %v", got)
	}
}

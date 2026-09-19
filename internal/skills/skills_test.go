package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLib(t *testing.T) *Library {
	t.Helper()
	return New(t.TempDir())
}

// TestNameSanitisesInsteadOfRejecting: the name comes from a model, and a name with a path
// separator would let a write escape the library directory. Normalising is right where
// rejecting the whole call over a space would not be.
func TestNameSanitisesInsteadOfRejecting(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Build the thing", "build-the-thing"},
		{"  Zephyr NRF Build  ", "zephyr-nrf-build"},
		{"zephyr.md", "zephyr"},
		{"a/b/c", "a-b-c"},
		{"../../etc/passwd", "etc-passwd"},
		{"keep_underscore", "keep_underscore"},
		{"UPPER", "upper"},
		{"dots.and.slashes/", "dots-and-slashes"},
		{"", ""},
		{"...", ""},
		{"!!!", ""},
	} {
		if got := Name(tc.in); got != tc.want {
			t.Errorf("Name(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNameIsBounded: the name is a handle that appears in a list the model reads, so it stays
// short enough to read.
func TestNameIsBounded(t *testing.T) {
	long := strings.Repeat("a", 200)
	if got := Name(long); len(got) > 64 {
		t.Errorf("Name returned %d characters, want at most 64", len(got))
	}
}

// TestSaveAndGet: the round trip is the whole contract.
func TestSaveAndGet(t *testing.T) {
	l := newLib(t)
	body := "# Build the firmware\n\nUse this when the board will not flash.\n\n1. Erase.\n2. Flash.\n"

	saved, err := l.Save("Build Firmware", body)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Name != "build-firmware" {
		t.Errorf("name = %q", saved.Name)
	}
	if saved.Title != "Build the firmware" {
		t.Errorf("title = %q", saved.Title)
	}
	if !strings.HasPrefix(saved.Summary, "Use this when") {
		t.Errorf("summary = %q", saved.Summary)
	}

	got, err := l.Get("build-firmware")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != body {
		t.Errorf("the body did not survive the round trip")
	}
}

// TestSaveReplaces: a procedure that turned out to be wrong is worse than none, so the model
// has to be able to correct it. An append-only library is how mistakes accumulate.
func TestSaveReplaces(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("x", "# X\n\nfirst\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("x", "# X\n\nsecond\n"); err != nil {
		t.Fatal(err)
	}
	got, err := l.Get("x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Body, "second") {
		t.Errorf("the replacement did not land: %q", got.Body)
	}
	all, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("replacing must not create a second document, got %d", len(all))
	}
}

// TestSearchRanksANameMatchFirst: a document that IS about the topic must outrank one that
// merely mentions it, or the useful result is buried.
func TestSearchRanksANameMatchFirst(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("serial-port", "# Serial port\n\nHow to work with a serial port.\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("unrelated", "# Unrelated\n\nThis one mentions a serial port once in passing.\n"); err != nil {
		t.Fatal(err)
	}

	hits, err := l.Search("serial port", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].Name != "serial-port" {
		t.Errorf("the name match must come first, got %q then %q", hits[0].Name, hits[1].Name)
	}
	if hits[0].Body == "" {
		t.Error("a search result must carry the body, so the model can decide whether to read it")
	}
}

// TestSearchLooksThroughTheBody: the user asking "how do I flash a board" has no idea what the
// skill was called, and a titles-only search would find nothing.
func TestSearchLooksThroughTheBody(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("nrf", "# NRF\n\nUse west and the SDK to produce a UF2 image.\n"); err != nil {
		t.Fatal(err)
	}

	hits, err := l.Search("UF2 image", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("the body must be searched, got %d hits", len(hits))
	}
}

// TestSearchOnAnEmptyLibraryIsNotAnError: an empty library is where everyone starts. The model
// must be told there is nothing, so it proceeds on its own knowledge instead of retrying.
func TestSearchOnAnEmptyLibraryIsNotAnError(t *testing.T) {
	l := newLib(t)
	hits, err := l.Search("anything", 10)
	if err != nil {
		t.Fatalf("an empty library is not a failure: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits from an empty library", len(hits))
	}
	all, err := l.List()
	if err != nil {
		t.Fatalf("List on a missing directory must not fail: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("got %d skills from a missing directory", len(all))
	}
}

// TestSearchWithAnEmptyQuery: an empty query cannot rank anything, and answering it as a
// failure is clearer than returning the whole library in an arbitrary order.
func TestSearchWithAnEmptyQuery(t *testing.T) {
	l := newLib(t)
	if _, err := l.Search("   ", 10); err == nil {
		t.Error("an empty query must be refused")
	}
}

// TestGetUnknownIsNotFound: the caller distinguishes "no such skill" from an I/O failure,
// because the model is told something different for each.
func TestGetUnknownIsNotFound(t *testing.T) {
	l := newLib(t)
	if _, err := l.Get("nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, err := l.Get(""); err == nil {
		t.Error("an empty name must be refused")
	}
}

// TestSaveRefusesWhatItCannotStore: an oversized document, an empty body and invalid UTF-8 are
// refused with a message that says why, because the model has to react to it.
func TestSaveRefusesWhatItCannotStore(t *testing.T) {
	l := newLib(t)
	l.MaxFileBytes = 100

	if _, err := l.Save("x", strings.Repeat("a", 200)); err == nil {
		t.Error("an oversized skill must be refused")
	}
	if _, err := l.Save("x", "   \n  "); err == nil {
		t.Error("an empty body must be refused")
	}
	if _, err := l.Save("x", string([]byte{0xff, 0xfe, 0xfd})); err == nil {
		t.Error("invalid UTF-8 must be refused")
	}
	if _, err := l.Save("", "# x\n"); err == nil {
		t.Error("an empty name must be refused")
	}
}

// TestTheIndexSkipsWhatItCannotRead: one bad file must not make the whole library unusable.
// The model reading the index must still see the good ones.
func TestTheIndexSkipsWhatItCannotRead(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("good", "# Good\n\nfine\n"); err != nil {
		t.Fatal(err)
	}

	// A directory that looks like a skill, a file that is not UTF-8, and a leftover temporary
	// file: none of these is a skill, and each is skipped for a different reason.
	if err := os.MkdirAll(filepath.Join(l.Dir, "dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.Dir, "binary.md"), []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.Dir, ".tmp-1.md"), []byte("# Half a write\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.Dir, "notes.txt"), []byte("not a skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].Name != "good" {
		t.Errorf("the index must hold only the readable skills, got %v", all)
	}
}

// TestAnOversizedSkillIsNotReadIntoTheIndex: reading every body to build an index would pull
// the whole library into memory, and from there into the context.
func TestAnOversizedSkillIsNotReadIntoTheIndex(t *testing.T) {
	l := newLib(t)
	l.MaxFileBytes = 50
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.Dir, "huge.md"), []byte(strings.Repeat("a", 500)), 0o644); err != nil {
		t.Fatal(err)
	}

	all, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("a document past the cap must not be indexed, got %v", all)
	}
	if _, err := l.Get("huge"); err == nil {
		t.Error("reading it must fail rather than return it")
	}
}

// TestParseFallsBackWhenThereIsNoHeading: a document the model wrote without a heading still
// has to appear in the index, with something readable as its title.
func TestParseFallsBackWhenThereIsNoHeading(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("bare", "just some text with no heading at all\n"); err != nil {
		t.Fatal(err)
	}
	got, err := l.Get("bare")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "bare" {
		t.Errorf("title = %q, want the name as a fallback", got.Title)
	}
	if got.Summary != "just some text with no heading at all" {
		t.Errorf("summary = %q", got.Summary)
	}
}

// TestParseFallsBackWhenThereIsNothingButAHeading: a heading with no prose still needs a
// summary line, because the index prints one.
func TestParseFallsBackWhenThereIsNothingButAHeading(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("only-heading", "# Just a heading\n"); err != nil {
		t.Fatal(err)
	}
	got, err := l.Get("only-heading")
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "(no summary)" {
		t.Errorf("summary = %q", got.Summary)
	}
}

// TestTheLibraryDirectoryIsCreatedOnTheFirstWrite: a session must be able to teach the agent
// something without anyone preparing the directory first.
func TestTheLibraryDirectoryIsCreatedOnTheFirstWrite(t *testing.T) {
	root := t.TempDir()
	l := New(filepath.Join(root, "deep", "nested", "skills"))

	if _, err := l.Save("x", "# X\n\nbody\n"); err != nil {
		t.Fatalf("Save into a missing directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "deep", "nested", "skills", "x.md")); err != nil {
		t.Errorf("the document was not created: %v", err)
	}
}

// TestAWriteLeavesNoTemporaryBehind: a session that dies mid-write must not leave a partial
// document that the next search reads as a procedure.
func TestAWriteLeavesNoTemporaryBehind(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("x", "# X\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// TestSearchHonoursTheLimit: the model asks for a number, and a library with more matches than
// that must not flood the context.
func TestSearchHonoursTheLimit(t *testing.T) {
	l := newLib(t)
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5"} {
		if _, err := l.Save(n, "# "+n+"\n\nabout alpha\n"); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := l.Search("alpha", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Errorf("got %d hits, want the requested 2", len(hits))
	}
	// A missing limit means the default, not everything.
	hits, err = l.Search("alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 5 {
		t.Errorf("got %d hits, want the default to return them all here", len(hits))
	}
}

// TestSearchIsCaseInsensitive: neither the model nor the user should have to match the
// capitalisation of a document they have not read.
func TestSearchIsCaseInsensitive(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("Zephyr Build", "# Zephyr Build\n\nUse west.\n"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"zephyr", "ZEPHYR", "ZePhYr"} {
		hits, err := l.Search(q, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 {
			t.Errorf("Search(%q) found %d, want 1", q, len(hits))
		}
	}
}

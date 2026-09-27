package skills

// The procedures embedded in the binary. They are the one part of the library the agent did not
// write and cannot lose, so the properties worth testing are about how they MEET the documents
// on disk: a shipped procedure must be readable, must be findable, and must lose to a document
// of the same name — never the other way round, or a correction would be silent and useless.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// builtinLib is a library that serves the embedded procedures.
func builtinLib(t *testing.T) *Library {
	t.Helper()
	l := New(t.TempDir())
	l.Builtins = true
	return l
}

// webSkill is the name of the shipped procedure about reaching the network. It is a constant
// because two tests name it: the one that checks the search finds it by the words of the job,
// and the one that checks a fresh library ships it at all.
const webSkill = "searching-the-web"

// TestTheShippedProceduresLoad is the base case: a fresh install has something to look up. An
// embed directive that names a folder the binary does not carry would leave the library empty
// and say nothing, which is the failure this catches.
func TestTheShippedProceduresLoad(t *testing.T) {
	all, err := builtinSkills()
	if err != nil {
		t.Fatalf("the shipped skills must load: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("the binary ships no skills at all")
	}
	for name, s := range all {
		if strings.TrimSpace(s.Body) == "" {
			t.Errorf("the shipped skill %q has an empty body", name)
		}
		if s.Title == name {
			// parse() falls back to the name when there is no heading, which for a document
			// we wrote ourselves means we shipped one without a title.
			t.Errorf("the shipped skill %q has no heading", name)
		}
		if s.Summary == "" || s.Summary == "(no summary)" {
			t.Errorf("the shipped skill %q has no summary, so the index says nothing about it", name)
		}
	}
}

// TestTheShippedProceduresAreFoundByTheWordsOfTheJob: the search is the only way the model
// reaches a skill it was not told about, so the words a person would actually use have to find
// it. These are the phrases the procedure claims to cover.
func TestTheShippedProceduresAreFoundByTheWordsOfTheJob(t *testing.T) {
	l := builtinLib(t)
	for _, q := range []string{
		"files and directories",
		"list a directory",
		"read a file",
		"find text in files",
		"copy a folder",
		"delete a directory",
		"create a file",
	} {
		hits, err := l.Search(q, 10)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if len(hits) == 0 {
			t.Errorf("Search(%q) found nothing in a library that ships a procedure about it", q)
		}
	}
}

// TestTheShippedWebProcedureIsFoundByTheWordsOfTheJob: the second shipped document answers a
// different question, and the check is on the DOCUMENT and not on "something matched". A query
// like "search the web" also hits the file procedure, because "search" is in its text, so a test
// that only asked for a hit would go green on the wrong answer and the web document could be
// deleted without anything failing.
func TestTheShippedWebProcedureIsFoundByTheWordsOfTheJob(t *testing.T) {
	l := builtinLib(t)
	for _, q := range []string{
		"search the web",
		"look up the documentation for a library",
		"fetch a web page",
		"research something online",
		"check a URL",
		"an API returns json and I need one field",
	} {
		hits, err := l.Search(q, 10)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		found := false
		for _, h := range hits {
			if h.Name == webSkill {
				found = true
			}
		}
		if !found {
			t.Errorf("Search(%q) did not find %q: got %v", q, webSkill, hitNames(hits))
		}
	}
}

// hitNames is what a failing search message shows, so the reader can see what came back instead.
func hitNames(hits []Skill) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Name)
	}
	return out
}

// TestAFreshInstallShipsTheWebProcedure: the document about reaching the network is part of
// what a fresh install starts with, and that is the whole reason it is embedded rather than
// left to the first session that needs it. A fresh install has no `~/.motita/skills/` at all,
// so the shipped set is the entire library — and a model that cannot search the web for a
// version, an API shape or an error message answers from its training data instead, which is
// exactly the failure this document exists to prevent.
func TestAFreshInstallShipsTheWebProcedure(t *testing.T) {
	l := builtinLib(t)
	// The directory does not exist, which is the state of a first run.
	got, err := l.Get(webSkill)
	if err != nil {
		t.Fatalf("a fresh install must ship %q: %v", webSkill, err)
	}
	if !strings.HasPrefix(got.Path, builtinPrefix) {
		t.Errorf("%q came from %q, want the binary", webSkill, got.Path)
	}
	all, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	names := hitNames(all)
	found := false
	for _, n := range names {
		if n == webSkill {
			found = true
		}
	}
	if !found {
		t.Errorf("the index does not offer %q: %v", webSkill, names)
	}
}

// TestAShippedProcedureIsReadableByTheAgent: the model reads a skill by NAME, and the name it
// has is the one the index printed. A read that fails for a shipped skill means the procedure
// exists and is unreachable.
func TestAShippedProcedureIsReadableByTheAgent(t *testing.T) {
	l := builtinLib(t)
	all, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("a library with builtins must list them")
	}
	for _, s := range all {
		got, err := l.Get(s.Name)
		if err != nil {
			t.Errorf("Get(%q) after listing it: %v", s.Name, err)
			continue
		}
		if !strings.Contains(got.Body, "#") {
			t.Errorf("the body of %q came back without its markdown", s.Name)
		}
		if got.Path == "" {
			t.Errorf("%q came back with no source, so the model cannot be told what it read", s.Name)
		}
	}
}

// TestTheBuiltinsAreOffByDefault: a library is the directory it was given. A test that asks what
// a directory holds must be answered with what is in it, and a caller that wants the shipped
// procedures says so.
func TestTheBuiltinsAreOffByDefault(t *testing.T) {
	l := newLib(t)
	all, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("a plain library must hold only its directory, got %d skills", len(all))
	}
	if _, err := l.Get("files-and-directories"); err == nil {
		t.Error("a plain library must not serve a shipped procedure")
	}
	hits, err := l.Search("files directories", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("a plain library must not match a shipped procedure, got %v", hits)
	}
}

// TestADocumentOnDiskShadowsTheShippedOne: this is the property that makes the arrangement
// usable. A user who corrects a shipped procedure writes their own next to it, and the
// correction has to WIN — a library that served the binary's version would make the user's file
// look broken, and the model would keep using the procedure that was just fixed.
func TestADocumentOnDiskShadowsTheShippedOne(t *testing.T) {
	l := builtinLib(t)
	shipped, err := builtinSkills()
	if err != nil {
		t.Fatal(err)
	}
	var name string
	for n := range shipped {
		name = n
		break
	}
	if name == "" {
		t.Skip("no shipped skills to shadow")
	}

	mine := "# My own version\n\nDo it my way.\n"
	if _, err := l.Save(name, mine); err != nil {
		t.Fatal(err)
	}

	got, err := l.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != mine {
		t.Errorf("the document on disk must win, got the shipped body:\n%s", got.Body)
	}

	// And it must not be listed TWICE under the one name: the model would be offered two
	// different procedures and no way to tell which is which.
	all, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, s := range all {
		if s.Name == name {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%q appears %d times in the index, want once", name, count)
	}
}

// TestRemovingTheShadowBringsTheShippedOneBack: the arrangement is reversible, which is what
// makes it safe to correct a shipped procedure and change your mind.
func TestRemovingTheShadowBringsTheShippedOneBack(t *testing.T) {
	l := builtinLib(t)
	shipped, err := builtinSkills()
	if err != nil {
		t.Fatal(err)
	}
	var name string
	for n := range shipped {
		name = n
		break
	}
	if name == "" {
		t.Skip("no shipped skills to shadow")
	}

	if _, err := l.Save(name, "# Mine\n\nshort\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(l.Dir, name+".md")); err != nil {
		t.Fatal(err)
	}

	got, err := l.Get(name)
	if err != nil {
		t.Fatalf("the shipped procedure must come back: %v", err)
	}
	if got.Body == "# Mine\n\nshort\n" {
		t.Error("the removed document is still being served")
	}
	if !strings.HasPrefix(got.Path, builtinPrefix) {
		t.Errorf("the restored procedure must come from the binary, got path %q", got.Path)
	}
}

// TestAShippedProcedureIsSearchableByItsBody: the search looks through the whole text, and that
// is the point — the words of a procedure are usually in its body, not its title.
func TestAShippedProcedureIsSearchableByItsBody(t *testing.T) {
	l := builtinLib(t)
	// A phrase that appears in the body of the shipped procedure and in no heading, so only a
	// body match can find it.
	hits, err := l.Search("guardrails confirm refused", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("a body-only query must still find the shipped procedure")
	}
}

// TestGetRefusesAnEmptyName: the lookup goes through the same name sanitiser as everything else,
// so an empty name is an error rather than a hit on something.
func TestGetRefusesAnEmptyName(t *testing.T) {
	if _, err := builtinLib(t).Get("   "); err == nil {
		t.Error("an empty name must be refused")
	}
}

// TestABrokenEmbeddedSetIsReported: the embedded set is part of the binary, so a read that fails
// means the build is broken. The point of the branch is that it says so instead of reporting an
// empty library, which would look like a fresh install.
func TestABrokenEmbeddedSetIsReported(t *testing.T) {
	boom := errors.New("corrupt binary")

	t.Run("listing", func(t *testing.T) {
		old := builtinReadDir
		builtinReadDir = func(string) ([]fs.DirEntry, error) { return nil, boom }
		defer func() { builtinReadDir = old }()

		if _, err := builtinSkills(); !errors.Is(err, boom) {
			t.Errorf("err = %v, want the read failure", err)
		}
		if _, err := builtinLib(t).List(); !errors.Is(err, boom) {
			t.Errorf("List = %v, want it reported rather than empty", err)
		}
	})

	t.Run("one document", func(t *testing.T) {
		old := builtinReadFile
		builtinReadFile = func(string) ([]byte, error) { return nil, boom }
		defer func() { builtinReadFile = old }()

		if _, err := builtinSkills(); !errors.Is(err, boom) {
			t.Errorf("err = %v, want the read failure", err)
		}
		if _, err := builtinLib(t).Get("files-and-directories"); !errors.Is(err, boom) {
			t.Errorf("Get = %v, want it reported rather than not-found", err)
		}
	})
}

// TestAnEntryThatIsNotAMarkdownFileIsIgnored: the folder decides what is served, so something
// else that ends up in it — a scratch file, a directory — must not become a skill or break the
// listing. The real folder holds only what was embedded, so the entries are supplied.
func TestAnEntryThatIsNotAMarkdownFileIsIgnored(t *testing.T) {
	// A directory and a non-markdown file, wrapped around the real entries for the folder.
	old := builtinReadDir
	defer func() { builtinReadDir = old }()
	builtinReadDir = func(dir string) ([]fs.DirEntry, error) {
		real, err := old(dir)
		if err != nil {
			return nil, err
		}
		return append(real, fakeEntry{name: "scratch"}, fakeEntry{name: "notes.txt"}, fakeEntry{name: "sub", dir: true}), nil
	}

	all, err := builtinSkills()
	if err != nil {
		t.Fatalf("a stray entry must not break the listing: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("the real entries must still be served")
	}
	for _, stray := range []string{"scratch", "notes", "sub"} {
		if _, ok := all[stray]; ok {
			t.Errorf("%q was served, but only .md files directly in the folder are skills", stray)
		}
	}
}

// fakeEntry is a DirEntry for the entries the embedded folder does not really hold.
type fakeEntry struct {
	name string
	dir  bool
}

func (e fakeEntry) Name() string               { return e.name }
func (e fakeEntry) IsDir() bool                { return e.dir }
func (e fakeEntry) Type() fs.FileMode          { return 0 }
func (e fakeEntry) Info() (fs.FileInfo, error) { return nil, errors.New("not consulted") }

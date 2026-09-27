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

// The shipped procedures, by name, so a test can assert on the DOCUMENT rather than on
// "something matched". Several of the queries below also match another document — "search the
// web" hits the FILE procedure, because "search" is in its text — so a test that only asked for
// a hit would go green on the wrong answer and a document could be deleted with nothing failing.
const (
	fileSkill     = "files-and-directories"
	webSkill      = "searching-the-web"
	commandsSkill = "running-commands"
	gitSkill      = "git-in-a-repository"
	curlSkill     = "calling-an-http-api"
)

// shippedCore is what a fresh install is expected to carry, in one list: the tests that ask "does
// a fresh install ship this" and "does the search find it" both read it, so adding a document
// means adding it here and nowhere else.
var shippedCore = []string{fileSkill, webSkill, commandsSkill, gitSkill, curlSkill}

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

// TestTheShippedProceduresAreFoundByTheWordsOfTheJob: the search is the only way the model reaches
// a skill it was not told about, so the words a person would actually use have to find it — and
// find the RIGHT document, not merely something. These are the phrases each procedure claims to
// cover, and they are the queries a real session starts from.
func TestTheShippedProceduresAreFoundByTheWordsOfTheJob(t *testing.T) {
	l := builtinLib(t)
	cases := []struct {
		skill string
		words []string
	}{
		{fileSkill, []string{
			"files and directories",
			"list a directory",
			"read a file",
			"find text in files",
			"copy a folder",
			"delete a directory",
			"create a file",
		}},
		{webSkill, []string{
			"search the web",
			"look up the documentation for a library",
			"fetch a web page",
			"research something online",
			"check a URL",
		}},
		{commandsSkill, []string{
			"what will happen if I run this command",
			"the output was truncated",
			"which commands are refused",
			"the command needs approval",
			"how do I read a value out of json",
		}},
		{gitSkill, []string{
			"undo my last commit",
			"which branch am I on",
			"make a commit",
			"restore a deleted file from git",
			"resolve a merge conflict",
		}},
		{curlSkill, []string{
			"call an api and get json",
			"why does curl return nothing",
			"post json to a service",
			"check an http status code",
		}},
	}
	for _, c := range cases {
		for _, q := range c.words {
			hits, err := l.Search(q, 10)
			if err != nil {
				t.Fatalf("Search(%q): %v", q, err)
			}
			found := false
			for _, h := range hits {
				if h.Name == c.skill {
					found = true
				}
			}
			if !found {
				t.Errorf("Search(%q) did not find %q: got %v", q, c.skill, hitNames(hits))
			}
		}
	}
}

// TestAFreshInstallShipsTheCoreProcedures: one fresh install, one document per kind of work an
// agent cannot do without a procedure for. This is what a new user starts with, and nothing tells
// them what a library is supposed to hold — so a document that fell out of the embed because its
// file was renamed would leave a gap they cannot see. The directory does not exist here, which is
// the state of a first run.
func TestAFreshInstallShipsTheCoreProcedures(t *testing.T) {
	l := builtinLib(t)
	for _, name := range shippedCore {
		got, err := l.Get(name)
		if err != nil {
			t.Errorf("a fresh install must ship %q: %v", name, err)
			continue
		}
		if !strings.HasPrefix(got.Path, builtinPrefix) {
			t.Errorf("%q came from %q, want the binary", name, got.Path)
		}
	}
	all, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	offered := hitNames(all)
	for _, name := range shippedCore {
		found := false
		for _, n := range offered {
			if n == name {
				found = true
			}
		}
		if !found {
			t.Errorf("the index does not offer %q: %v", name, offered)
		}
	}
}

// TestEveryShippedProcedureIsDistinct: two documents that answer the same question are worse than
// one — the model is then choosing between two accounts of the same work and the search cannot
// tell it which to trust. The title is the first thing a reader sees, so a shared one is the tell.
//
// The names in shippedCore are checked against the embedded set too: a name no file matches would
// make the tests above pass while asserting nothing, which is the quietest way for a test suite
// to stop protecting anything.
func TestEveryShippedProcedureIsDistinct(t *testing.T) {
	all, err := builtinSkills()
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]string{}
	for name, s := range all {
		if prev, dup := byTitle[s.Title]; dup {
			t.Errorf("%q and %q share the title %q", prev, name, s.Title)
		}
		byTitle[s.Title] = name
	}
	for _, want := range shippedCore {
		if _, ok := all[want]; !ok {
			t.Errorf("shippedCore names %q but the binary does not hold it", want)
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
	if _, err := l.Get(fileSkill); err == nil {
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
		if _, err := builtinLib(t).Get(fileSkill); !errors.Is(err, boom) {
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

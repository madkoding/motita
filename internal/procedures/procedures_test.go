package procedures

// Building the library and its ledger is what every front end does, and the reason this package
// exists is that doing it in more than one place is how a path ended up promising the model a
// library and then answering that none was configured. So the properties worth testing are the
// ones a caller relies on: that the library is there, that it can reach the shipped procedures,
// that the shipped ones are reachable and answerable, and that an unreadable ledger costs the
// scores but NOT the procedures.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// quiet is a logger that writes nothing, so a warning about a broken ledger does not fill the
// test output with JSON.
func quiet(t *testing.T) *logx.Logger {
	t.Helper()
	l, err := logx.New(logx.Options{Level: logx.Error, Console: false})
	if err != nil {
		t.Fatalf("could not build the logger: %v", err)
	}
	return l
}

// cfgAt is a configuration whose skills live in a directory the test owns.
func cfgAt(dir string) config.Config {
	cfg := config.Default()
	cfg.Skills.Dir = dir
	return cfg
}

// TestTheStoreAlwaysHasALibrary: a caller gets a library from this, or the tools the prompt
// advertises cannot be answered. There is no configuration that produces a nil library, because
// "no library" is exactly the state that broke two of the three paths.
func TestTheStoreAlwaysHasALibrary(t *testing.T) {
	for _, dir := range []string{t.TempDir(), "", filepath.Join(t.TempDir(), "not", "made", "yet")} {
		st := Open(cfgAt(dir), quiet(t))
		if st.Library == nil {
			t.Fatalf("Open(%q) returned no library", dir)
		}
		if !st.Library.Builtins {
			t.Errorf("Open(%q) must serve the shipped procedures", dir)
		}
		if _, err := st.Library.List(); err != nil {
			t.Errorf("Open(%q) produced a library that cannot be listed: %v", dir, err)
		}
	}
}

// TestAnUnsetDirectoryFallsBackToTheHome: motita's own state does not belong in whatever
// project the user happens to be standing in, and an empty string would root the library at the
// working directory.
func TestAnUnsetDirectoryFallsBackToTheHome(t *testing.T) {
	st := Open(cfgAt(""), quiet(t))
	want := config.Default().Skills.Dir
	if st.Library.Dir != want {
		t.Errorf("dir = %q, want %q", st.Library.Dir, want)
	}
	if !filepath.IsAbs(want) {
		t.Errorf("the fallback must be absolute, got %q", want)
	}
}

// TestTheConfiguredCapIsHonoured: a stray large file must not be pulled into the context as if
// it were a procedure, and the cap is the configuration's, not the default's.
func TestTheConfiguredCapIsHonoured(t *testing.T) {
	cfg := cfgAt(t.TempDir())
	cfg.Skills.MaxFileBytes = 128
	st := Open(cfg, quiet(t))
	if st.Library.MaxFileBytes != 128 {
		t.Errorf("cap = %d, want the configured 128", st.Library.MaxFileBytes)
	}
	// A cap of zero means "no override", and the library's own default must survive it: the
	// alternative is a library that refuses every document.
	cfg.Skills.MaxFileBytes = 0
	if got := Open(cfg, quiet(t)).Library.MaxFileBytes; got == 0 {
		t.Error("a zero cap must not turn into no limit at all")
	}
}

// TestTheShippedProceduresAreReachableThroughTheStore: the whole point of the shipped documents
// is that a fresh install can answer a question about its own tools. This is the property the
// first-run path depends on.
func TestTheShippedProceduresAreReachableThroughTheStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills") // deliberately NOT created: a fresh install
	st := Open(cfgAt(dir), quiet(t))

	all, err := st.Library.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("a fresh install must still find the shipped procedures")
	}
	hits, err := st.Library.Search("delete a folder", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("the shipped procedure must be findable by the words of the job")
	}
	s, err := st.Library.Get(hits[0].Name)
	if err != nil {
		t.Fatalf("the model must be able to read what the search returned: %v", err)
	}
	if !strings.Contains(s.Body, "#") {
		t.Errorf("the procedure came back without its markdown:\n%s", s.Body)
	}
}

// TestTheLedgerLivesWithTheLibrary: one directory to copy or back up carries both the
// procedures and their telemetry.
func TestTheLedgerLivesWithTheLibrary(t *testing.T) {
	dir := t.TempDir()
	st := Open(cfgAt(dir), quiet(t))
	if st.Usage == nil {
		t.Fatal("a fresh directory must still give a working ledger")
	}
	st.Usage.SetPinned("build", true)
	if err := st.Usage.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".usage.json")); err != nil {
		t.Errorf("the ledger must be kept with the library: %v", err)
	}
}

// TestAnOldScoresFileIsIgnored: the verdict ledger (.scores.json) was retired. A file an earlier
// version left behind is neither read nor needed: the library and its telemetry open as usual.
func TestAnOldScoresFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".scores.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := Open(cfgAt(dir), quiet(t))
	if st.Library == nil || st.Usage == nil {
		t.Fatal("an old scores file must not cost the library or its telemetry")
	}
}

// TestABrokenUsageLedgerIsReportedAndSurvived: the usage sidecar disables the curator and the
// background review fork and nothing else. A corrupt .usage.json must cost that telemetry and
// leave the library exactly as it was.
func TestABrokenUsageLedgerIsReportedAndSurvived(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".usage.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(t.TempDir(), "motita.log")
	l, err := logx.New(logx.Options{Level: logx.Warn, Path: logPath})
	if err != nil {
		t.Fatal(err)
	}

	st := Open(cfgAt(dir), l)
	if st.Library == nil {
		t.Fatal("an unreadable usage ledger must not cost the procedures")
	}
	if st.Usage != nil {
		t.Error("a usage ledger that cannot be read must be absent rather than half-built")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("the warning must have been written: %v", err)
	}
	if !strings.Contains(string(data), "usage ledger") {
		t.Errorf("the loss must be reported as the usage ledger, got %q", string(data))
	}
}

// TestABrokenUsageLedgerIsSurvivedWithoutALogger: the same failure on a path with no logger
// configured. The warning has nowhere to go, and that must not turn into a panic before the
// library is returned - the degraded state (no curator, no review) is still usable.
func TestABrokenUsageLedgerIsSurvivedWithoutALogger(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".usage.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := Open(cfgAt(dir), nil)
	if st.Library == nil {
		t.Fatal("a nil logger must not stop the library from being built")
	}
	if st.Usage != nil {
		t.Error("the broken usage ledger must still be treated as absent")
	}
}

// TestTheLedgerAndTheLibraryAreConnected: a turned-off skill has to disappear from the index the
// model is given. They are two sidecar files and one directory, and the only place that can tie
// them together is this constructor: if the seam is not installed here, the flag is written to
// the JSON and nothing changes, which is the worst kind of working.
//
// The count is not the thing to assert: the store turns the shipped procedures on, so the index
// holds them too. What changes when a document is turned off is whether THAT NAME is offered.
//
// The search below uses a word that is in the body of THIS document and in nothing the binary
// ships: a shared word ("procedure", "the") matches a shipped document and the assertion then
// fails for the wrong reason — the seam working perfectly while the message says it did not.
func TestTheLedgerAndTheLibraryAreConnected(t *testing.T) {
	st := Open(cfgAt(t.TempDir()), quiet(t))
	if st.Usage == nil {
		t.Fatal("no usage ledger")
	}
	if _, err := st.Library.Save("mine", "# Mine\n\nThe procedure, about a zebra.\n"); err != nil {
		t.Fatal(err)
	}

	if !offered(t, st, "mine") {
		t.Fatal("the document must be offered before it is turned off")
	}
	st.Usage.SetDisabled("mine", true)
	if offered(t, st, "mine") {
		t.Error("disabling did not reach the library's index")
	}
	// And the search, which is the other way a skill arrives unasked.
	hits, err := st.Library.Search("zebra", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, h := range hits {
		if h.Name == "mine" {
			t.Errorf("search = %+v: disabling did not reach the search", hits)
		}
	}
	st.Usage.SetDisabled("mine", false)
	if !offered(t, st, "mine") {
		t.Error("enabling did not reach the library's index")
	}
}

// offered reports whether a name is currently in the index the model is given.
func offered(t *testing.T, st *Store, name string) bool {
	t.Helper()
	all, err := st.Library.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, s := range all {
		if s.Name == name {
			return true
		}
	}
	return false
}

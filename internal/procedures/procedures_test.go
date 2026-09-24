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
	"github.com/madkoding/motita/internal/reward"
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
// procedures and what has been learned about them.
func TestTheLedgerLivesWithTheLibrary(t *testing.T) {
	dir := t.TempDir()
	st := Open(cfgAt(dir), quiet(t))
	if st.Ledger == nil {
		t.Fatal("a fresh directory must still give a working ledger")
	}
	if got := filepath.Dir(st.Ledger.Path); got != dir {
		t.Errorf("ledger dir = %q, want it with the library at %q", got, dir)
	}
}

// TestTheLibraryScoresWithTheLedger: the search breaks ties by what has worked, so a library
// that has a ledger must be using it. A store that opened both and connected neither would look
// identical from the outside until someone read the scores.
func TestTheLibraryScoresWithTheLedger(t *testing.T) {
	st := Open(cfgAt(t.TempDir()), quiet(t))
	if st.Ledger == nil {
		t.Fatal("a fresh directory must still give a working ledger")
	}
	if st.Library.Scorer == nil {
		t.Error("the library must read the ledger, or the tie-break never happens")
	}
	// The scorer must be THIS ledger and not another one: two ledgers over one file would let a
	// verdict recorded on one be invisible to the search using the other.
	if got, ok := st.Library.Scorer.(*reward.Ledger); !ok || got != st.Ledger {
		t.Errorf("scorer = %#v, want this store's ledger", st.Library.Scorer)
	}
}

// TestABrokenLedgerCostsTheScoresNotTheProcedures is the deliberate trade-off.
//
// The scores are the only record of what the user thought of the library, so losing them is
// reported rather than hidden. But refusing to build the library over it would cost the
// PROCEDURES too, and a library without its scores still answers every question the model asks.
// So the ledger is nil and the library works.
func TestABrokenLedgerCostsTheScoresNotTheProcedures(t *testing.T) {
	dir := t.TempDir()
	// A ledger that exists and is not JSON: what a truncated write or a bad merge leaves.
	if err := os.WriteFile(filepath.Join(dir, ".scores.json"), []byte("this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := Open(cfgAt(dir), quiet(t))
	if st.Ledger != nil {
		t.Error("a ledger that cannot be read must be absent, not silently replaced")
	}
	if st.Library == nil {
		t.Fatal("the procedures must survive a broken ledger")
	}
	if st.Library.Scorer != nil {
		t.Error("with no ledger there is no tie-break to apply, and pretending otherwise would panic")
	}
	all, err := st.Library.List()
	if err != nil {
		t.Fatalf("the library must still work: %v", err)
	}
	if len(all) == 0 {
		t.Error("the shipped procedures must still be served")
	}
}

// TestABrokenLedgerIsReported: the loss is real information the user needs, so it goes to the
// log rather than being swallowed. A store that opened quietly would make a lost history look
// like a fresh one.
func TestABrokenLedgerIsReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".scores.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The real logger, writing to a file, so the warning is observed rather than assumed.
	logPath := filepath.Join(t.TempDir(), "motita.log")
	l, err := logx.New(logx.Options{Level: logx.Warn, Path: logPath})
	if err != nil {
		t.Fatal(err)
	}

	st := Open(cfgAt(dir), l)
	if st.Ledger != nil {
		t.Fatal("the fixture expects an unreadable ledger")
	}
	if st.Library == nil {
		t.Fatal("the procedures must survive it")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("the warning must have been written: %v", err)
	}
	if !strings.Contains(string(data), "ledger") {
		t.Errorf("the loss must be reported, got %q", string(data))
	}
}

// TestANilLoggerIsTolerated: Open is called from paths that may not have a logger, and a
// warning with nowhere to go must not become a panic on the way to a working library.
func TestANilLoggerIsTolerated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".scores.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := Open(cfgAt(dir), nil)
	if st.Library == nil {
		t.Fatal("a nil logger must not stop the library from being built")
	}
	if st.Ledger != nil {
		t.Error("the broken ledger must still be treated as absent")
	}
}

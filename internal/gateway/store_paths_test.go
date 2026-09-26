package gateway

// The error paths, orderings and skips that the CRUD tests in coverage_test.go never enter.
//
// A store is the one place that touches the filesystem in this package, and every branch here is a
// disk that failed: a directory that cannot be created, a write that cannot land, a rename that
// cannot replace, a listing that cannot be read. None of them happen on a working machine, which is
// exactly why they were never covered - and exactly why they matter, because the day one of them
// happens the answer has to be an error the caller can act on rather than a silent drop.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
)

// blockStoreDirectory puts a FILE where the store's directory should be. Every operation on the
// store then fails with ENOTDIR: creation because the path is occupied, writes because the parent
// is not a directory, listing because it cannot be read. It is how a mount that went away looks,
// and it is preferable to a mode-based fixture because root ignores modes.
func blockStoreDirectory(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove the store directory: %v", err)
	}
	if err := os.WriteFile(dir, []byte("in the way"), 0o600); err != nil {
		t.Fatalf("block the store directory: %v", err)
	}
}

// A store whose directory cannot be created is not returned: a store that reported itself as
// working while every save was dropped is a store that loses work silently.
func TestAStoreWhoseDirectoryCannotBeCreatedIsRefused(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "blocked")

	t.Run("sessions", func(t *testing.T) {
		blockStoreDirectory(t, blocked)
		if _, err := newSessionStore(blocked); err == nil {
			t.Error("a session store with an unusable directory must be refused")
		}
	})
	t.Run("projects", func(t *testing.T) {
		blockStoreDirectory(t, blocked)
		if _, err := newProjectStore(blocked); err == nil {
			t.Error("a project store with an unusable directory must be refused")
		}
	})
}

// A session whose record cannot be written reports it, and reports WHICH session: an error that
// only says "could not write" leaves the operator no way to find the file.
func TestASessionThatCannotBeWrittenIsReported(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	st, err := newSessionStore(dir)
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	blockStoreDirectory(t, dir)

	conv := newConversation("s1", &fakeService{})
	err = st.save(conv)
	if err == nil {
		t.Fatal("a save that cannot land must be reported")
	}
	if !strings.Contains(err.Error(), "s1") {
		t.Errorf("err = %q, want it to name the session", err)
	}
}

// And the same for a project.
func TestAProjectThatCannotBeWrittenIsReported(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects")
	ps, err := newProjectStore(dir)
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	blockStoreDirectory(t, dir)

	err = ps.save(Project{ID: "p1", Title: "t", Dir: "/tmp/t", Created: time.Now()})
	if err == nil {
		t.Fatal("a save that cannot land must be reported")
	}
	if !strings.Contains(err.Error(), "p1") {
		t.Errorf("err = %q, want it to name the project", err)
	}
}

// A record that cannot be ENCODED is reported rather than written as something else. A time whose
// year is outside [0,9999] makes time.Time's marshaller fail, and that is the only input in these
// structs that json.MarshalIndent refuses - so it is the one way to reach this branch honestly.
func TestARecordThatCannotBeEncodedIsReported(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		st, err := newSessionStore(filepath.Join(t.TempDir(), "sessions"))
		if err != nil {
			t.Fatalf("newSessionStore: %v", err)
		}
		conv := newConversation("s1", &fakeService{})
		conv.created = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)

		err = st.save(conv)
		if err == nil {
			t.Fatal("a record that cannot be encoded must be reported")
		}
		if !strings.Contains(err.Error(), "encode") {
			t.Errorf("err = %q, want it to say the record could not be encoded", err)
		}
	})
	t.Run("project", func(t *testing.T) {
		ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
		if err != nil {
			t.Fatalf("newProjectStore: %v", err)
		}
		err = ps.save(Project{ID: "p1", Created: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err == nil {
			t.Fatal("a record that cannot be encoded must be reported")
		}
		if !strings.Contains(err.Error(), "encode") {
			t.Errorf("err = %q, want it to say the record could not be encoded", err)
		}
	})
}

// A directory that cannot be listed is an error and not an empty answer: "there is nothing here"
// and "I could not look" are different, and a caller that treats the second as the first deletes
// what it cannot see.
func TestADirectoryThatCannotBeListedIsReported(t *testing.T) {
	t.Run("sessions", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "sessions")
		st, err := newSessionStore(dir)
		if err != nil {
			t.Fatalf("newSessionStore: %v", err)
		}
		blockStoreDirectory(t, dir)
		if _, err := st.loadAll(); err == nil {
			t.Error("an unlistable session directory must be reported")
		}
	})
	t.Run("projects", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "projects")
		ps, err := newProjectStore(dir)
		if err != nil {
			t.Fatalf("newProjectStore: %v", err)
		}
		blockStoreDirectory(t, dir)
		if _, err := ps.loadAll(); err == nil {
			t.Error("an unlistable project directory must be reported")
		}
	})
}

// A project whose file exists but cannot be READ is reported, not treated as missing: one is a
// project that was never created, the other is a project whose record is on a disk that failed.
func TestAProjectThatCannotBeReadIsNotTreatedAsMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file, so this fixture cannot make the record unreadable")
	}
	dir := filepath.Join(t.TempDir(), "projects")
	ps, err := newProjectStore(dir)
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if err := ps.save(Project{ID: "p1", Title: "t", Dir: "/tmp/t", Created: time.Now()}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.Chmod(ps.path("p1"), 0o000); err != nil {
		t.Fatalf("make the record unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(ps.path("p1"), 0o600) })

	got, err := ps.load("p1")
	if err == nil {
		t.Fatalf("an unreadable record must be reported, got (%v, nil)", got)
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("err = %q, want it to say the record could not be read", err)
	}
}

// The listing SKIPS what it cannot use rather than failing: one unreadable or corrupt file must not
// hide every other project. Both halves are asserted, because "returns nothing" also passes a test
// that only checks the good record is absent.
func TestAProjectListingSkipsWhatItCannotUseAndKeepsTheRest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file, so the unreadable half cannot be set up")
	}
	dir := filepath.Join(t.TempDir(), "projects")
	ps, err := newProjectStore(dir)
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	good := Project{ID: "good", Title: "kept", Dir: "/tmp/g", Created: time.Now()}
	if err := ps.save(good); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Corrupt, unreadable, a directory and a file with the wrong extension: four reasons a listing
	// has to carry on.
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{ not a project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unreadable.json"), []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "unreadable.json"), 0o600) })
	if err := os.Mkdir(filepath.Join(dir, "a-directory.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a record"), 0o600); err != nil {
		t.Fatal(err)
	}

	all, err := ps.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("loadAll = %d records, want only the usable one: %+v", len(all), all)
	}
	if all[0].ID != "good" {
		t.Errorf("the surviving record is %q, want good", all[0].ID)
	}
}

// The same for sessions, plus the .tmp file a crashed save leaves behind: it is not a record and
// must not appear in a listing.
func TestASessionListingSkipsWhatItCannotUseAndKeepsTheRest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file, so the unreadable half cannot be set up")
	}
	dir := filepath.Join(t.TempDir(), "sessions")
	st, err := newSessionStore(dir)
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	if err := st.save(newConversation("good", &fakeService{})); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{ not a session"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unreadable.json"), []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "unreadable.json"), 0o600) })
	if err := os.WriteFile(filepath.Join(dir, "good.json.tmp"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "good" {
		t.Fatalf("loadAll = %+v, want only the good record", all)
	}
}

// Sessions are listed most-recently-used FIRST, and the order comes from the record's own timestamp
// rather than from the directory listing - which has none. Two reads of an unchanged store must
// answer in the same order, or a front end redraws the list on every poll.
func TestSessionsAreListedMostRecentlyUsedFirst(t *testing.T) {
	st, err := newSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	base := time.Now().Add(-time.Hour)
	// Saved in a scrambled order, and the NAMES match the times: a failure then names the end of
	// the ordering that is wrong instead of requiring the reader to work it out.
	for _, c := range []struct {
		id      string
		minutes int
	}{
		{"middle", 10},
		{"newest", 20},
		{"oldest", 0},
	} {
		conv := newConversation(c.id, &fakeService{})
		conv.created = base.Add(time.Duration(c.minutes) * time.Minute)
		conv.lastUsed = base.Add(time.Duration(c.minutes) * time.Minute)
		if err := st.save(conv); err != nil {
			t.Fatalf("save %s: %v", c.id, err)
		}
	}

	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	want := []string{"newest", "middle", "oldest"}
	if len(all) != len(want) {
		t.Fatalf("loadAll = %d records, want %d", len(all), len(want))
	}
	for i, id := range want {
		if all[i].ID != id {
			t.Errorf("position %d is %q, want %q", i, all[i].ID, id)
		}
	}
}

// Projects are listed OLDEST first, which is the opposite of sessions: a project list reads as a
// history of what was added, while a session list reads as what you were just doing. The names
// match the times here so a failure says which end of the order is wrong.
func TestProjectsAreListedOldestFirst(t *testing.T) {
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	base := time.Now().Add(-time.Hour)
	// Written in a scrambled order on purpose: the order must come from Created, not from the
	// order the files happened to be written or listed in.
	for _, p := range []struct {
		id      string
		minutes int
	}{
		{"newest", 20},
		{"oldest", 0},
		{"middle", 10},
	} {
		rec := Project{ID: p.id, Title: p.id, Dir: "/tmp/" + p.id, Created: base.Add(time.Duration(p.minutes) * time.Minute)}
		if err := ps.save(rec); err != nil {
			t.Fatalf("save %s: %v", p.id, err)
		}
	}

	all, err := ps.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	want := []string{"oldest", "middle", "newest"}
	if len(all) != len(want) {
		t.Fatalf("loadAll = %d records, want %d", len(all), len(want))
	}
	for i, id := range want {
		if all[i].ID != id {
			t.Errorf("position %d is %q, want %q", i, all[i].ID, id)
		}
	}
}

// A session is written through a temporary file and renamed into place, so a process killed
// mid-save must not leave a truncated record: either the old file is intact or the new one is
// complete. Asserting the .tmp file is GONE afterwards is what proves the write is atomic rather
// than in-place - a plain WriteFile passes every read-back assertion and is not atomic at all.
func TestASessionIsWrittenThroughARename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	st, err := newSessionStore(dir)
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	conv := newConversation("s1", &fakeService{})
	conv.setTitle("the first title")
	if err := st.save(conv); err != nil {
		t.Fatalf("save: %v", err)
	}

	conv.setTitle("the second title")
	if err := st.save(conv); err != nil {
		t.Fatalf("save again: %v", err)
	}

	if _, err := os.Stat(st.path("s1") + ".tmp"); !os.IsNotExist(err) {
		t.Error("the temporary file must be renamed away, not left behind")
	}
	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("loadAll = %d records, want 1", len(all))
	}
	if all[0].Title != "the second title" {
		t.Errorf("Title = %q, want the second save to have replaced the first", all[0].Title)
	}
}

// A session record carries the fields the listing and the restore path need, and there is no
// per-session load: the store is always read as a SET, because that is how the gateway restores
// its conversations. What this pins is that a save round-trips every field the restore reads -
// ProjectDir in particular, without which a restarted gateway reports every session as having
// nothing to integrate.
func TestASessionRecordRoundTripsEveryRestoreField(t *testing.T) {
	st, err := newSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	conv := newConversation("s1", &fakeService{})
	conv.setTitle("a title")
	conv.projectID = "p1"
	conv.projectDir = "/projects/p1"
	conv.lastTask = "the last thing asked"
	conv.lastKind = "task"
	conv.running = true
	if err := st.save(conv); err != nil {
		t.Fatalf("save: %v", err)
	}

	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("loadAll = %d records, want 1", len(all))
	}
	rec := all[0]
	if rec.ID != "s1" || rec.Title != "a title" {
		t.Errorf("ID/Title = %q/%q", rec.ID, rec.Title)
	}
	if rec.ProjectID != "p1" || rec.ProjectDir != "/projects/p1" {
		t.Errorf("ProjectID/ProjectDir = %q/%q, want both persisted", rec.ProjectID, rec.ProjectDir)
	}
	if rec.LastTask != "the last thing asked" || rec.LastKind != "task" {
		t.Errorf("LastTask/LastKind = %q/%q, want the interrupted work recorded", rec.LastTask, rec.LastKind)
	}
	if !rec.Running {
		t.Error("Running must persist: it is what marks a session to resume after a restart")
	}
}

// cloneGitRepo reports the parent directory it could not create rather than running git in a
// directory that is not there.
func TestCloneGitRepoRefusesAnUncreatableParent(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "blocked")
	blockStoreDirectory(t, blocked)

	out, err := cloneGitRepo("https://example.invalid/repo.git", filepath.Join(blocked, "child", "repo"))
	if err == nil {
		t.Fatal("a parent directory that cannot be created must be reported")
	}
	if !strings.Contains(err.Error(), "parent directory") {
		t.Errorf("err = %q, want it to name the parent directory", err)
	}
	if out != "" {
		t.Errorf("out = %q, want empty: git must not have run", out)
	}
}

// The transcript and the provider configuration are read from the service while saving, so a session
// saved after a run carries the turns a restarted gateway needs to resume it.
func TestASavedSessionCarriesItsTranscriptAndConfig(t *testing.T) {
	st, err := newSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	svc := &fakeService{
		transcript: []agent.DialogueTurn{{User: "do the thing", Agent: "done"}},
	}
	cfg := config.Default()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.Model = "claude-x"
	cfg.Agent.WorkspaceDir = "/work"
	svc.cfg = cfg
	conv := newConversation("s1", svc)
	if err := st.save(conv); err != nil {
		t.Fatalf("save: %v", err)
	}

	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("loadAll = %d records, want 1", len(all))
	}
	rec := all[0]
	if len(rec.Turns) != 1 || rec.Turns[0].User != "do the thing" {
		t.Errorf("Turns = %+v, want the service's transcript", rec.Turns)
	}
	if rec.Provider != "anthropic" || rec.Model != "claude-x" {
		t.Errorf("Provider/Model = %q/%q, want the service's configuration", rec.Provider, rec.Model)
	}
	if rec.Workspace != "/work" {
		t.Errorf("Workspace = %q, want the configured workspace", rec.Workspace)
	}
}

// A conversation whose service is gone still saves: the record is the session's identity, and a
// gateway shutting down must be able to write it even after its engine was released.
func TestASessionWithNoServiceStillSaves(t *testing.T) {
	st, err := newSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	conv := newConversation("s1", nil)
	if err := st.save(conv); err != nil {
		t.Fatalf("a session with no service must still be writable: %v", err)
	}
	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("loadAll = %d records, want 1", len(all))
	}
	if all[0].ID != "s1" || len(all[0].Turns) != 0 {
		t.Errorf("record = %+v, want the id and no turns", all[0])
	}
}

// A directory with a .json suffix is skipped by the listing: it is a name, not a record, and
// ReadFile on it fails in a way that must not abort the whole listing.
func TestADirectoryNamedLikeARecordIsSkipped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	st, err := newSessionStore(dir)
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	if err := st.save(newConversation("good", &fakeService{})); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "a-directory.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "good" {
		t.Fatalf("loadAll = %+v, want only the good record", all)
	}
}

// The record is JSON an operator may open and read, so the field names are the contract: renaming
// one silently would make a gateway read somebody's sessions as empty.
func TestTheSessionRecordUsesTheNamesAnOperatorReads(t *testing.T) {
	raw, err := json.Marshal(sessionRecord{ID: "s1", Title: "t", ProjectID: "p1", ProjectDir: "/p", Running: true, LastTask: "x", LastKind: "task"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{`"id"`, `"title"`, `"project_id"`, `"project_dir"`, `"running"`, `"last_task"`, `"last_kind"`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("the record is missing %s: %s", field, raw)
		}
	}
}

// ErrProjectNotFound is what the HTTP layer turns into a 404 while every other failure becomes a
// 500, so it has to be a SENTINEL the caller can test with errors.Is rather than a message. This
// pins that property rather than the text, which is what the two handlers rely on.
func TestTheProjectNotFoundSentinelIsTestableWithErrorsIs(t *testing.T) {
	err := fmt.Errorf("creating a session: %w", ErrProjectNotFound)
	if !errors.Is(err, ErrProjectNotFound) {
		t.Error("ErrProjectNotFound must survive wrapping: the 404 depends on it")
	}
	if ErrProjectNotFound.Error() == "" {
		t.Error("the sentinel needs a message: it is what the client is shown")
	}
}

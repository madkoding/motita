package gateway

// SESSIONS MUST NOT BE LOST.
//
// Reported from real use: sessions that were working disappeared after restarts, and one
// finished session was relaunched 25 times (44 "resuming interrupted session" lines in the
// gateway log). Two defects produced that, and each test below pins one.
//
//  1. The run goroutine saved the session BEFORE its deferred releaseRunSlot ran, so every
//     run that ended - finished, failed or cancelled - was written with running=true. The
//     next start read that as "interrupted" and ran the task again.
//  2. A run that was interrupted left nothing to resume FROM: the record held the request
//     and no trace of the work done (see the agent's pending turn).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readRecordOnDisk(t *testing.T, dir, id string) sessionRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatalf("the session was not persisted: %v", err)
	}
	var rec sessionRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestAFinishedRunIsNotPersistedAsRunning: defect 1. The record written when a run ends says
// the session is idle, whatever way the run ended.
func TestAFinishedRunIsNotPersistedAsRunning(t *testing.T) {
	for name, task := range map[string]func(context.Context, string, func(string, ...any)) (string, error){
		"finished": func(context.Context, string, func(string, ...any)) (string, error) { return "all done", nil },
		"failed": func(context.Context, string, func(string, ...any)) (string, error) {
			return "", context.DeadlineExceeded
		},
		"cancelled": func(context.Context, string, func(string, ...any)) (string, error) {
			return "", context.Canceled
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "sessions")
			srv, _ := startServer(t, Options{
				Token: testToken, WorkspaceDir: t.TempDir(), SessionDir: dir,
				ProjectDir: filepath.Join(t.TempDir(), "projects"),
				Service:    &fakeService{task: task},
			})
			c, ok := srv.lookup(DefaultSession)
			if !ok {
				t.Fatal("no default session")
			}
			rn, started := srv.startDetachedRun(c, "do the work", "task", srv.approverFactory(c))
			if !started {
				t.Fatal("the run did not start")
			}
			<-rn.done
			waitForNoRun(t, srv)

			rec := readRecordOnDisk(t, dir, DefaultSession)
			if rec.Running {
				t.Fatal("a run that ended was saved as running: the next start would run the task again")
			}
			if rec.LastTask != "do the work" {
				t.Errorf("last_task = %q: what was asked must survive", rec.LastTask)
			}
		})
	}
}

// TestAShutdownMidRunIsStillResumed: the guard the other way. A gateway stopped WHILE a run
// is in flight must still record it as running, or nothing would ever be resumed.
func TestAShutdownMidRunIsStillResumed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	srv, _ := startServer(t, Options{
		Token: testToken, WorkspaceDir: t.TempDir(), SessionDir: dir,
		ProjectDir: filepath.Join(t.TempDir(), "projects"),
		Service: &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
			entered <- struct{}{}
			<-release
			return "late", nil
		}},
	})
	t.Cleanup(func() { close(release); waitForNoRun(t, srv) })
	c, _ := srv.lookup(DefaultSession)
	if _, ok := srv.startDetachedRun(c, "long job", "task", srv.approverFactory(c)); !ok {
		t.Fatal("the run did not start")
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the run never started")
	}
	srv.saveAllSessions()
	if rec := readRecordOnDisk(t, dir, DefaultSession); !rec.Running || rec.LastTask != "long job" {
		t.Fatalf("a session mid-run must be persisted as running with its task: %+v", rec)
	}
}

// --- The store itself: a bad write or a delete must not destroy a conversation ----------------

func seedConversation(t *testing.T, st *sessionStore, id, title string) {
	t.Helper()
	conv := newConversation(id, &fakeService{})
	conv.setTitle(title)
	if err := st.save(conv); err != nil {
		t.Fatal(err)
	}
}

// TestACorruptSessionFileIsRecoveredFromThePreviousSave: every save keeps the version it
// replaces, so a file that is cut half way costs one save, not the conversation.
func TestACorruptSessionFileIsRecoveredFromThePreviousSave(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	st, _ := newSessionStore(dir)
	seedConversation(t, st, "s1", "first")
	seedConversation(t, st, "s1", "second") // the first version becomes s1.json.prev
	if err := os.WriteFile(st.path("s1"), []byte(`{"id":"s1","tit`), 0o600); err != nil {
		t.Fatal(err)
	}
	all, err := st.loadAll()
	if err != nil || len(all) != 1 || all[0].Title != "first" {
		t.Fatalf("the previous save must take its place: %+v err=%v", all, err)
	}
	matches, _ := filepath.Glob(st.path("s1") + ".corrupt-*")
	if len(matches) != 1 {
		t.Errorf("the corrupt file must be set aside, not deleted: %v", matches)
	}
}

// TestACorruptSessionWithNoBackupIsSetAsideNotOverwritten: with nothing to recover from, the
// bytes are still kept where a person can read them.
func TestACorruptSessionWithNoBackupIsSetAsideNotOverwritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	st, _ := newSessionStore(dir)
	if err := os.WriteFile(st.path("s2"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	all, _ := st.loadAll()
	if len(all) != 0 {
		t.Fatalf("nothing can be loaded: %+v", all)
	}
	matches, _ := filepath.Glob(st.path("s2") + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("the unreadable file must be kept: %v", matches)
	}
	if b, _ := os.ReadFile(matches[0]); string(b) != "not json" {
		t.Errorf("kept bytes = %q", b)
	}
}

// TestDeletingASessionKeepsItsFileInTheTrash: forgetting a session does not destroy the
// only copy of a conversation, and it does not bring it back on the next start either.
func TestDeletingASessionKeepsItsFileInTheTrash(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	st, _ := newSessionStore(dir)
	seedConversation(t, st, "s3", "keep me")
	seedConversation(t, st, "s3", "keep me too") // leaves a .prev
	if err := st.delete("s3"); err != nil {
		t.Fatal(err)
	}
	if all, _ := st.loadAll(); len(all) != 0 {
		t.Fatalf("a deleted session came back: %+v", all)
	}
	kept, _ := filepath.Glob(filepath.Join(dir, trashDir, "s3-*.json"))
	if len(kept) != 1 {
		t.Fatalf("the deleted session must be in the trash: %v", kept)
	}
	if rec, err := readRecord(kept[0]); err != nil || rec.Title != "keep me too" {
		t.Errorf("the trash holds the last version: %+v err=%v", rec, err)
	}
}

func TestDeletingWhereTheTrashCannotBeMadeIsReported(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	st, _ := newSessionStore(dir)
	seedConversation(t, st, "s4", "x")
	if err := os.WriteFile(filepath.Join(dir, trashDir), []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.delete("s4"); err == nil {
		t.Fatal("a delete that cannot keep the file must say so")
	}
	if _, err := os.Stat(st.path("s4")); err != nil {
		t.Error("and the session file must still be there")
	}
}

// TestASyncedWriteReportsAFullDisk: /dev/full accepts the open and refuses the write, the way
// a full disk does.
func TestASyncedWriteReportsAFullDisk(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full on this platform")
	}
	if err := writeSynced("/dev/full", []byte("data")); err == nil {
		t.Error("a write that the disk refuses must be reported")
	}
	// /dev/null takes the write and refuses fsync (EINVAL): the flush is what failed.
	if err := writeSynced("/dev/null", []byte("data")); err == nil {
		t.Error("a sync that the disk refuses must be reported")
	}
	if err := writeSynced(filepath.Join(t.TempDir(), "no", "such", "dir", "f"), []byte("x")); err == nil {
		t.Error("an unopenable path must be reported")
	}
}

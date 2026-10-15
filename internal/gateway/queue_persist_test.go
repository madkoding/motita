package gateway

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
)

// The queue survives a restart, on both paths a session is restored by, and what was queued runs
// on the new process. It lived in memory only, so a restart (an upgrade) dropped messages the
// person had been told would run.
//
// The slot is held by something that is not a run while the messages are queued, which is also
// the check that such a holder is not saved as a run in flight: the next start would otherwise
// run the session's last task again.
func TestTheQueueSurvivesARestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	rec := &queueRec{}
	svc := func() *fakeService {
		return &fakeService{task: func(_ context.Context, task string, _ func(string, ...any)) (string, error) {
			rec.add(task)
			return "done " + task, nil
		}}
	}
	opts := func() Options {
		return Options{
			Token: testToken, WorkspaceDir: t.TempDir(), SessionDir: dir,
			ProjectDir: filepath.Join(t.TempDir(), "projects"),
			Service:    svc(),
			NewService: func() (Service, error) { return svc(), nil },
		}
	}

	first, _ := startServer(t, opts())
	other := seedConversationOn(t, first, "s-other")
	ids := []string{DefaultSession, other}
	for _, id := range ids {
		c, _ := first.lookup(id)
		c.takeRunSlot()
		w := post(t, first, sessionPath(first, id, "/queue"), `{"task":"queued in `+id+`"}`, testToken)
		if w.Code != http.StatusAccepted {
			t.Fatalf("queue in %s: %d %s", id, w.Code, w.Body.String())
		}
		saved := readRecordOnDisk(t, dir, id)
		if len(saved.Queue) != 1 || saved.Queue[0] != "queued in "+id {
			t.Fatalf("%s: the queued message was not written to disk: %+v", id, saved.Queue)
		}
		if saved.Running {
			t.Fatalf("%s: a slot held by no run was saved as a run in flight", id)
		}
	}
	_ = first.Close(context.Background())
	if rec.ran("queued in " + DefaultSession) {
		t.Fatal("the message ran on the first process although its slot was held")
	}

	second, _ := startServer(t, opts())
	for _, id := range ids {
		waitUntil(t, "the restored message of "+id+" to run", func() bool { return rec.ran("queued in " + id) })
		c, _ := second.lookup(id)
		waitIdle(t, c)
	}
}

// An interruption's message is saved as soon as it is queued.
func TestAnInterruptionsMessageIsSaved(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	c := srv.conversationOf(a.ID)
	c.takeRunSlot()
	w := post(t, srv, "/v1/sessions/"+a.ID+"/interrupt", `{"task":"next"}`, testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("interrupt: %d", w.Code)
	}
	if saved := readRecordOnDisk(t, srv.opts.SessionDir, a.ID); len(saved.Queue) != 1 {
		t.Fatalf("the interruption's message was not saved: %+v", saved.Queue)
	}
	srv.releaseAndDrain(c)
	waitUntil(t, "the message to run", func() bool { return rec.ran("next") })
	waitIdle(t, c)
}

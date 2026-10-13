package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// listedIDs is what GET /v1/sessions lists.
func listedIDs(t *testing.T, srv *Server) []string {
	t.Helper()
	w := get(t, srv, "/v1/sessions", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("list answered %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Sessions []SessionStatus `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(out.Sessions))
	for _, s := range out.Sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

// A gateway nobody has talked to lists no session: the default one exists for the terminal and
// the scheduler, but the user never created it, and listing it is what put a "New session" in
// the sidebar of a fresh gateway.
func TestAnEmptyDefaultSessionIsNotListed(t *testing.T) {
	srv := newTestServer(t, recordingService(nil))
	if ids := listedIDs(t, srv); len(ids) != 0 {
		t.Fatalf("a fresh gateway lists %v, want nothing", ids)
	}
	// It is still there for whoever talks to it.
	runTask(t, srv, "hello")
	if ids := listedIDs(t, srv); len(ids) != 1 || ids[0] != DefaultSession {
		t.Fatalf("after a turn the list is %v, want the default session", ids)
	}
}

// Deleting every session leaves none: deleting the default one resets it, and empty again it
// leaves the list instead of coming back as a new session nobody asked for.
func TestDeletingEverySessionLeavesNone(t *testing.T) {
	srv := newTestServer(t, recordingService(nil), func(o *Options) {
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	runTask(t, srv, "hello")
	other := createSessionIn(t, srv, "")
	if ids := listedIDs(t, srv); len(ids) != 2 {
		t.Fatalf("list = %v, want the default and %s", ids, other.ID)
	}
	for _, id := range []string{DefaultSession, other.ID} {
		if w := deleteReq(t, srv, sessionPath(srv, id, ""), testToken); w.Code >= 300 {
			t.Fatalf("delete %s answered %d: %s", id, w.Code, w.Body.String())
		}
	}
	if ids := listedIDs(t, srv); len(ids) != 0 {
		t.Fatalf("after deleting everything the list is %v, want nothing", ids)
	}
}

// A renamed default session is the user's, even before it has a turn.
func TestARenamedDefaultSessionIsListed(t *testing.T) {
	srv := newTestServer(t, recordingService(nil))
	defaultConv(t, srv).setTitle("mine")
	if ids := listedIDs(t, srv); len(ids) != 1 {
		t.Fatalf("list = %v, want the renamed default session", ids)
	}
}

// The answer a turn was shown with comes back with its checkpoint: the transcript keeps only
// the agent's one-line account, and a conversation opened again from it lost the full answer
// and its report.
func TestATurnKeepsTheAnswerItWasShownWith(t *testing.T) {
	svc := &fakeService{}
	svc.task = func(_ context.Context, task string, _ func(string, ...any)) (string, error) {
		svc.transcript = append(svc.transcript, agent.DialogueTurn{User: task, Agent: "short", Kind: "task"})
		return "the whole answer, as the user read it", nil
	}
	dir := t.TempDir()
	srv := newTestServer(t, svc, func(o *Options) { o.SessionDir = dir })
	runTask(t, srv, "explain")

	cps := checkpointsOf(t, srv)
	if len(cps) != 1 || cps[0].Outcome == nil || cps[0].Outcome.Result != "the whole answer, as the user read it" {
		t.Fatalf("checkpoints = %+v, want the answer kept with the turn", cps)
	}
	if err := srv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := newTestServer(t, &fakeService{}, func(o *Options) { o.SessionDir = dir })
	cps = checkpointsOf(t, again)
	if len(cps) != 1 || cps[0].Outcome == nil || cps[0].Outcome.Result != "the whole answer, as the user read it" {
		t.Fatalf("after a restart: %+v", cps)
	}
}

// A turn that failed keeps the error it was shown with.
func TestATurnKeepsTheErrorItFailedWith(t *testing.T) {
	svc := &fakeService{}
	svc.task = func(context.Context, string, func(string, ...any)) (string, error) {
		return "", errors.New("the provider refused")
	}
	srv := newTestServer(t, svc)
	runTask(t, srv, "try")
	cps := checkpointsOf(t, srv)
	if len(cps) != 1 || cps[0].Outcome == nil || cps[0].Outcome.Error != "the provider refused" {
		t.Fatalf("checkpoints = %+v, want the error kept with the turn", cps)
	}
}

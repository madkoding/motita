package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func newThreadSession(t *testing.T, srv *Server, projectID, extra string) SessionStatus {
	t.Helper()
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+projectID+`"`+extra+`}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	return ss
}

// A session is a thread only when it asks to be one of a session of its own project; a thread gets
// a worktree of its own, and a thread of a thread is a thread of the main session.
func TestAThreadIsASessionWithAParentAndAWorktree(t *testing.T) {
	srv, projectDir, mainID := newSessionInProject(t)
	pid := onlyProjectID(t, srv)
	plain := newThreadSession(t, srv, pid, "")
	if plain.ParentID != "" {
		t.Fatalf("a session that asks for no parent is a main one, got %q", plain.ParentID)
	}
	thread := newThreadSession(t, srv, pid, `,"parent_id":"`+mainID+`"`)
	if thread.ParentID != mainID {
		t.Fatalf("parent %q, want %q", thread.ParentID, mainID)
	}
	if thread.Workspace == projectDir || filepath.Dir(thread.Workspace) == projectDir {
		t.Errorf("a thread must have a worktree of its own, got %q", thread.Workspace)
	}
	if _, err := os.Stat(thread.Workspace); err != nil {
		t.Errorf("the thread's worktree is missing: %v", err)
	}
	nested := newThreadSession(t, srv, pid, `,"parent_id":"`+thread.ID+`"`)
	if nested.ParentID != mainID {
		t.Errorf("the tree is one level deep: parent %q, want %q", nested.ParentID, mainID)
	}
	// An unknown parent, or a session of no project, is ignored.
	free := newThreadSession(t, srv, "", "")
	for _, asked := range []string{"nope", free.ID} {
		if got := newThreadSession(t, srv, pid, `,"parent_id":"`+asked+`"`).ParentID; got != "" {
			t.Errorf("parent %q must be ignored, got %q", asked, got)
		}
	}
}

// Deleting the main session hands the project to its oldest thread.
func TestDeletingTheMainSessionPromotesItsOldestThread(t *testing.T) {
	srv, _, mainID := newSessionInProject(t)
	pid := onlyProjectID(t, srv)
	a := newThreadSession(t, srv, pid, `,"parent_id":"`+mainID+`"`)
	b := newThreadSession(t, srv, pid, `,"parent_id":"`+mainID+`"`)
	if rec := doDelete(t, srv, "/v1/sessions/"+mainID+"?force=1"); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if got := convNamed(t, srv, a.ID).parent(); got != "" {
		t.Errorf("the oldest thread must become the main session, parent %q", got)
	}
	if got := convNamed(t, srv, b.ID).parent(); got != a.ID {
		t.Errorf("the other threads follow the new main session, parent %q", got)
	}
	// Deleting a thread leaves the rest alone.
	if rec := doDelete(t, srv, "/v1/sessions/"+b.ID+"?force=1"); rec.Code != http.StatusNoContent {
		t.Fatalf("delete thread: %d", rec.Code)
	}
}

// Sessions saved before threads existed are adopted: the oldest of a project is its main session.
func TestOldSessionsOfAProjectAreAdoptedAsThreads(t *testing.T) {
	srv, _, first := newSessionInProject(t)
	pid := onlyProjectID(t, srv)
	second := newThreadSession(t, srv, pid, "")
	srv.adoptThreads()
	if got := convNamed(t, srv, second.ID).parent(); got != first {
		t.Errorf("parent %q, want the oldest session %q", got, first)
	}
	if got := convNamed(t, srv, first).parent(); got != "" {
		t.Errorf("the main session has no parent, got %q", got)
	}
	srv.adoptThreads() // idempotent
	if got := convNamed(t, srv, second.ID).parent(); got != first {
		t.Errorf("adopting twice changed the parent to %q", got)
	}
}

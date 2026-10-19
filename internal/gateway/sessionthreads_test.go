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

// The first session of a project is its main one and works in the project's folder; the next ones
// are threads of it, each in a worktree of its own.
func TestAProjectHasOneMainSessionAndTheRestAreThreads(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	pid := onlyProjectID(t, srv)
	// newSessionInProject already made the project's first session, so it is the main one.
	thread := newThreadSession(t, srv, pid, "")
	if thread.ParentID == "" {
		t.Fatal("the second session of a project must be a thread of the first")
	}
	if thread.Workspace == projectDir || filepath.Dir(thread.Workspace) == projectDir {
		t.Errorf("a thread must have a worktree of its own, got %q", thread.Workspace)
	}
	if _, err := os.Stat(thread.Workspace); err != nil {
		t.Errorf("the thread's worktree is missing: %v", err)
	}
	// A thread of a thread is a thread of the main session.
	nested := newThreadSession(t, srv, pid, `,"parent_id":"`+thread.ID+`"`)
	if nested.ParentID != thread.ParentID {
		t.Errorf("the tree is one level deep: parent %q, want %q", nested.ParentID, thread.ParentID)
	}
	// A parent from another project is ignored.
	other := newThreadSession(t, srv, pid, `,"parent_id":"nope"`)
	if other.ParentID != thread.ParentID {
		t.Errorf("an unknown parent falls back to the main session, got %q", other.ParentID)
	}
}

// Deleting the main session hands the project to its oldest thread.
func TestDeletingTheMainSessionPromotesItsOldestThread(t *testing.T) {
	srv, _, mainID := newSessionInProject(t)
	pid := onlyProjectID(t, srv)
	a := newThreadSession(t, srv, pid, "")
	b := newThreadSession(t, srv, pid, "")
	if rec := doDelete(t, srv, "/v1/sessions/"+mainID+"?force=1"); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if got := convNamed(t, srv, a.ID).parent(); got != "" {
		t.Errorf("the oldest thread must become the main session, parent %q", got)
	}
	if got := convNamed(t, srv, b.ID).parent(); got != a.ID {
		t.Errorf("the other threads follow the new main session, parent %q", got)
	}
}

// Sessions saved before threads existed are adopted: the oldest of a project is its main session.
func TestOldSessionsOfAProjectAreAdoptedAsThreads(t *testing.T) {
	srv, _, first := newSessionInProject(t)
	pid := onlyProjectID(t, srv)
	second := newThreadSession(t, srv, pid, "")
	// Forget the parent, as a record from before the field would.
	convNamed(t, srv, second.ID).setParent("")
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

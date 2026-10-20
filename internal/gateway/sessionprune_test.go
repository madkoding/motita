package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// TestPruneBlankSessionsKeepsWhatHasContent: an unused session goes, a session with a turn, a name
// of its own or a queued message stays, and the default session is never removed.
func TestPruneBlankSessionsKeepsWhatHasContent(t *testing.T) {
	srv, _ := startServer(t, Options{
		Token: testToken, WorkspaceDir: t.TempDir(), SessionDir: filepath.Join(t.TempDir(), "sessions"),
		ProjectDir: filepath.Join(t.TempDir(), "projects"),
		Service:    &fakeService{},
		NewService: func() (Service, error) { return &fakeService{}, nil },
	})
	blank := createFree(t, srv)
	spoken := createFree(t, srv)
	named := createFree(t, srv)
	queued := createFree(t, srv)

	c, _ := srv.lookup(spoken)
	c.svc.RestoreTranscript([]agent.DialogueTurn{{User: "hi", Agent: "hello"}})
	c, _ = srv.lookup(named)
	c.setTitle("my work")
	c, _ = srv.lookup(queued)
	c.stateMu.Lock()
	c.queue = []string{"later"}
	c.stateMu.Unlock()

	w := post(t, srv, "/v1/sessions/prune-blank", ``, testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("prune answered %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Removed []string `json:"removed"`
		Count   int      `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 1 || len(out.Removed) != 1 || out.Removed[0] != blank {
		t.Fatalf("removed %v, want only %s", out.Removed, blank)
	}
	for _, id := range []string{spoken, named, queued, DefaultSession} {
		if _, ok := srv.lookup(id); !ok {
			t.Errorf("session %s was removed", id)
		}
	}
	if _, ok := srv.lookup(blank); ok {
		t.Error("the blank session is still there")
	}
}

// TestPruneBlankSessionsInAProject: an unused session of a project is removed with its checkout, and
// one whose checkout holds uncommitted work stays.
func TestPruneBlankSessionsInAProject(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	clean := createSessionIn(t, srv, pid)
	dirty := createSessionIn(t, srv, pid)
	if err := os.WriteFile(filepath.Join(dirty.Workspace, "wip.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed := srv.pruneBlankSessions()
	if len(removed) != 1 || removed[0] != clean.ID {
		t.Fatalf("removed %v, want only %s", removed, clean.ID)
	}
	if _, ok := srv.lookup(dirty.ID); !ok {
		t.Fatal("the session with uncommitted work was removed")
	}
	if c, _ := srv.lookup(dirty.ID); c.isClosing() {
		t.Error("a session kept after a refused release is still closing")
	}
	if _, err := os.Stat(clean.Workspace); !os.IsNotExist(err) {
		t.Errorf("the checkout of the removed session is still there: %v", err)
	}
}

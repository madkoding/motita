package gateway

import (
	"encoding/json"
	"net/http"
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

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/gitx"
)

// TestMovingAFreeSessionIntoAProject: the conversation stays and now runs in its own worktree of
// the project; the empty directory it had is released, and a second move is refused.
func TestMovingAFreeSessionIntoAProject(t *testing.T) {
	srv, _, inProject := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	free := createFree(t, srv)
	oldDir := convNamed(t, srv, free).workspaceDir()

	w := post(t, srv, sessionPath(srv, free, "/project"), `{"project_id":"`+id+`"}`, testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("moving answered %d: %s", w.Code, w.Body.String())
	}
	var st SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.ID != free || st.ProjectID != id {
		t.Fatalf("the session must stay itself and join the project: %+v", st)
	}
	if got := gitx.Display(context.Background(), st.Workspace); got != sessionBranch(free) {
		t.Errorf("the moved session works on %q, want its own branch", got)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("the empty directory it left must be released: %v", err)
	}

	for name, c := range map[string]struct {
		path, body string
		want       int
	}{
		"already in one":  {sessionPath(srv, free, "/project"), `{"project_id":"` + id + `"}`, http.StatusConflict},
		"unknown project": {sessionPath(srv, createFree(t, srv), "/project"), `{"project_id":"nope"}`, http.StatusNotFound},
		"bad body":        {sessionPath(srv, inProject, "/project"), `{`, http.StatusBadRequest},
	} {
		if got := post(t, srv, c.path, c.body, testToken).Code; got != c.want {
			t.Errorf("%s: answered %d, want %d", name, got, c.want)
		}
	}
	busy := createFree(t, srv)
	convNamed(t, srv, busy).takeRunSlot()
	if got := post(t, srv, sessionPath(srv, busy, "/project"), `{"project_id":"`+id+`"}`, testToken).Code; got != http.StatusConflict {
		t.Errorf("a working session answered %d, want 409", got)
	}
}

// A file the free session left stays where it was: it is the user's work.
func TestMovingKeepsTheFilesTheSessionLeft(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	free := createFree(t, srv)
	kept := filepath.Join(convNamed(t, srv, free).workspaceDir(), "notes.txt")
	if err := os.WriteFile(kept, []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := post(t, srv, sessionPath(srv, free, "/project"), `{"project_id":"`+id+`"}`, testToken); w.Code != http.StatusOK {
		t.Fatalf("moving answered %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("the file was lost: %v", err)
	}
}

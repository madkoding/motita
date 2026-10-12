package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// createFree opens a session that belongs to no project and returns its id.
func createFree(t *testing.T, srv *Server) string {
	t.Helper()
	w := post(t, srv, "/v1/sessions", ``, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("creating a session answered %d: %s", w.Code, w.Body.String())
	}
	var st SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decoding the session: %v", err)
	}
	return st.ID
}

// TestFreeStandingSessionsGetADirectoryEach: two sessions with no project used to share the
// workspace root, so two agents working at once wrote into the same files. Each one now runs in a
// directory of its own.
func TestFreeStandingSessionsGetADirectoryEach(t *testing.T) {
	root := t.TempDir()
	svcs := []*fakeService{}
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.WorkspaceDir = root
		o.NewService = func() (Service, error) {
			s := &fakeService{}
			s.cfg.Agent.WorkspaceDir = root
			svcs = append(svcs, s)
			return s, nil
		}
	})

	a, b := createFree(t, srv), createFree(t, srv)
	if a == b {
		t.Fatalf("two sessions share the id %s", a)
	}
	for i, id := range []string{a, b} {
		want := filepath.Join(root, "sessions", id)
		if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
			t.Fatalf("session %s has no directory of its own at %s: %v", id, want, err)
		}
		if got := svcs[i].cfg.Agent.WorkspaceDir; got != want {
			t.Errorf("session %s runs in %q, want its own %q", id, got, want)
		}
		c, _ := srv.lookup(id)
		if st := c.status(); st.Workspace != want || st.ProjectID != "" {
			t.Errorf("session %s reports workspace %q project %q", id, st.Workspace, st.ProjectID)
		}
	}

	// A session that belongs to no project is still not something to merge.
	w := post(t, srv, sessionPath(srv, a, "/merge"), `{}`, testToken)
	if w.Code != http.StatusConflict {
		t.Errorf("merging a free-standing session answered %d, want 409", w.Code)
	}
}

// TestDeletingAFreeSessionRemovesOnlyAnEmptyDirectory: the directory goes with the session when
// nothing is in it, and is kept when the agent left files there - they are the user's work.
func TestDeletingAFreeSessionRemovesOnlyAnEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.WorkspaceDir = root
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})

	empty, full := createFree(t, srv), createFree(t, srv)
	kept := filepath.Join(root, "sessions", full, "notes.txt")
	if err := os.WriteFile(kept, []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{empty, full} {
		if w := deleteReq(t, srv, sessionPath(srv, id, ""), testToken); w.Code != http.StatusNoContent {
			t.Fatalf("deleting %s answered %d: %s", id, w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "sessions", empty)); !os.IsNotExist(err) {
		t.Errorf("the empty directory of a deleted session was left behind: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("the files a deleted session left were removed: %v", err)
	}
}

// TestAFreeSessionWithoutAWorkspaceRootKeepsTheConfiguredOne: no root, nothing to place.
func TestAFreeSessionWithoutAWorkspaceRootKeepsTheConfiguredOne(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	id := createFree(t, srv)
	c, _ := srv.lookup(id)
	if ws := c.status().Workspace; ws != "" {
		t.Errorf("a gateway with no workspace root placed a session in %q", ws)
	}
}

// TestAFreeSessionWhoseDirectoryCannotBeMadeKeepsTheSharedOne: a root that is a file cannot hold
// a directory, and the session still starts.
func TestAFreeSessionWhoseDirectoryCannotBeMadeKeepsTheSharedOne(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.WorkspaceDir = file
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	id := createFree(t, srv)
	c, _ := srv.lookup(id)
	if ws := c.status().Workspace; ws != "" {
		t.Errorf("a session whose directory could not be made reports %q", ws)
	}
}

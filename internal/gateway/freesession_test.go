package gateway

import (
	"context"
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

// TestAFreeSessionGetsGitOnlyWhenAskedFor: git is opt-in, by the gateway's setting or by the
// request, and the request wins. A session that got git and never committed leaves nothing behind.
func TestAFreeSessionGetsGitOnlyWhenAskedFor(t *testing.T) {
	root := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.WorkspaceDir = root
		o.FreeSessionGit = true
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	isRepo := func(id string) bool {
		_, err := os.Stat(filepath.Join(root, "sessions", id, ".git"))
		return err == nil
	}
	create := func(body string) string {
		t.Helper()
		w := post(t, srv, "/v1/sessions", body, testToken)
		if w.Code != http.StatusCreated {
			t.Fatalf("creating %s answered %d: %s", body, w.Code, w.Body.String())
		}
		var st SessionStatus
		_ = json.Unmarshal(w.Body.Bytes(), &st)
		return st.ID
	}

	bySetting, plain, asked := create(``), create(`{"git":false}`), create(`{"git":true}`)
	if !isRepo(bySetting) || !isRepo(asked) {
		t.Errorf("a session that asked for git, or got it from the setting, is not a repository")
	}
	if isRepo(plain) {
		t.Errorf("a session that asked for no git got a repository")
	}

	srv.opts.FreeSessionGit = false
	if id := create(``); isRepo(id) {
		t.Errorf("git was made without the setting or the request asking for it")
	}

	if w := deleteReq(t, srv, sessionPath(srv, asked, ""), testToken); w.Code != http.StatusNoContent {
		t.Fatalf("deleting answered %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "sessions", asked)); !os.IsNotExist(err) {
		t.Errorf("a repository nobody committed to was left behind: %v", err)
	}
}

// TestAFailedGitInitStillStartsTheSession: git is a convenience, so a directory git cannot use
// still gives a usable session.
func TestAFailedGitInitStillStartsTheSession(t *testing.T) {
	root := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.WorkspaceDir = root
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	t.Setenv("PATH", t.TempDir()) // no git on the path
	w := post(t, srv, "/v1/sessions", `{"git":true}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("a session whose git init failed was refused: %d %s", w.Code, w.Body.String())
	}
}

// TestRemovingAMissingDirectoryIsHarmless: a directory already gone is nothing to remove.
func TestRemovingAMissingDirectoryIsHarmless(t *testing.T) {
	removeIfUnused(filepath.Join(t.TempDir(), "gone"))
}

// TestOrphanedSessionDirectoriesArePrunedOnStart: directories no session owns any more are removed
// when the gateway starts, unless they hold the user's work or belong to a session still on disk.
func TestOrphanedSessionDirectoriesArePrunedOnStart(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(t.TempDir(), "sessions")
	opts := func() Options {
		return Options{
			Token: testToken, WorkspaceDir: work, SessionDir: dir,
			ProjectDir: filepath.Join(t.TempDir(), "projects"),
			Service:    &fakeService{},
			NewService: func() (Service, error) { return &fakeService{}, nil },
		}
	}
	first, _ := startServer(t, opts())
	kept := createFree(t, first)
	_ = first.Close(context.Background())

	sessions := filepath.Join(work, "sessions")
	for _, d := range []string{"sgone", "swork", "sgit"} {
		if err := os.MkdirAll(filepath.Join(sessions, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sessions, "swork", "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sessions, "sgit", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "stray-file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	startServer(t, opts())
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(sessions, name))
		return err == nil
	}
	if exists("sgone") || exists("sgit") {
		t.Errorf("an orphaned directory with nothing of the user's in it was kept")
	}
	if !exists("swork") || !exists("stray-file") {
		t.Errorf("an orphaned directory holding the user's files, or a file, was removed")
	}
	if !exists(kept) {
		t.Errorf("the directory of a session that still exists was pruned")
	}
}

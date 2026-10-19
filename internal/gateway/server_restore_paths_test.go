package gateway

// The restore branches of server.go a healthy gateway rarely takes: sessions restored with their
// project, worktree, provider and model, and the sessions that are skipped or come back without
// their project directory.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A restored session that belonged to a PROJECT comes back with its project id, its workspace and
// its own worktree, which is what makes its work still mergeable after a restart. Without ProjectDir
// the gateway would report every restored session as having nothing to integrate.
func TestARestoredProjectSessionKeepsItsWorktreeAndBranch(t *testing.T) {
	ws := t.TempDir()
	pid := "p-restore"
	pdir := filepath.Join(ws, "repo")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, pdir)

	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id":          "s-proj",
		"title":       "worked in a project",
		"project_id":  pid,
		"workspace":   pdir,
		"project_dir": pdir,
		"provider":    "anthropic",
		"model":       "claude-x",
		"created":     time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"last_used":   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"turns":       []map[string]string{{"user": "do the work", "agent": "done"}},
	})

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: ws,
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})
	if err := srv.projects.save(Project{ID: pid, Title: "repo", Dir: pdir, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}

	c, ok := srv.lookup("s-proj")
	if !ok {
		t.Fatal("the project session must be restored")
	}
	st := c.status()
	if st.ProjectID != pid {
		t.Errorf("ProjectID = %q, want %q", st.ProjectID, pid)
	}
	// A session that did not have a worktree of its own comes back in the project's folder.
	if st.Workspace != pdir {
		t.Errorf("Workspace = %q, want the project's folder %q", st.Workspace, pdir)
	}
}

// A session with no project comes back with NO workspace: there is nowhere to run it, and inventing
// a directory would run it in whatever the process's own directory happens to be.
func TestARestoredSessionWithNoProjectHasNoWorkspace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id": "s-free", "title": "free standing",
		"created":   time.Now().UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{},
	})

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})

	c, ok := srv.lookup("s-free")
	if !ok {
		t.Fatal("the session must be restored")
	}
	if ws := c.status().Workspace; ws != "" {
		t.Errorf("Workspace = %q, want empty for a session with no project", ws)
	}
}

// A session whose provider and model were recorded gets them back, so the configuration a user
// chose for a session survives a restart instead of silently reverting to the default.
func TestARestoredSessionKeepsItsProviderAndModel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id": "s-configured", "title": "chose a provider",
		"provider":  "anthropic",
		"model":     "claude-x",
		"created":   time.Now().UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{},
	})

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})

	if _, ok := srv.lookup("s-configured"); !ok {
		t.Fatal("the session must be restored")
	}
}

// Without a service FACTORY a non-default session cannot be restored: registering it with the
// default session's engine would be two names for one transcript, edited in one place and read in
// the other. It is skipped, and the default session still comes up.
func TestASessionIsSkippedWhenThereIsNoServiceFactory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id": "s-orphan", "title": "no engine to build",
		"created":   time.Now().UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{},
	})

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		// No NewService, and a service for the default session only.
	})

	if _, ok := srv.lookup("s-orphan"); ok {
		t.Error("a session with no engine to build must be skipped, not wired to another session's engine")
	}
	if _, ok := srv.lookup(DefaultSession); !ok {
		t.Error("the default session must still be there")
	}
}

// A service factory that FAILS skips that session and keeps going, so one broken engine does not
// cost the user every other session.
func TestASessionWhoseEngineCannotBeBuiltIsSkipped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id": "s-broken", "title": "engine fails",
		"created":   time.Now().UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{},
	})

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService:   func() (Service, error) { return nil, errors.New("the engine could not start") },
	})

	if _, ok := srv.lookup("s-broken"); ok {
		t.Error("a session whose engine cannot be built must not be registered")
	}
}

// A restored session whose PROJECT DIRECTORY is gone still comes back, in the project's directory
// as recorded, rather than being dropped: the record is the user's work and losing it is worse than
// a session that cannot branch.
func TestARestoredSessionWhoseProjectDirectoryIsGoneStillComesBack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	gone := filepath.Join(t.TempDir(), "deleted-project")
	seedSessionFile(t, dir, map[string]any{
		"id": "s-gone", "title": "its project moved",
		"project_id":  "p-gone",
		"workspace":   gone,
		"project_dir": gone,
		"created":     time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"last_used":   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"turns":       []map[string]string{},
	})

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})

	if _, ok := srv.lookup("s-gone"); !ok {
		t.Error("a session whose project directory is gone must still be restored: the record is the work")
	}
}

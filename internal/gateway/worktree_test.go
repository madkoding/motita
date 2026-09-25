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

// These tests cover the worktree-per-session feature: a session that belongs to
// a project gets its own checkout on its own branch, so two sessions can work
// at the same time without editing each other's files.
//
// They use REAL git in a temporary repository, for the same reason the rest of
// the git integration does: the behaviour under test is git's, and a fake would
// only assert what the author believed git does.

// sessionWorkspace is the worktree path a session is expected to get: one
// directory per session, under a "worktrees" folder in the workspace root. The
// tests name it with the same helper the gateway uses, so a test cannot pass
// while production writes somewhere else.
func sessionWorkspace(t *testing.T, srv *Server, sessionID string) string {
	t.Helper()
	return filepath.Join(srv.opts.WorkspaceDir, "worktrees", sessionID)
}

// TestProjectSessionRunsInItsOwnWorktree is the feature: creating a session in
// a git project gives it its own checkout on its own branch, rather than
// pointing it at the project's directory where a second session would collide.
func TestProjectSessionRunsInItsOwnWorktree(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}

	// The session works in its OWN directory, not the project's checkout.
	want := sessionWorkspace(t, srv, ss.ID)
	if ss.Workspace != want {
		t.Errorf("workspace = %q, want %q: a project session must run in its own worktree", ss.Workspace, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the worktree must exist on disk: %v", err)
	}

	// It is a REAL worktree of the project, on its own branch.
	if branch := gitx.Display(ctx, want); branch != sessionBranch(ss.ID) {
		t.Errorf("worktree branch = %q, want %q", branch, sessionBranch(ss.ID))
	}
	if root, err := gitx.Root(ctx, want); err != nil || root != want {
		t.Errorf("Root(worktree) = %q, %v: the worktree must be its own checkout root", root, err)
	}

	// The project's own checkout is untouched: this is what stops two sessions
	// from editing the same files.
	if branch := gitx.Display(ctx, p.Dir); branch != "main" {
		t.Errorf("the project's checkout moved to %q: a worktree must not disturb it", branch)
	}
}

// TestSecondSessionGetsASeparateWorktree: the point of the feature is that two
// sessions in one project do not collide, so the second must get a DIFFERENT
// directory and branch than the first.
func TestSecondSessionGetsASeparateWorktree(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")

	first := createSessionIn(t, srv, pid)
	second := createSessionIn(t, srv, pid)

	if first.Workspace == second.Workspace {
		t.Fatalf("both sessions got %q: two sessions in one project must not share a checkout", first.Workspace)
	}
	if first.Branch == second.Branch {
		t.Errorf("both sessions are on %q: each session needs its own branch", first.Branch)
	}
	for _, ws := range []string{first.Workspace, second.Workspace} {
		if _, err := os.Stat(ws); err != nil {
			t.Errorf("worktree %q must exist: %v", ws, err)
		}
	}
}

// TestSessionInAProjectWithoutGitKeepsTheProjectDirectory: worktrees are a git
// feature, and a project that is not a repository must keep working. The
// session falls back to the project's directory rather than failing to start.
func TestSessionInAProjectWithoutGitKeepsTheProjectDirectory(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)

	dir := filepath.Join(ws, "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := srv.projects.save(Project{ID: "p-plain", Title: "plain", Dir: dir}); err != nil {
		t.Fatal(err)
	}

	ss := createSessionIn(t, srv, "p-plain")
	if ss.Workspace != dir {
		t.Errorf("workspace = %q, want the project directory %q: a non-git project has no worktrees", ss.Workspace, dir)
	}
}

// TestFreeSessionHasNoWorktree: a session that belongs to no project has
// nothing to branch from, and must not be given a worktree.
func TestFreeSessionHasNoWorktree(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())

	w := post(t, srv, "/v1/sessions", `{}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create free session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	if ss.Workspace != "" {
		t.Errorf("workspace = %q, want empty: a free-standing session has no worktree", ss.Workspace)
	}
}

// TestSessionWorktreeIsCreatedOnceAndReused: creating a session must be
// idempotent about its worktree. A resumed session - or one whose creation is
// retried - must find its own work again rather than failing because the
// directory is already there.
func TestSessionWorktreeIsCreatedOnceAndReused(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	ss := createSessionIn(t, srv, pid)

	// Work committed in the worktree survives a second ensure: the branch is
	// attached, not reset.
	if err := os.WriteFile(filepath.Join(ss.Workspace, "work.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", ss.Workspace, "add", "work.txt")
	mustRun(t, "git", "-C", ss.Workspace, "commit", "-qm", "session work")

	again, err := srv.sessionWorktree(ctx, p.Dir, ss.ID)
	if err != nil {
		t.Fatalf("sessionWorktree on an existing worktree: %v", err)
	}
	if again != ss.Workspace {
		t.Errorf("the worktree path changed: %q -> %q", ss.Workspace, again)
	}
	if _, err := os.Stat(filepath.Join(ss.Workspace, "work.txt")); err != nil {
		t.Errorf("the committed work must survive: %v", err)
	}
}

// createSessionIn creates a session in a project and returns its status.
func createSessionIn(t *testing.T, srv *Server, projectID string) SessionStatus {
	t.Helper()
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+projectID+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	return ss
}

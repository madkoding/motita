package gateway

// Deleting a session must give back the checkout it was working in.
//
// Measured on a real gateway: six orphaned worktrees of one project, two of them over
// 800 MB, none of which any session would ever touch again. The conversation was
// forgotten and its file removed, and nothing released the directory or its git
// registration.
//
// The risk the fix must NOT take is the opposite one: a worktree with uncommitted changes
// is a copy of the project whose only pointer is the session being deleted, so removing it
// destroys work with no way back. That case refuses the deletion and says which session
// and how many files, so the decision stays with the user.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo initialises a repository with one commit, which is what a worktree needs.
func gitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@localhost",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@localhost")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "initial")
}

// newSessionInProject creates a project and a session inside it, and returns the server,
// the project's directory and the session's id.
func newSessionInProject(t *testing.T) (*Server, string, string) {
	t.Helper()
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, projectDir)

	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: workspace,
		ProjectDir:   filepath.Join(workspace, "projects"),
		SessionDir:   filepath.Join(workspace, "sessions"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})

	// Register the project through the API, so the test exercises the same path a user does.
	rec := post(t, srv, "/v1/projects", `{"title":"proj","dir":"proj"}`, testToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating the project answered %d: %s", rec.Code, rec.Body.String())
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &proj); err != nil {
		t.Fatal(err)
	}

	created := post(t, srv, "/v1/sessions", `{"project_id":"`+proj.ID+`"}`, testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("creating the session answered %d: %s", created.Code, created.Body.String())
	}
	var conv SessionStatus
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}
	return srv, projectDir, conv.ID
}

// TestDeletingASessionReleasesItsWorktree: the fix. The session's checkout must be gone
// and its git registration released, so `git worktree list` no longer names it.
func TestDeletingASessionReleasesItsWorktree(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("the session must have its own worktree to test this: %v", err)
	}
	if !worktreeRegistered(t, projectDir, wt) {
		t.Fatalf("the worktree must start registered")
	}

	rec := doDelete(t, srv, "/v1/sessions/"+id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deleting a clean session answered %d: %s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the session's directory must be gone, still there: %v", err)
	}
	if worktreeRegistered(t, projectDir, wt) {
		t.Error("the git registration must be released, or `git worktree list` keeps naming a session nobody has")
	}
}

// TestDeletingASessionWithUncommittedWorkIsRefused: the case that must NOT be silent. The
// deletion is refused, the session survives, and the message says which session and that
// there are changes — so the user chooses, instead of losing the work.
func TestDeletingASessionWithUncommittedWorkIsRefused(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	// Uncommitted work in the session's own checkout: the state that cannot be recovered.
	if err := os.WriteFile(filepath.Join(wt, "work-in-progress.md"), []byte("not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := doDelete(t, srv, "/v1/sessions/"+id)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a session with uncommitted work must be refused with 409, got %d: %s", rec.Code, rec.Body.String())
	}
	msg := rec.Body.String()
	if !strings.Contains(msg, "uncommitted") {
		t.Errorf("the message must say the work is uncommitted, got: %s", msg)
	}
	// The message must name the way out, or the user is stuck with a session they cannot delete.
	if !strings.Contains(msg, "worktree remove") {
		t.Errorf("the message must name the command that removes it anyway, got: %s", msg)
	}

	// The session AND its work must both survive the refusal.
	if _, err := os.Stat(filepath.Join(wt, "work-in-progress.md")); err != nil {
		t.Errorf("the work must survive the refusal: %v", err)
	}
	if _, ok := srv.lookup(id); !ok {
		t.Error("the session must survive the refusal, so the decision stays with the user")
	}
	if !worktreeRegistered(t, projectDir, wt) {
		t.Error("the registration must survive the refusal too")
	}
}

// TestDeletingASessionWithNoProjectIsUnaffected: a free-standing session has no worktree
// and its deletion goes through as before. This is what keeps the common case fast.
func TestDeletingASessionWithNoProjectIsUnaffected(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, withFactory())
	created := post(t, srv, "/v1/sessions", "{}", testToken)
	var conv SessionStatus
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}
	rec := doDelete(t, srv, "/v1/sessions/"+conv.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a session with no project must delete as before, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestDeletingASessionWhoseWorktreeIsGoneStillWorks: the worktree was removed by hand. The
// deletion must go through rather than refusing over a directory that no longer exists —
// which is the state a `git worktree prune` or an rm -rf leaves behind.
func TestDeletingASessionWhoseWorktreeIsGoneStillWorks(t *testing.T) {
	srv, _, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	// prune clears the registration, which is what a user does after removing a directory.
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = filepath.Join(srv.opts.WorkspaceDir, "proj")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prune: %v\n%s", err, out)
	}

	rec := doDelete(t, srv, "/v1/sessions/"+id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a session whose worktree is already gone must delete, got %d: %s", rec.Code, rec.Body.String())
	}
}

// worktreeRegistered reports whether git still knows the path as a worktree of repoDir.
func worktreeRegistered(t *testing.T, repoDir, path string) bool {
	t.Helper()
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v\n%s", err, out)
	}
	// The comparison is on the resolved path, because git reports the canonical spelling
	// and a test's TempDir may be reached through a symlink (/tmp on some systems).
	want := resolvePath(t, path)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		got := resolvePath(t, strings.TrimPrefix(line, "worktree "))
		if got == want {
			return true
		}
	}
	return false
}

// resolvePath canonicalises a path that may not exist any more, by resolving its deepest
// existing ancestor.
func resolvePath(t *testing.T, path string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	dir, base := filepath.Split(path)
	if dir == "" || dir == "/" {
		return path
	}
	return filepath.Join(resolvePath(t, filepath.Clean(dir)), base)
}

// doDelete sends an authenticated DELETE and returns the recorder.
func doDelete(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.BaseURL()+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestDeletingASessionWhoseWorkspaceIsTheProjectIsUnaffected: the workspace falls back to
// the project's own checkout when its worktree could not be created, and removing THAT
// would delete the user's project. The deletion must go through and leave the checkout
// alone — this is the branch that protects the project itself.
func TestDeletingASessionWhoseWorkspaceIsTheProjectIsUnaffected(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	c, ok := srv.lookup(id)
	if !ok {
		t.Fatal("the session must exist")
	}
	// The state a failed worktree creation leaves: the session runs in the project itself.
	c.setProjectID(c.projectID, projectDir, projectDir)

	rec := doDelete(t, srv, "/v1/sessions/"+id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a session running in the project itself must delete, got %d: %s", rec.Code, rec.Body.String())
	}
	// The project's own checkout MUST survive. That is the whole point of this branch.
	if _, err := os.Stat(filepath.Join(projectDir, "README.md")); err != nil {
		t.Fatalf("the project's checkout must survive: %v", err)
	}
}

// TestTheRemovalIsRefusedWhenTheCheckoutCannotBeInspected: the count could not be read, so
// the checkout cannot be PROVEN clean. Refusing is the answer that cannot lose work, and
// the message says how to proceed by hand.
//
// Driven through the package's seam, because no filesystem produces "the count is
// unreadable" on demand — the state a revoked mount or a broken registration leaves.
func TestTheRemovalIsRefusedWhenTheCheckoutCannotBeInspected(t *testing.T) {
	srv, _, id := newSessionInProject(t)
	c, _ := srv.lookup(id)

	restore := worktreeInspect
	worktreeInspect = func(context.Context, string) (int, error) {
		return 0, errors.New("the checkout could not be read")
	}
	t.Cleanup(func() { worktreeInspect = restore })

	err := srv.releaseWorktree(c)
	if err == nil {
		t.Fatal("a checkout that cannot be inspected must be refused, not removed")
	}
	if !strings.Contains(err.Error(), "could not be inspected") {
		t.Errorf("the message must say the inspection failed, got: %v", err)
	}
	if !strings.Contains(err.Error(), "worktree remove") {
		t.Errorf("the message must name the way out, got: %v", err)
	}
}

// TestTheRemovalIsRefusedWhenGitRefuses: the checkout was proven clean and git still said
// no. That is reported rather than forced — this program does not delete what it cannot
// prove is safe — and the message names the command that does.
func TestTheRemovalIsRefusedWhenGitRefuses(t *testing.T) {
	srv, _, id := newSessionInProject(t)

	restore := worktreeRemove
	worktreeRemove = func(context.Context, string, string, bool) error {
		return errors.New("git said no")
	}
	t.Cleanup(func() { worktreeRemove = restore })

	// The deletion goes through the handler, so the refusal is proven end to end: a 409
	// and a session that survives.
	rec := doDelete(t, srv, "/v1/sessions/"+id)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a removal git refuses must answer 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "could not be removed") {
		t.Errorf("the message must say the removal failed, got: %s", rec.Body.String())
	}
	if _, ok := srv.lookup(id); !ok {
		t.Error("the session must survive, so the user can act on the message")
	}
}

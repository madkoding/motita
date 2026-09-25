package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// These tests exercise the gateway's integration with git: the branch a
// session reports, the branch a project reports, and the merge that integrates
// a session's work back into the project's checkout. They use REAL git in a
// temporary repository, for the same reason internal/gitx does: the behaviour
// being tested is git's behaviour, and a fake would only assert what the author
// believed git does.

// gitInit creates a repository with one commit on main at dir.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	mustRun(t, "git", "init", "-q", "-b", "main", dir)
	mustRun(t, "git", "-C", dir, "config", "user.email", "test@example.com")
	mustRun(t, "git", "-C", dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", dir, "add", "f.txt")
	mustRun(t, "git", "-C", dir, "commit", "-qm", "init")
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// withProjects configures a test server with a project store, workspace and
// the ability to create new sessions.
func withProjects(t *testing.T, srv *Server, workspace string) {
	t.Helper()
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	srv.projects = ps
	srv.opts.WorkspaceDir = workspace
	srv.opts.NewService = func() (Service, error) { return &fakeService{}, nil }
}

// makeProject creates a git repo under the workspace and registers it.
func makeProject(t *testing.T, srv *Server, name string) string {
	t.Helper()
	dir := filepath.Join(srv.opts.WorkspaceDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	id := "p-test-" + name
	if err := srv.projects.save(Project{
		ID:      id,
		Title:   name,
		Dir:     dir,
		Created: time.Now(),
	}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	return id
}

// TestSessionReportsItsBranch is the first half of the feature: a session that
// belongs to a project draws the branch the project's checkout is on, and a
// free-standing session draws none.
func TestSessionReportsItsBranch(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")

	// Create a session that belongs to the project.
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ss.Branch != "main" {
		t.Errorf("branch = %q, want main: a session in a git project must report its branch", ss.Branch)
	}

	// A free-standing session has no branch.
	w = post(t, srv, "/v1/sessions", `{}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create free session: %d %s", w.Code, w.Body.String())
	}
	var free SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &free); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if free.Branch != "" {
		t.Errorf("branch = %q, want empty: a free-standing session has no branch", free.Branch)
	}
}

// TestProjectReportsItsBranch is the other half: the project list shows the
// branch each project is on.
func TestProjectReportsItsBranch(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	makeProject(t, srv, "alpha")

	w := get(t, srv, "/v1/projects", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("list projects: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(resp.Projects))
	}
	if resp.Projects[0].Branch != "main" {
		t.Errorf("branch = %q, want main", resp.Projects[0].Branch)
	}
}

// TestProjectReportsNoBranchForNonRepo: a project whose directory is not a git
// repository reports no branch, and the list does not fail.
func TestProjectReportsNoBranchForNonRepo(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	dir := filepath.Join(ws, "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := srv.projects.save(Project{ID: "p-plain", Title: "plain", Dir: dir, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}

	w := get(t, srv, "/v1/projects", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Projects[0].Branch != "" {
		t.Errorf("branch = %q, want empty for a non-repo project", resp.Projects[0].Branch)
	}
}

// TestMergeSessionIntegratesTheBranch: a session's branch is merged back into
// the project's checkout, and the response carries the merge commit's sha.
func TestMergeSessionIntegratesTheBranch(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	// Create a session and make a commit on its branch.
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	branch := sessionBranch(ss.ID)
	wtPath := filepath.Join(ws, "worktrees", ss.ID)
	if err := gitx.AddWorktree(ctx, p.Dir, wtPath, branch); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "new.txt"), []byte("session work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", wtPath, "add", "new.txt")
	mustRun(t, "git", "-C", wtPath, "commit", "-qm", "session work")

	// Merge it back.
	w = post(t, srv, sessionPath(srv, ss.ID, "/merge"), "{}", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.SHA == "" {
		t.Error("the merge must report the commit it produced")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "new.txt")); err != nil {
		t.Errorf("the session's work must be in the project's checkout: %v", err)
	}
}

// TestMergeSessionOnAConflictRollsBack: a conflicting merge is reported as a
// failure, and the project's checkout is returned to what it was.
func TestMergeSessionOnAConflictRollsBack(t *testing.T) {
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
	branch := sessionBranch(ss.ID)
	wtPath := filepath.Join(ws, "worktrees", ss.ID)
	if err := gitx.AddWorktree(ctx, p.Dir, wtPath, branch); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	// Create a conflicting change.
	if err := os.WriteFile(filepath.Join(wtPath, "f.txt"), []byte("from session\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", wtPath, "commit", "-qam", "session edit")
	if err := os.WriteFile(filepath.Join(p.Dir, "f.txt"), []byte("from main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", p.Dir, "commit", "-qam", "main edit")

	w = post(t, srv, sessionPath(srv, ss.ID, "/merge"), "{}", testToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("merge conflict: %d, want 409: %s", w.Code, w.Body.String())
	}
	// The checkout must be back to its own content.
	got, err := os.ReadFile(filepath.Join(p.Dir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "from main" {
		t.Errorf("the checkout must be back to its own content, got %q", string(got))
	}
}

// TestMergeSessionWithoutProjectIs409: a session that does not belong to a
// project cannot be merged, and the error says so.
func TestMergeSessionWithoutProjectIs409(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := post(t, srv, sessionPath(srv, DefaultSession, "/merge"), "{}", testToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("merge without project: %d, want 409: %s", w.Code, w.Body.String())
	}
}

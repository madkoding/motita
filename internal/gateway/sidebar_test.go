package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// These tests cover what the sidebar draws under a title: the branch, the
// worktree a session is working in, how many changes it has, and when it was
// last used. Each field is read live from git, so each is measured here rather
// than assumed - a number on a badge is read as fact, and a wrong one is worse
// than an absent one.

// TestSessionReportsItsChanges counts the session's OWN uncommitted work. The
// count is what tells a user at a glance whether a session has done anything,
// so it has to come from the session's worktree and not from the project's
// checkout.
func TestSessionReportsItsChanges(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")

	ss := createSessionIn(t, srv, pid)
	if ss.Changes != 0 {
		t.Errorf("changes = %d, want 0 for a session that has not written anything", ss.Changes)
	}

	// Two new files and one edit, in the SESSION's worktree.
	if err := os.WriteFile(filepath.Join(ss.Workspace, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ss.Workspace, "b.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ss.Workspace, "f.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := sessionByID(t, srv, ss.ID); got.Changes != 3 {
		t.Errorf("changes = %d, want 3", got.Changes)
	}
}

// TestSessionChangesDoNotIncludeTheProjectCheckout: a session works in its own
// worktree, so edits in the PROJECT's checkout are not the session's work. A
// count that added them would report progress the session did not make.
func TestSessionChangesDoNotIncludeTheProjectCheckout(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	ss := createSessionIn(t, srv, pid)
	if err := os.WriteFile(filepath.Join(p.Dir, "project-only.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := sessionByID(t, srv, ss.ID); got.Changes != 0 {
		t.Errorf("changes = %d, want 0: a change in the project's checkout is not the session's work", got.Changes)
	}
}

// TestSessionReportsItsWorktree is what tells two sessions of one project apart:
// they share a project, a branch prefix and a title format, and the directory is
// the only thing that differs.
func TestSessionReportsItsWorktree(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")

	first := createSessionIn(t, srv, pid)
	second := createSessionIn(t, srv, pid)

	if first.Worktree != first.ID {
		t.Errorf("worktree = %q, want the session id %q", first.Worktree, first.ID)
	}
	if first.Worktree == second.Worktree {
		t.Errorf("both sessions report worktree %q: each session works in its own", first.Worktree)
	}
}

// TestSessionWithoutAWorktreeReportsNone: a free-standing session has no
// worktree, and neither has one that fell back to the project's checkout. An
// invented worktree name would be a distinction the filesystem does not make.
func TestSessionWithoutAWorktreeReportsNone(t *testing.T) {
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
	if ss.Worktree != "" {
		t.Errorf("worktree = %q, want empty for a free-standing session", ss.Worktree)
	}
	if ss.Changes != 0 {
		t.Errorf("changes = %d, want 0 for a session with no workspace", ss.Changes)
	}
}

// TestSessionReportsWhenItWasLastUsed: every session carries the time it was
// last spoken to, and it has to move forward when it is used - a timestamp that
// never changes is a timestamp that means nothing.
func TestSessionReportsWhenItWasLastUsed(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")

	ss := createSessionIn(t, srv, pid)
	if ss.LastUsed.IsZero() {
		t.Fatal("a session must report when it was last used")
	}
	before := ss.LastUsed

	// Touching the session is what withConversation does for every scoped
	// request, which is the call the sidebar makes on every switch.
	time.Sleep(10 * time.Millisecond)
	get(t, srv, sessionPath(srv, ss.ID, "/config"), testToken)

	after := sessionByID(t, srv, ss.ID).LastUsed
	if !after.After(before) {
		t.Errorf("last_used = %v, want later than %v", after, before)
	}
}

// TestProjectReportsItsChangesAndWorktrees: the project header carries its OWN
// checkout's changes and the number of session worktrees under it. The two are
// different numbers about different directories, and the worktree count is read
// from git so a worktree left by a deleted session is still counted.
func TestProjectReportsItsChangesAndWorktrees(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	proj := func() Project {
		t.Helper()
		w := get(t, srv, "/v1/projects", testToken)
		var resp struct {
			Projects []Project `json:"projects"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		for _, got := range resp.Projects {
			if got.ID == pid {
				return got
			}
		}
		t.Fatalf("project %q is missing from the list", pid)
		return Project{}
	}

	if got := proj(); got.Changes != 0 || got.Worktrees != 0 || got.Sessions != 0 {
		t.Errorf("fresh project: changes=%d worktrees=%d sessions=%d, want all zero", got.Changes, got.Worktrees, got.Sessions)
	}

	createSessionIn(t, srv, pid)
	createSessionIn(t, srv, pid)

	got := proj()
	if got.Sessions != 2 {
		t.Errorf("sessions = %d, want 2", got.Sessions)
	}
	if got.Worktrees != 2 {
		t.Errorf("worktrees = %d, want 2: one per session", got.Worktrees)
	}
	if got.Changes != 0 {
		t.Errorf("changes = %d, want 0: no sessions have written into the project's checkout", got.Changes)
	}

	// A change in the PROJECT's own checkout is the project's number, and it
	// must not be attributed to a session's worktree count.
	if err := os.WriteFile(filepath.Join(p.Dir, "project.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = proj()
	if got.Changes != 1 {
		t.Errorf("changes = %d, want 1", got.Changes)
	}
	if got.Worktrees != 2 {
		t.Errorf("worktrees = %d, want 2: the project's own checkout is not a session worktree", got.Worktrees)
	}
}

// TestProjectWithoutGitReportsNoChangesOrWorktrees: a project that is not a
// repository has neither, and the list must not fail over it.
func TestProjectWithoutGitReportsNoChangesOrWorktrees(t *testing.T) {
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
		t.Fatalf("list projects: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(resp.Projects))
	}
	got := resp.Projects[0]
	if got.Changes != 0 || got.Worktrees != 0 || got.Branch != "" {
		t.Errorf("non-repo project: branch=%q changes=%d worktrees=%d, want all empty", got.Branch, got.Changes, got.Worktrees)
	}
}

// TestCountSessionWorktreesSkipsTheProjectAndPrunableEntries pins the two
// exclusions directly. Both are cases where a naive count would report a
// number the user cannot reconcile with anything on disk: the project's own
// checkout is a listed worktree of its own repository, and a prunable
// registration has no directory left.
func TestCountSessionWorktreesSkipsTheProjectAndPrunableEntries(t *testing.T) {
	project := "/work/proj"
	all := []gitx.Worktree{
		{Path: project, Branch: "main"},            // the project's own checkout
		{Path: "/work/wt/sA", Branch: "motita/sA"}, // a real session worktree
		{Path: "/work/wt/gone", Prunable: true},    // directory removed, nothing there to work in
		{Path: "/work/wt/sB", Branch: "feature"},   // a session moved to a branch of its own
	}
	if got := countSessionWorktrees(all, project); got != 2 {
		t.Errorf("count = %d, want 2: the project checkout and the prunable entry are not session worktrees", got)
	}
}

// TestSessionOnTheProjectCheckoutReportsNoWorktree: a session that could not
// get its own worktree runs in the project's directory, and that directory IS a
// listed worktree of the repository - so "git knows this path" answers yes and
// the sidebar would print the project's own folder as the session's worktree.
// The project's checkout is the project; it is not a session's.
func TestSessionOnTheProjectCheckoutReportsNoWorktree(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	// The workspace must be a real directory even though the point of the test
	// is that no worktree is made in it: an EMPTY workspace root makes
	// makeProject create its repository as a RELATIVE path, which lands inside
	// the package directory and leaves a stray repo behind. A root is required,
	// a worktree root is not.
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	// Remove the worktree root so sessionWorktree has nowhere to create one and
	// falls back to the project's own directory.
	srv.opts.WorkspaceDir = ""

	ss := createSessionIn(t, srv, pid)
	if !gitx.SamePath(ss.Workspace, p.Dir) {
		t.Fatalf("workspace = %q, want the project's own checkout %q", ss.Workspace, p.Dir)
	}
	if got := sessionByID(t, srv, ss.ID); got.Worktree != "" {
		t.Errorf("worktree = %q, want empty: the project's checkout is not a session's worktree", got.Worktree)
	}
}

// sessionByID reads one session back from the list endpoint, which is what the
// sidebar draws from - so a test cannot pass on a field the front end never
// receives.
func sessionByID(t *testing.T, srv *Server, id string) SessionStatus {
	t.Helper()
	w := get(t, srv, "/v1/sessions", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("list sessions: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Sessions []SessionStatus `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, s := range resp.Sessions {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %q is missing from the list", id)
	return SessionStatus{}
}

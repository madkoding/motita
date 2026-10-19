package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A repository the user has is never fetched into when a session opens: the branch they have out
// is theirs.
func TestOpeningASessionLeavesTheUsersRepositoryAlone(t *testing.T) {
	srv, _, id, _ := localProject(t)
	local, _ := usersRepo(t)
	mustGit(t, local, "stash", "-u", "-q")
	mustGit(t, local, "checkout", "-q", "main")
	before := snapshotOf(t, local)
	if code, p := addRepo(t, srv, id, `{"path":"`+local+`"}`); code != http.StatusCreated {
		t.Fatalf("adding it: %d %+v", code, p)
	}
	post(t, srv, "/v1/sessions", `{"project_id":"`+id+`"}`, testToken)
	srv.mainRefresh.wait()
	if after := snapshotOf(t, local); after != before {
		t.Errorf("the user's repository moved:\nbefore %s\nafter  %s", before, after)
	}
}

// The session's worktree branches from what origin has, not from the checkout as it was before the
// background update ran.
func TestANewSessionBranchesFromTheUpdatedMainBranch(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	pid, _ := projectWithOrigin(t, srv, ws)
	ss := createSessionIn(t, srv, pid)
	if !hasUpstream(convNamed(t, srv, ss.ID).workspaceDir()) {
		t.Fatal("the new session's worktree does not have what origin's main has")
	}
}

// A slow origin does not hold the session back for longer than the wait.
func TestANewSessionDoesNotWaitForeverForASlowUpdate(t *testing.T) {
	var m mainRefreshState
	if !m.begin("p") {
		t.Fatal("begin")
	}
	m.await("p", 20*time.Millisecond)
	m.await("none", time.Hour)
	m.finish("p", "")
	m.await("p", time.Hour)
}

// A project with no origin has nothing to pull, so a merged session continues.
func TestAContinueWithoutAnOriginHasNothingToPull(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	ss := createSessionIn(t, srv, pid)
	c, _ := srv.lookup(ss.ID)
	c.setMerged("abc123")
	w := callHandler(t, srv, c, srv.handleContinueSession, sessionPath(srv, ss.ID, "/continue"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
}

// Deleting a project that stands on a folder of the user's own removes what motita put in it.
func TestDeletingAProjectInTheUsersFolderReleasesItsRepositories(t *testing.T) {
	srv, _ := fullDiskServer(t)
	local, _ := usersRepo(t)
	mine := t.TempDir()
	gitRepo(t, mine)
	if code, out := createProject(t, srv, `{"title":"mine","path":"`+mine+`"}`); code != http.StatusCreated {
		t.Fatalf("creating: %d %v", code, out)
	}
	id := onlyProjectID(t, srv)
	if code, p := addRepo(t, srv, id, `{"path":"`+local+`"}`); code != http.StatusCreated {
		t.Fatalf("adding it: %d %+v", code, p)
	}
	srv.mainRefresh.wait()
	if w := doDelete(t, srv, "/v1/projects/"+id+"?force=1"); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if out := mustGit(t, local, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("a worktree of the deleted project is left in the user's repository:\n%s", out)
	}
	if out := mustGit(t, local, "branch", "--list", "project/*"); out != "" {
		t.Errorf("the project's branch is left: %s", out)
	}
	if _, err := os.Stat(filepath.Join(mine, "bridges")); err == nil {
		t.Error("the repository's folder is left inside the user's folder")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Errorf("the user's folder must stay: %v", err)
	}
}

// A session's branch goes with it when nothing on it is unmerged, and stays when it holds work.
func TestDeletingASessionDropsItsMergedBranchAndKeepsItsWork(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	dir := srv.projectOf(pid).Dir
	blank := createSessionIn(t, srv, pid)
	worked := createSessionIn(t, srv, pid)
	write(t, filepath.Join(worked.Workspace, "feature.txt"), "work")
	mustRun(t, "git", "-C", worked.Workspace, "add", "-A")
	mustRun(t, "git", "-C", worked.Workspace, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "work")

	for _, id := range []string{blank.ID, worked.ID} {
		if w := doDelete(t, srv, sessionPath(srv, id, "")); w.Code != http.StatusNoContent {
			t.Fatalf("delete %s: %d %s", id, w.Code, w.Body)
		}
	}
	srv.mainRefresh.wait()
	left := mustGit(t, dir, "branch", "--list", "motita/*")
	if strings.Contains(left, blank.ID) {
		t.Errorf("the blank session's branch is left: %s", left)
	}
	if !strings.Contains(left, worked.ID) {
		t.Errorf("a branch with unmerged commits must stay: %q", left)
	}
}

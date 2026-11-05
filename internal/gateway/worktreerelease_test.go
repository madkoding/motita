package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/gitx"
)

// A thread whose pull request was merged gives its worktree back at once: the session stays,
// read-only, says so, and continues isolated.
func TestAMergedThreadGivesItsWorktreeBack(t *testing.T) {
	ctx := context.Background()
	srv, _, ss := isolatedPRSession(t, "")
	c := srv.sessions[ss.ID]
	p := srv.projectOf(ss.ProjectID)
	commitFile(t, ss.Workspace, "work.txt", "w\n", "feat: the work")
	commitFile(t, p.Dir, "work.txt", "w\n", "feat: the work (#7)")

	srv.finishMerge(ctx, c, "", 7, "sha", "main")
	st := c.status()
	if !st.Merged || !st.WorktreeReleased || st.Worktree != "" || st.Workspace != p.Dir {
		t.Fatalf("a merged thread's worktree is released: %+v", st)
	}
	if _, err := os.Stat(ss.Workspace); !os.IsNotExist(err) {
		t.Error("the worktree is gone from disk")
	}
	if gitx.BranchExists(ctx, p.Dir, sessionBranch(ss.ID)) {
		t.Error("the branch goes with it (squash merged on the host)")
	}
	if c.svc.Config().Agent.WorkspaceDir != p.Dir {
		t.Error("the service runs in the project's folder now")
	}
	// Continuing still isolates.
	next, _, err := srv.continueFrom(ctx, c)
	if err != nil || gitx.SamePath(next.workspace, p.Dir) {
		t.Fatalf("a released thread continues in a worktree of its own: %v %q", err, next.workspace)
	}
	// Deleting it afterwards has nothing to give back and succeeds.
	if rec := del(t, srv, "/v1/sessions/"+ss.ID, testToken); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
	}
}

// The release survives a restart: the session comes back in the project's folder and the
// worktree is not recreated.
func TestAReleasedWorktreeStaysReleasedAfterARestart(t *testing.T) {
	ctx := context.Background()
	srv, _, ss := isolatedPRSession(t, "")
	c := srv.sessions[ss.ID]
	p := srv.projectOf(ss.ProjectID)
	commitFile(t, ss.Workspace, "work.txt", "w\n", "feat: the work")
	srv.finishMerge(ctx, c, "", 7, "sha", "main")
	if !c.status().WorktreeReleased {
		t.Fatal("released")
	}
	rec := recordOf(t, c)
	if !rec.WorktreeReleased {
		t.Fatal("the release is persisted")
	}
	srv2 := newTestServer(t, &fakeService{})
	withProjects(t, srv2, srv.opts.WorkspaceDir)
	srv2.projects = srv.projects
	if !srv2.restoreSession(rec) {
		t.Fatal("restore")
	}
	if got := srv2.sessions[ss.ID].status(); !got.WorktreeReleased || got.Workspace != p.Dir {
		t.Errorf("restored as released, in the project's folder: %+v", got)
	}
	if _, err := os.Stat(ss.Workspace); !os.IsNotExist(err) {
		t.Error("a restart does not recreate the released worktree")
	}
}

// recordOf is the session's persisted record, written and read back through a store of its own.
func recordOf(t *testing.T, c *conversation) sessionRecord {
	t.Helper()
	store, err := newSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.write(c, false); err != nil {
		t.Fatal(err)
	}
	recs, err := store.loadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.ID == c.id {
			return r
		}
	}
	t.Fatal("the session was not written")
	return sessionRecord{}
}

// What keeps a merged thread's worktree: uncommitted work, a run in flight, and a session that
// never had one.
func TestAMergedThreadKeepsAnUnsafeWorktree(t *testing.T) {
	ctx := context.Background()
	srv, _, ss := isolatedPRSession(t, "")
	c := srv.sessions[ss.ID]
	commitFile(t, ss.Workspace, "work.txt", "w\n", "feat: the work")
	if err := os.WriteFile(filepath.Join(ss.Workspace, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.finishMerge(ctx, c, "", 7, "sha", "main")
	if st := c.status(); !st.Merged || st.WorktreeReleased || st.Worktree == "" {
		t.Fatalf("uncommitted work keeps the worktree: %+v", st)
	}
	if err := os.Remove(filepath.Join(ss.Workspace, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	// A run in flight: the checkout is being written in.
	if !c.takeRunSlot() {
		t.Fatal("slot")
	}
	srv.releaseMergedWorktree(c)
	c.releaseRunSlot()
	if st := c.status(); st.WorktreeReleased || st.Worktree == "" {
		t.Fatalf("a run in flight keeps the worktree: %+v", st)
	}
	// Not merged: nothing to release.
	free := newConversation("s-x", &fakeService{})
	srv.releaseMergedWorktree(free)
	if free.wasIsolated() {
		t.Error("a session with no project was never isolated")
	}
	// Merged, but in the project's folder: nothing to release either.
	srv2, _, main := prSession(t, "")
	c2 := srv2.sessions[main.ID]
	c2.setMerged("sha")
	srv2.releaseMergedWorktree(c2)
	if st := c2.status(); st.WorktreeReleased {
		t.Errorf("a session in the project's folder has no worktree to release: %+v", st)
	}
}

// "Return to main branch" puts the project's folder back on its main branch by the user's word,
// carrying uncommitted changes when git can, and says why when it cannot.
func TestReturnProjectToMainBranch(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	if w := post(t, srv, "/v1/projects/nope/main/return", "", testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d", w.Code)
	}
	plain, _ := plainProject(t, srv, ws, "plain")
	if w := post(t, srv, "/v1/projects/"+plain+"/main/return", "", testToken); w.Code != http.StatusConflict {
		t.Errorf("a plain folder: %d", w.Code)
	}

	// Already on main: nothing to do.
	w := post(t, srv, "/v1/projects/"+pid+"/main/return", "", testToken)
	var out map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK || out["branch"] != "main" {
		t.Fatalf("on main: %d %s", w.Code, w.Body.String())
	}

	// On another branch with a change git can carry: back on main, change kept.
	mustRun(t, "git", "-C", p.Dir, "switch", "-qc", "motita/sX")
	if err := os.WriteFile(filepath.Join(p.Dir, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w = post(t, srv, "/v1/projects/"+pid+"/main/return", "", testToken); w.Code != http.StatusOK {
		t.Fatalf("return: %d %s", w.Code, w.Body.String())
	}
	if gitx.Display(ctx, p.Dir) != "main" {
		t.Error("the folder is back on main")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "new.txt")); err != nil {
		t.Error("the uncommitted change came along")
	}

	// A change git cannot carry (the file differs between the branches): refused, with git's reason.
	mustRun(t, "git", "-C", p.Dir, "switch", "-q", "motita/sX")
	commitFile(t, p.Dir, "f.txt", "theirs\n", "feat: change f")
	if err := os.WriteFile(filepath.Join(p.Dir, "f.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w = post(t, srv, "/v1/projects/"+pid+"/main/return", "", testToken)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "could not check out") {
		t.Errorf("a conflicting change: %d %s", w.Code, w.Body.String())
	}
	if gitx.Display(ctx, p.Dir) != "motita/sX" {
		t.Error("a refused return leaves the folder where it was")
	}

	// Back on main, but origin cannot be reached: the notice says so.
	mustRun(t, "git", "-C", p.Dir, "checkout", "-q", "f.txt")
	mustRun(t, "git", "-C", p.Dir, "remote", "add", "origin", "https://example.invalid/x.git")
	mustRun(t, "git", "-C", p.Dir, "config", "protocol.https.allow", "never")
	w = post(t, srv, "/v1/projects/"+pid+"/main/return", "", testToken)
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK ||
		!strings.HasPrefix(out["main_warning"], "main: ") {
		t.Errorf("offline: %d %s", w.Code, w.Body.String())
	}
	if gitx.Display(ctx, p.Dir) != "main" {
		t.Error("the folder is back on main even offline")
	}
}

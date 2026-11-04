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

// A thread's branch starts from the project's MAIN branch, whatever the project's folder has
// checked out. Measured before this: the main session proposed a pull request, which left the
// folder on motita/<main id>, and every thread opened after that was branched from it.
func TestAThreadStartsFromTheMainBranchNotFromTheFoldersCheckout(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	// The folder sits on a work branch with a commit main does not have.
	mustRun(t, "git", "-C", p.Dir, "switch", "-qc", "motita/sMAIN")
	commitFile(t, p.Dir, "wip.txt", "wip\n", "feat: unmerged work of the main session")

	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`","isolate":true}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ss.Workspace, "wip.txt")); err == nil {
		t.Fatal("the thread was branched from the folder's work branch, not from main")
	}
	if ss.BaseBranch != "main" {
		t.Errorf("base_branch = %q, want main", ss.BaseBranch)
	}
	if gitOut(t, ss.Workspace, "rev-parse", "HEAD") != gitOut(t, p.Dir, "rev-parse", "main") {
		t.Error("the thread's branch must start at main")
	}
	// Mergeable compares with MAIN too, not with the folder's branch.
	commitFile(t, ss.Workspace, "t.txt", "t\n", "feat: thread work")
	if st := srv.sessions[ss.ID].status(); !st.Mergeable || st.BaseBranch != "main" {
		t.Errorf("a thread ahead of main is mergeable: %+v", st)
	}
	if ahead := gitOut(t, p.Dir, "rev-list", "--count", "main.."+sessionBranch(ss.ID)); ahead != "1" {
		t.Errorf("commits ahead of main = %s, want 1", ahead)
	}
	_ = ctx
}

// The main branch a thread starts from is brought up to date with origin first, when that moves
// nothing but the ref; and a repository with no main branch at all starts from HEAD.
func TestAThreadStartsFromAnUpToDateMainBranch(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	repoWithOrigin(t, ws, filepath.Join(ws, "other")) // a recipe for an origin that is ahead
	// Give THIS project an origin that is one commit ahead on main.
	remote := filepath.Join(ws, "repo-origin.git")
	mustRun(t, "git", "clone", "--bare", "-q", p.Dir, remote)
	mustRun(t, "git", "-C", p.Dir, "remote", "add", "origin", remote)
	elsewhere := filepath.Join(ws, "repo-elsewhere")
	mustRun(t, "git", "clone", "-q", remote, elsewhere)
	mustRun(t, "git", "-C", elsewhere, "config", "user.email", "o@example.com")
	mustRun(t, "git", "-C", elsewhere, "config", "user.name", "O")
	commitFile(t, elsewhere, "upstream.txt", "up\n", "feat: upstream")
	mustRun(t, "git", "-C", elsewhere, "push", "-q", "origin", "main")
	// The folder is on another branch, so only the ref moves.
	mustRun(t, "git", "-C", p.Dir, "switch", "-qc", "feature")

	if start := srv.freshStart(ctx, p); start != "main" {
		t.Fatalf("freshStart = %q", start)
	}
	if gitOut(t, p.Dir, "rev-parse", "main") != gitOut(t, elsewhere, "rev-parse", "main") {
		t.Error("main must be brought up to date before a thread starts from it")
	}
	if gitx.Display(ctx, p.Dir) != "feature" {
		t.Error("the folder's own checkout is not switched")
	}

	// Offline, the branch is what it is: a warning, not a refusal.
	mustRun(t, "git", "-C", p.Dir, "remote", "set-url", "origin", "https://example.invalid/x.git")
	mustRun(t, "git", "-C", p.Dir, "config", "protocol.https.allow", "never")
	if start := srv.freshStart(ctx, p); start != "main" {
		t.Errorf("freshStart offline = %q, want main", start)
	}

	// No main branch: nothing to start from, and nothing to say.
	mustRun(t, "git", "-C", p.Dir, "branch", "-D", "main")
	if start := srv.freshStart(ctx, p); start != "" {
		t.Errorf("freshStart without main = %q", start)
	}
	if base := projectBase(ctx, p); base != "" {
		t.Errorf("projectBase without main = %q", base)
	}
	if base := projectBase(ctx, nil); base != "" {
		t.Errorf("projectBase(nil) = %q", base)
	}
	if base := projectBase(ctx, &Project{Dir: t.TempDir()}); base != "" {
		t.Errorf("projectBase of a plain folder = %q", base)
	}
	wt, err := srv.sessionWorktree(ctx, p.Dir, "s-nomain", "")
	if err != nil || gitx.Display(ctx, wt) != sessionBranch("s-nomain") {
		t.Errorf("a repository without main still gives a worktree from HEAD: %v %q", err, wt)
	}
}

// Changing the project's main branch moves the base of every session of it.
func TestChangingTheMainBranchMovesEverySessionsBase(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	mustRun(t, "git", "-C", p.Dir, "branch", "develop")
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	if ss.BaseBranch != "main" {
		t.Fatalf("base_branch = %q", ss.BaseBranch)
	}
	if rec := patchProject(t, srv, pid, `{"main_branch":"develop"}`); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	if st := srv.sessions[ss.ID].status(); st.BaseBranch != "develop" {
		t.Errorf("after the change base_branch = %q, want develop", st.BaseBranch)
	}
}

// The agent is told which branch is the project's and which is its own, in words that depend on
// where its checkout is.
func TestTheAgentIsToldItsBranches(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)
	if note := sessionBranchNote(ctx, dir, ""); note != "" {
		t.Errorf("no base, no note: %q", note)
	}
	if note := sessionBranchNote(ctx, t.TempDir(), "main"); note != "" {
		t.Errorf("not a repository, no note: %q", note)
	}
	note := sessionBranchNote(ctx, dir, "main")
	if !strings.Contains(note, `main branch is "main", and the checkout is on it`) ||
		!strings.Contains(note, "git switch -c") {
		t.Errorf("on main, the agent is told to branch first: %q", note)
	}
	mustRun(t, "git", "-C", dir, "switch", "-qc", "motita/s1")
	note = sessionBranchNote(ctx, dir, "main")
	if !strings.Contains(note, `this session's branch is "motita/s1"`) ||
		!strings.Contains(note, `pull request into "main"`) {
		t.Errorf("on its own branch, the agent is told to commit there: %q", note)
	}
	mustRun(t, "git", "-C", dir, "checkout", "-q", "--detach")
	if note = sessionBranchNote(ctx, dir, "main"); !strings.Contains(note, "not on a branch") {
		t.Errorf("detached: %q", note)
	}

	// A plain folder holding repositories: one line per repository, with ITS main branch.
	plain := t.TempDir()
	if note = sessionBranchNote(ctx, plain, ""); note != "" {
		t.Errorf("an empty folder has nothing to say: %q", note)
	}
	api := filepath.Join(plain, "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, api)
	mustRun(t, "git", "-C", api, "switch", "-qc", "motita/s2")
	web := filepath.Join(plain, "web")
	mustRun(t, "git", "init", "-q", "-b", "trunk", web) // no main branch: left out
	note = sessionBranchNote(ctx, plain, "")
	if !strings.Contains(note, "repository by repository") ||
		!strings.Contains(note, `- api: on "motita/s2", main branch "main"`) || strings.Contains(note, "web") {
		t.Errorf("a plain folder lists each repository with a main branch: %q", note)
	}

	// Through the runtime: a session in a project gets it with the rest of the note.
	svc := &noteService{}
	applyRuntimeTo(svc, &Project{Dir: dir}, dir)
	if !strings.Contains(svc.note, "Git in this session") {
		t.Errorf("the project note carries the branches: %q", svc.note)
	}
}

// After a pull request from the project's folder is merged, the folder goes back to the main
// branch, brought up to date, and the session's branch is gone even when the host squashed it.
func TestAMergedPullRequestReturnsTheProjectToItsMainBranch(t *testing.T) {
	ctx := context.Background()
	srv, _, ss := prSession(t, "")
	c := srv.sessions[ss.ID]
	p := srv.projectOf(ss.ProjectID)
	branch := sessionBranch(ss.ID)

	// The main session proposed its work: the folder is on its branch.
	if got, err := c.ensureProposalBranch(ctx, p.Dir); err != nil || got != branch {
		t.Fatalf("ensureProposalBranch: %q %v", got, err)
	}
	commitFile(t, p.Dir, "work.txt", "w\n", "feat: the work")
	// The host squash-merged it: main has the same lines in another commit.
	mustRun(t, "git", "-C", p.Dir, "checkout", "-q", "main")
	commitFile(t, p.Dir, "work.txt", "w\n", "feat: the work (#7)")
	mustRun(t, "git", "-C", p.Dir, "checkout", "-q", branch)

	srv.finishMerge(ctx, c, "", 7, "sha", "main")
	if gitx.Display(ctx, p.Dir) != "main" {
		t.Fatalf("the folder is on %q, want main", gitx.Display(ctx, p.Dir))
	}
	if gitx.BranchExists(ctx, p.Dir, branch) {
		t.Error("the merged session branch is deleted although `-d` would refuse the squash")
	}
	if c.isMerged() {
		t.Error("a session in the project's folder goes on after its pull request")
	}
}

// A pull request closed without a merge still returns the folder to the main branch, and the
// branch, whose work is nowhere else, stays.
func TestAClosedPullRequestReturnsTheProjectButKeepsTheBranch(t *testing.T) {
	ctx := context.Background()
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	p := srv.projectOf(ss.ProjectID)
	branch := sessionBranch(ss.ID)
	mustRun(t, "git", "-C", p.Dir, "switch", "-qc", branch)
	commitFile(t, p.Dir, "work.txt", "w\n", "feat: the work")

	prHost(host, branch, "success", "clean")
	w := &prWatchState{status: prFollowing}
	srv.pollPR(c, w)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7", `{"state":"closed","merged":false}`)
	if !srv.pollPR(c, w) || c.prWatchView().Status != prClosed {
		t.Fatalf("closed: %+v", c.prWatchView())
	}
	if gitx.Display(ctx, p.Dir) != "main" {
		t.Errorf("the folder is on %q, want main", gitx.Display(ctx, p.Dir))
	}
	if !gitx.BranchExists(ctx, p.Dir, branch) {
		t.Error("unmerged work keeps its branch")
	}
}

// What settleAfterPR leaves alone: a dirty checkout, a thread's own worktree, a session with no
// project, a repository with no main branch, a branch of the user's own that git says is not
// merged, and a checkout git cannot move.
func TestSettleAfterPRLeavesTheUnsafeAlone(t *testing.T) {
	ctx := context.Background()
	srv, _, ss := prSession(t, "")
	c := srv.sessions[ss.ID]
	p := srv.projectOf(ss.ProjectID)
	branch := sessionBranch(ss.ID)

	// Dirty: stays on the branch, with the changes.
	mustRun(t, "git", "-C", p.Dir, "switch", "-qc", branch)
	if err := os.WriteFile(filepath.Join(p.Dir, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.settleAfterPR(ctx, c, "", "main", true)
	if gitx.Display(ctx, p.Dir) != branch {
		t.Fatal("a dirty checkout is not moved")
	}
	if err := os.Remove(filepath.Join(p.Dir, "dirty.txt")); err != nil {
		t.Fatal(err)
	}

	// The user's own branch with unmerged work: the folder goes back, the branch stays.
	mustRun(t, "git", "-C", p.Dir, "switch", "-qc", "feat/theirs")
	commitFile(t, p.Dir, "theirs.txt", "t\n", "feat: theirs")
	srv.settleAfterPR(ctx, c, "", "main", true)
	if gitx.Display(ctx, p.Dir) != "main" || !gitx.BranchExists(ctx, p.Dir, "feat/theirs") {
		t.Error("the user's branch is only deleted when git agrees it is merged")
	}

	// Already on main: nothing to move, nothing to delete.
	srv.settleAfterPR(ctx, c, "", "main", true)
	if gitx.Display(ctx, p.Dir) != "main" {
		t.Error("on main, stays on main")
	}

	// git cannot move it: main is checked out in a worktree elsewhere.
	mustRun(t, "git", "-C", p.Dir, "switch", "-q", branch)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	mustRun(t, "git", "-C", p.Dir, "worktree", "add", "-q", elsewhere, "main")
	srv.settleAfterPR(ctx, c, "", "main", true)
	if gitx.Display(ctx, p.Dir) != branch {
		t.Error("a checkout git refuses to move is left where it is")
	}
	mustRun(t, "git", "-C", p.Dir, "worktree", "remove", "--force", elsewhere)

	// No main branch anywhere: nothing to go back to.
	mustRun(t, "git", "-C", p.Dir, "branch", "-D", "main")
	srv.settleAfterPR(ctx, c, "", "", true)
	if gitx.Display(ctx, p.Dir) != branch {
		t.Error("without a main branch the checkout stays")
	}

	// A thread's worktree is left on its branch: it is the integrated session from here on.
	srv2, _, thread := isolatedPRSession(t, "")
	c2 := srv2.sessions[thread.ID]
	commitFile(t, thread.Workspace, "t.txt", "t\n", "feat: thread")
	srv2.settleAfterPR(ctx, c2, "", "main", true)
	if gitx.Display(ctx, thread.Workspace) != sessionBranch(thread.ID) {
		t.Error("a thread's worktree keeps its branch")
	}

	// No project, or no repository: nothing to settle.
	free := newConversation("s-free", &fakeService{})
	srv.settleAfterPR(ctx, free, "", "main", true)
	c3 := newConversation("s-plain", &fakeService{})
	c3.setProjectID(ss.ProjectID, t.TempDir(), t.TempDir())
	srv.settleAfterPR(ctx, c3, "", "main", true)
}

// A project that is a plain folder holding repositories: the repository the pull request came
// from goes back to ITS main branch, and is brought up to date with its origin.
func TestSettleAfterPRReturnsARepositoryInsideAPlainFolder(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid, dir := plainProject(t, srv, ws, "proj")
	api := filepath.Join(dir, "api")
	repoWithOrigin(t, ws, api)
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	c := srv.sessions[ss.ID]
	mustRun(t, "git", "-C", api, "switch", "-qc", sessionBranch(ss.ID))
	commitFile(t, api, "work.txt", "w\n", "feat: the work")

	srv.settleAfterPR(ctx, c, "api", "", true)
	if gitx.Display(ctx, api) != "main" {
		t.Fatalf("api is on %q, want main", gitx.Display(ctx, api))
	}
	if _, err := os.Stat(filepath.Join(api, "upstream.txt")); err != nil {
		t.Error("the repository is brought up to date with its origin after the merge")
	}
	if gitx.BranchExists(ctx, api, sessionBranch(ss.ID)) {
		t.Error("the merged session branch is gone")
	}

	// A repository whose origin cannot be reached: back on main, with a warning.
	mustRun(t, "git", "-C", api, "switch", "-qc", "x")
	mustRun(t, "git", "-C", api, "remote", "set-url", "origin", "https://example.invalid/x.git")
	mustRun(t, "git", "-C", api, "config", "protocol.https.allow", "never")
	srv.settleAfterPR(ctx, c, "api", "", false)
	if gitx.Display(ctx, api) != "main" {
		t.Errorf("api is on %q, want main", gitx.Display(ctx, api))
	}
}

// Deleting a merged thread takes its branch even when the host squashed the merge; an unmerged
// one keeps it.
func TestDeletingAMergedThreadDropsItsSquashedBranch(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`","isolate":true}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	commitFile(t, ss.Workspace, "work.txt", "w\n", "feat: the work")
	commitFile(t, p.Dir, "work.txt", "w\n", "feat: the work (#7)")
	srv.sessions[ss.ID].setMerged("sha")

	if rec := del(t, srv, "/v1/sessions/"+ss.ID, testToken); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if gitx.BranchExists(ctx, p.Dir, sessionBranch(ss.ID)) {
		t.Error("a merged thread's branch goes with it, squash or not")
	}
}

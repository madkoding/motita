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

// newSharedSession creates a session in the project the default way, with no worktree of its own.
func newSharedSession(t *testing.T, srv *Server, projectID string) SessionStatus {
	t.Helper()
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+projectID+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var st SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestProjectSessionsShareTheProjectFolder: a project is a folder; its sessions work in it, as in
// Claude Code, with no worktree or branch made for them.
func TestProjectSessionsShareTheProjectFolder(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	dir := srv.projectOf(pid).Dir

	a, b := newSharedSession(t, srv, pid), newSharedSession(t, srv, pid)
	for _, st := range []SessionStatus{a, b} {
		if !gitx.SamePath(st.Workspace, dir) {
			t.Errorf("workspace = %q, want the project folder %q", st.Workspace, dir)
		}
		if st.Worktree != "" || st.Mergeable {
			t.Errorf("a session in the project folder has no worktree to integrate: %+v", st)
		}
	}
	if _, err := os.Stat(filepath.Join(srv.opts.WorkspaceDir, "worktrees", a.ID)); !os.IsNotExist(err) {
		t.Errorf("no worktree must be made: %v", err)
	}
	if gitx.BranchExists(context.Background(), dir, sessionBranch(a.ID)) {
		t.Error("no session branch must be made before a pull request asks for one")
	}
}

// TestASessionFindsTheRepositoriesOfItsFolder: a project folder holds any number of repositories,
// found by looking, and the one to propose is the one with work in it.
func TestASessionFindsTheRepositoriesOfItsFolder(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for _, name := range []string{"api", "web", ".hidden"} {
		gitInit(t, filepath.Join(dir, name))
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	repos := repoDirsIn(ctx, dir)
	if len(repos) != 2 || filepath.Base(repos[0]) != "api" || filepath.Base(repos[1]) != "web" {
		t.Fatalf("repos = %v, want api and web (not hidden folders or plain ones)", repos)
	}

	c := &conversation{id: "s1", workspace: dir}
	if got := c.sessionRepoDir(ctx, ""); got != repos[0] {
		t.Errorf("no work anywhere: the first repository, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(repos[1], "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := c.sessionRepoDir(ctx, ""); got != repos[1] {
		t.Errorf("the repository with changes, got %q", got)
	}
	if got := c.sessionRepoDir(ctx, "api"); got != repos[0] {
		t.Errorf("a named repository wins, got %q", got)
	}
	if got := c.sessionRepoDir(ctx, "nope"); got != "" {
		t.Errorf("an unknown name is no repository, got %q", got)
	}
	if got := c.changesAcrossRepos(ctx); got != 1 {
		t.Errorf("changes = %d, want 1 across the repositories", got)
	}
}

// TestTheProposalBranchLeavesTheDefaultBranch: work on main goes to a branch of the session's own;
// a repository already on a work branch proposes that one.
func TestTheProposalBranchLeavesTheDefaultBranch(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	gitInit(t, repo)
	c := &conversation{id: "s1", workspace: repo}

	if got := c.proposalBranch(ctx, repo); got != sessionBranch("s1") {
		t.Fatalf("on the default branch the session's own branch is used, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	branch, err := c.ensureProposalBranch(ctx, repo)
	if err != nil || gitx.Display(ctx, repo) != branch || branch != sessionBranch("s1") {
		t.Fatalf("branch = %q (%v), on %q", branch, err, gitx.Display(ctx, repo))
	}
	if n, _ := gitx.WorkingTreeChanges(ctx, repo); n != 1 {
		t.Errorf("uncommitted work must come along, changes = %d", n)
	}
	mustGit(t, repo, "switch", "-c", "feat/mine")
	if got := c.proposalBranch(ctx, repo); got != "feat/mine" {
		t.Errorf("a work branch is proposed as it is, got %q", got)
	}
	if got, err := c.ensureProposalBranch(ctx, repo); err != nil || got != "feat/mine" {
		t.Errorf("ensure must not move a work branch: %q %v", got, err)
	}
}

func TestSessionRepoHelpersOnFoldersWithoutRepositories(t *testing.T) {
	ctx := context.Background()
	if got := repoDirsIn(ctx, filepath.Join(t.TempDir(), "gone")); len(got) != 0 {
		t.Errorf("a folder that cannot be read holds no repository: %v", got)
	}
	c := &conversation{id: "s1", workspace: t.TempDir()}
	if got := c.sessionRepoDir(ctx, ""); got != "" {
		t.Errorf("no repository to propose, got %q", got)
	}
	// A session whose folder is not a worktree of the project is not isolated, and has no branch.
	other := &conversation{id: "s2", workspace: t.TempDir(), projectDir: t.TempDir()}
	if name := other.ownWorktreeName(ctx); name != "" {
		t.Errorf("a plain folder is no worktree: %q", name)
	}
	repo := t.TempDir()
	gitInit(t, repo)
	if (&conversation{id: "s3", workspace: repo}).branchAhead(ctx, "main") {
		t.Error("a session without its branch has nothing ahead")
	}
}

func TestTheRemoteOfARepositoryComesFromTheProjectFirst(t *testing.T) {
	srv, ws := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"Shop","dir":"shop"}`); code != http.StatusCreated {
		t.Fatal(code)
	}
	id := onlyProjectID(t, srv)
	p := srv.projectOf(id)
	web := filepath.Join(ws, "shop", "web")
	gitInit(t, web)
	p.Repos = []ProjectRepo{{Name: "web", GitURL: "https://example.com/web.git"}}
	if err := srv.projects.save(*p); err != nil {
		t.Fatal(err)
	}
	c := &conversation{id: "s", projectID: id, workspace: p.Dir}
	if got := srv.repoRemote(context.Background(), c, web); got != "https://example.com/web.git" {
		t.Errorf("a recorded repository answers with its URL, got %q", got)
	}
}

func TestCreatingAPRNeedsARepositoryAndWork(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	path := sessionPath(srv, ss.ID, "/pr")
	if w := postJSON(t, srv, path, testToken, `{bad`); w.Code != http.StatusBadRequest {
		t.Fatalf("a malformed body: %d %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, srv, path, testToken, `{"repo":"nope"}`); w.Code != http.StatusConflict {
		t.Fatalf("a repository the folder does not hold: %d %s", w.Code, w.Body.String())
	}
	// A branch named like the prefix of the session's branch makes the branch impossible to create.
	mustGit(t, ss.Workspace, "branch", "motita")
	if err := os.WriteFile(filepath.Join(ss.Workspace, "work.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := postJSON(t, srv, path, testToken, `{}`); w.Code != http.StatusConflict {
		t.Fatalf("a branch that cannot be made: %d %s", w.Code, w.Body.String())
	}
}

func TestCreatingAPRMovesTheWorkOffTheDefaultBranch(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	if err := os.WriteFile(filepath.Join(ss.Workspace, "work.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken, `{"repo":"."}`)
	if w.Code != http.StatusOK {
		t.Fatalf("the pull request run starts: %d %s", w.Code, w.Body.String())
	}
	if got := gitx.Display(context.Background(), ss.Workspace); got != sessionBranch(ss.ID) {
		t.Errorf("the work must be on the session's branch, got %q", got)
	}
}

func TestAPRInAFolderWithoutARepositoryHasNothingToPropose(t *testing.T) {
	srv, _ := gitServer(t)
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	dir := filepath.Join(ws, "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := srv.projects.save(Project{ID: "p-plain", Title: "plain", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	ss := newSharedSession(t, srv, "p-plain")
	connectGitHub(t, srv)
	if w := get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken); w.Code != http.StatusConflict {
		t.Fatalf("no repository: %d %s", w.Code, w.Body.String())
	}
}

func TestARestoredIsolatedSessionThatCannotGetItsWorktreeRunsInTheProject(t *testing.T) {
	ws := t.TempDir()
	pdir := filepath.Join(ws, "plain")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	srv, _ := startServer(t, Options{Token: testToken, WorkspaceDir: ws,
		SessionDir: t.TempDir(), ProjectDir: t.TempDir(),
		NewService: func() (Service, error) { return &fakeService{}, nil }})
	rec := sessionRecord{ID: "s-iso", ProjectID: "p", ProjectDir: pdir, Workspace: filepath.Join(ws, "worktrees", "s-iso")}
	if got := srv.restoredWorkspace(rec); got != pdir {
		t.Errorf("a worktree that cannot be re-created falls back to the project folder, got %q", got)
	}
}

func TestCreatingAPRCountsCommitsAheadOfTheBaseAsWork(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	mustGit(t, ss.Workspace, "switch", "-c", sessionBranch(ss.ID))
	mustGit(t, ss.Workspace, "commit", "--allow-empty", "-qm", "feat: committed work")
	w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("a committed branch with a clean tree is work: %d %s", w.Code, w.Body.String())
	}
}

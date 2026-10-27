package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// A plain folder's repositories count for the remote watch and the pull request list like any
// other, and the folder itself, which is no repository, is not a problem to report.
func TestAPlainFoldersRepositoriesAreWatchedAndListed(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	id, dir := plainProject(t, srv, ws, "services")
	repoWithOrigin(t, ws, filepath.Join(dir, "api"))
	repoWithOrigin(t, ws, filepath.Join(dir, "group", "web"))
	p := srv.projectOf(id)

	targets := remoteTargets(context.Background(), p)
	if len(targets) != 2 || targets[0].repo != "api" || targets[1].repo != "group/web" {
		t.Fatalf("remote targets = %+v, want api and group/web", targets)
	}
	repos := prReposOf(context.Background(), *p)
	if len(repos) != 2 || repos[0].name != "api" || repos[1].name != "group/web" {
		t.Errorf("pull request repositories = %+v, want api and group/web, not the folder", repos)
	}
}

// A recorded repository whose worktree sits in the folder is not found a second time.
func TestAFoundRepositoryIsNotCountedTwice(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	id, dir := plainProject(t, srv, ws, "services")
	gitInit(t, filepath.Join(dir, "api"))
	gitInit(t, filepath.Join(dir, "web"))
	p := srv.projectOf(id)
	p.Repos = []ProjectRepo{{Name: "api", Dir: filepath.Join(dir, "api")}}
	got := foundRepos(context.Background(), p)
	if len(got) != 1 || got[0].rel != "web" {
		t.Errorf("found = %+v, want only web", got)
	}
}

// A repository inside a plain folder is a repository of the project: the branches of sessions
// that are gone are cleared from it too.
func TestSessionBranchesAreClearedFromAPlainFoldersRepositories(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	_, dir := plainProject(t, srv, ws, "services")
	api := filepath.Join(dir, "api")
	gitInit(t, api)
	branch := sessionBranch("s0123456789abcdef01234567")
	mustGit(t, api, "branch", branch)

	srv.pruneSessionBranches(map[string]bool{})
	srv.mainRefresh.wait()
	if out := mustGit(t, api, "branch", "--list", branch); out != "" {
		t.Errorf("the branch of a session that is gone stayed: %q", out)
	}
}

// A session's worktree of a repository answers with the URL the project recorded, as the
// project's own checkout of it does: the origin an agent can rewrite is not the answer.
func TestTheRecordedRemoteAlsoCoversTheSessionsOwnWorktree(t *testing.T) {
	srv, ws := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"Shop","dir":"shop"}`); code != http.StatusCreated {
		t.Fatal(code)
	}
	id := onlyProjectID(t, srv)
	p := srv.projectOf(id)
	session := filepath.Join(ws, "worktrees", "s1")
	web := filepath.Join(session, "web")
	if err := os.MkdirAll(web, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, web)
	mustGit(t, web, "remote", "add", "origin", "https://example.com/rewritten.git")
	p.Repos = []ProjectRepo{{Name: "web", GitURL: "https://example.com/web.git"}}
	if err := srv.projects.save(*p); err != nil {
		t.Fatal(err)
	}
	c := &conversation{id: "s1", projectID: id, workspace: session, projectDir: p.Dir}
	if got := srv.repoRemote(context.Background(), c, web); got != "https://example.com/web.git" {
		t.Errorf("the recorded URL must win over the worktree's origin, got %q", got)
	}
}

// Two repositories in different groups can share a folder name: each is asked for by its path.
func TestRepositoriesWithTheSameFolderNameAreToldApart(t *testing.T) {
	ws := t.TempDir()
	a, b := filepath.Join(ws, "web", "api"), filepath.Join(ws, "mobile", "api")
	c := &conversation{id: "s", workspace: ws}
	repos := []string{a, b}
	if got := c.namedRepo(repos, "mobile/api"); got != b {
		t.Errorf("mobile/api = %q, want %q", got, b)
	}
	if got := c.namedRepo(repos, "web/api"); got != a {
		t.Errorf("web/api = %q, want %q", got, a)
	}
	if got := c.namedRepo(repos, "api"); got != "" {
		t.Errorf("a bare name two repositories have is no answer, got %q", got)
	}
	if got := c.namedRepo([]string{a}, "api"); got != a {
		t.Errorf("a bare name only one has still works, got %q", got)
	}
	if got := c.repoName(ws); got != "." {
		t.Errorf("the folder itself is '.', got %q", got)
	}
}

// A dot inside a folder name is allowed; a path, a hidden folder or ".." is not.
func TestAProjectFolderNameMayHaveADot(t *testing.T) {
	srv, _ := projectServer(t)
	for dir, want := range map[string]int{
		"my.app": http.StatusCreated, ".ssh": http.StatusBadRequest, "a..b": http.StatusBadRequest,
		"a/b": http.StatusBadRequest, "app.": http.StatusBadRequest,
	} {
		if code, _ := createProject(t, srv, `{"title":"T","dir":"`+dir+`"}`); code != want {
			t.Errorf("dir %q: status %d, want %d", dir, code, want)
		}
	}
}

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

// Each repository's pull request has a watch of its own: merging one leaves the session open while
// another is still open, and the session is integrated once the last one is merged.
func TestEachRepositorysPullRequestIsFollowedOnItsOwn(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	c := newConversation("s-multi", &fakeService{})
	c.setProjectID("", filepath.Join(t.TempDir(), "worktree"), t.TempDir())
	c.setPRWatch(&PRWatchView{Status: prFollowing, Number: 7, Repo: "."})
	ctx := context.Background()

	srv.prMu.Lock()
	extra := srv.firstWatchElsewhere(c, "web")
	srv.prMu.Unlock()
	if !extra {
		t.Fatal("a second repository's watch is an extra one while the first PR is open")
	}
	srv.finishMerge(ctx, c, "web", 8, "sha-web", "")
	if c.isMerged() {
		t.Error("the session is not integrated while the folder's own pull request is open")
	}
	if v := c.repoWatchView("web"); v == nil || v.Status != prMerged || v.Number != 8 {
		t.Errorf("web's watch = %+v, want merged #8", v)
	}
	if v := c.prWatchView(); v == nil || v.Status != prFollowing || v.Number != 7 {
		t.Errorf("the first watch was overwritten: %+v", v)
	}
	srv.finishMerge(ctx, c, ".", 7, "sha-root", "")
	if !c.isMerged() {
		t.Error("with every pull request merged the session is integrated")
	}
}

// A folder git sees inside a work tree is a repository of its own (a package of a monorepo), unless
// it holds repositories: then it is a container that only happens to sit inside another one.
func TestAContainerInsideAnotherRepositoryIsStillAContainer(t *testing.T) {
	ctx := context.Background()
	outer := t.TempDir()
	gitInit(t, outer)
	pkg := filepath.Join(outer, "packages", "app")
	box := filepath.Join(outer, "work")
	for _, d := range []string{pkg, box} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitInit(t, filepath.Join(box, "api"))
	if !projectIsRepo(ctx, outer) || !projectIsRepo(ctx, pkg) {
		t.Error("a repository and a package inside one are repositories")
	}
	if projectIsRepo(ctx, box) {
		t.Error("a folder holding repositories is a container, wherever it sits")
	}
	if projectIsRepo(ctx, t.TempDir()) {
		t.Error("a plain folder is no repository")
	}
}

// The background update brings the repositories of a plain folder up to date too.
func TestTheRepositoriesOfAPlainFolderAreUpdated(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	id, dir := plainProject(t, srv, ws, "services")
	api := filepath.Join(dir, "api")
	repoWithOrigin(t, ws, api)
	srv.refreshMain(context.Background(), srv.projectOf(id), false)
	if _, err := os.Stat(filepath.Join(api, "upstream.txt")); err != nil {
		t.Errorf("api was not brought up to date: %v", err)
	}
}

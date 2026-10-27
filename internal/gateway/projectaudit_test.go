package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// The watches of a session's other repositories are saved with it and followed again after a
// restart, with the push whose failure was already handed to the agent.
func TestExtraWatchesSurviveARestart(t *testing.T) {
	srv, _, ss := prSession(t, "https://github.com/o/r.git", func(o *Options) {
		o.PRPollEvery = time.Hour
		o.SessionDir = t.TempDir()
	})
	c := srv.sessions[ss.ID]
	c.setPRWatch(&PRWatchView{Status: prFollowing, Number: 7, Repo: "."})
	srv.publishPR(c, &prWatchState{repo: "web", extra: true, status: prFixing, attempts: 1, fixedKey: "k1"}, 8)

	recs, err := srv.store.loadAll()
	if err != nil {
		t.Fatal(err)
	}
	var saved []prWatchRecord
	for _, r := range recs {
		if r.ID == ss.ID {
			saved = r.PRRepoWatches
		}
	}
	if len(saved) != 1 || saved[0].Repo != "web" || saved[0].Number != 8 || saved[0].FixedKey != "k1" {
		t.Fatalf("saved extra watches = %+v", saved)
	}

	restored := newConversation("again", &fakeService{})
	restored.restoreRepoWatches(saved, 3)
	if v := restored.repoWatchView("web"); v == nil || v.Status != prFixing || v.fixedKey != "k1" || v.Max != 3 {
		t.Errorf("restored view = %+v", v)
	}
	srv.resumeRepoWatches(sessionRecord{ID: ss.ID, PRRepoWatches: saved})
	srv.prMu.Lock()
	watching := srv.prWatching[ss.ID+"\x00web"]
	srv.prMu.Unlock()
	if !watching {
		t.Error("web's pull request is followed again after the restart")
	}
}

// A repository whose CI is seen running in the bar's list starts being followed by itself.
func TestARunningCIInTheRepositoryListIsFollowed(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git", func(o *Options) { o.PRPollEvery = time.Hour })
	connectGitHub(t, srv)
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs",
		`{"check_runs":[{"id":1,"name":"test","status":"in_progress"}]}`)
	writeIn(t, ss.Workspace, "root.txt")
	web := filepath.Join(ss.Workspace, "web")
	gitInit(t, web)
	writeIn(t, web, "new.txt")
	get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken)
	srv.prMu.Lock()
	watching := srv.prWatching[ss.ID+"\x00."]
	srv.prMu.Unlock()
	if !watching {
		t.Error("the folder's running CI is followed")
	}
}

// The bookkeeping of the extra watches: removing one, telling when one is over, an open one keeping
// the session from being integrated, and a record without a repository being skipped.
func TestTheExtraWatchesBookkeeping(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	c := newConversation("s-book", &fakeService{})
	w := &prWatchState{repo: "web", extra: true}
	c.setWatchViewOf(w, &PRWatchView{Status: prFollowing, Repo: "web"})
	if c.watchDone(w) {
		t.Error("a followed pull request is not over")
	}
	if !c.otherPRsOpen(".") {
		t.Error("web's open pull request is another one open")
	}
	srv.rememberTalk(c, w)
	c.setWatchViewOf(w, &PRWatchView{Status: prMerged, Repo: "web"})
	if !c.watchDone(w) {
		t.Error("a merged pull request is over")
	}
	c.setWatchViewOf(w, nil)
	if c.repoWatchView("web") != nil {
		t.Error("a removed watch has no view")
	}
	c.restoreRepoWatches([]prWatchRecord{{Repo: "", Status: prFollowing}}, 3)
	if len(c.prRepoWatches) != 0 {
		t.Errorf("a record without a repository is skipped: %v", c.prRepoWatches)
	}
	srv.prMu.Lock()
	srv.prWatching = map[string]bool{"other\x00.": true}
	any := srv.watchingAny("s-book")
	srv.prMu.Unlock()
	if any {
		t.Error("another session's watch is not this one's")
	}
	outside := &conversation{workspace: t.TempDir()}
	if got := outside.repoName(filepath.Join(t.TempDir(), "elsewhere")); got != "elsewhere" {
		t.Errorf("a folder outside the session's is named by its folder, got %q", got)
	}
}

// A folder another clone is being made in is never offered.
func TestAFolderBeingClonedIntoIsNotOffered(t *testing.T) {
	base := filepath.Join(t.TempDir(), "app")
	got := freeCloneDir(base, "https://example.com/a/app", nil, func(d string) bool { return d == base })
	if got != base+"-2" {
		t.Errorf("folder = %q, want %q", got, base+"-2")
	}
}

// A repository of a plain folder whose origin cannot be reached is logged and left as it is.
func TestARepositoryThatCannotBeUpdatedIsLeftAsItIs(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	id, dir := plainProject(t, srv, ws, "services")
	api := filepath.Join(dir, "api")
	gitInit(t, api)
	mustGit(t, api, "remote", "add", "origin", filepath.Join(ws, "missing.git"))
	srv.refreshFoundRepos(context.Background(), srv.projectOf(id), true)
	if out := mustGit(t, api, "rev-parse", "--abbrev-ref", "HEAD"); out != "main" {
		t.Errorf("api moved: %q", out)
	}
}

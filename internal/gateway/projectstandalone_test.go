package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/gitx"
)

func standalonePath(id, name string) string {
	return "/v1/projects/" + id + "/repos/" + name + "/standalone"
}

// sharedRepoProject is a project with a shared repository "web-client" in its folder.
func sharedRepoProject(t *testing.T) (*Server, string, string) {
	t.Helper()
	srv, projectDir, sessionID := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if w := doDelete(t, srv, "/v1/sessions/"+sessionID); w.Code != http.StatusNoContent {
		t.Fatalf("removing the session: %d %s", w.Code, w.Body.String())
	}
	if code, _ := addRepo(t, srv, id, `{"shared":true,"git_url":"`+sourceRepo(t)+`"}`); code != http.StatusCreated {
		t.Fatalf("adding the shared repository: %d", code)
	}
	return srv, id, filepath.Join(projectDir, "web-client")
}

func TestASharedRepositoryBecomesAPlainCloneKeepingItsWork(t *testing.T) {
	srv, id, dir := sharedRepoProject(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "mine.txt")
	mustGit(t, dir, "commit", "-qm", "feat: committed in the project branch")
	branch := gitx.Display(ctx, dir)
	url := srv.projectOf(id).Repos[0].GitURL
	newSharedSession(t, srv, id) // a session in the folder is told about the change

	w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("migrating: %d %s", w.Code, w.Body.String())
	}
	repo := srv.projectOf(id).Repos[0]
	if repo.Shared || repo.Dir != dir || repo.ProjectBranch != "" {
		t.Errorf("the repository is a plain clone of the folder now: %+v", repo)
	}
	if got := gitx.Display(ctx, dir); got != branch {
		t.Errorf("same branch: %q, want %q", got, branch)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.txt")); err != nil {
		t.Errorf("the commit made in the shared folder is kept: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !info.IsDir() {
		t.Errorf("the folder has a repository of its own, not a worktree link: %v", err)
	}
	if got := mustGit(t, dir, "remote", "get-url", "origin"); got != url {
		t.Errorf("origin = %q, want the repository's URL %q", got, url)
	}
	if _, err := os.Stat(srv.projectOf(id).Repos[0].Dir); err != nil {
		t.Errorf("the folder exists: %v", err)
	}
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusNotFound {
		t.Errorf("a plain clone has nothing to migrate: %d", w.Code)
	}
}

func TestMigratingARepositoryThatCannotBeMigrated(t *testing.T) {
	srv, id, dir := sharedRepoProject(t)
	if w := post(t, srv, standalonePath("nope", "web-client"), "{}", testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d", w.Code)
	}
	if w := post(t, srv, standalonePath(id, "nope"), "{}", testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown repository: %d", w.Code)
	}

	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("uncommitted work: %d %s", w.Code, w.Body.String())
	}
	if err := os.Remove(filepath.Join(dir, "wip.txt")); err != nil {
		t.Fatal(err)
	}

	mustGit(t, dir, "checkout", "-q", "--detach")
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("a detached folder: %d %s", w.Code, w.Body.String())
	}
	mustGit(t, dir, "checkout", "-q", "-")

	if err := os.MkdirAll(dir+".standalone", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir+".standalone", "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("a clone that cannot be made: %d %s", w.Code, w.Body.String())
	}
	if err := os.RemoveAll(dir + ".standalone"); err != nil {
		t.Fatal(err)
	}
	if !srv.projectOf(id).Repos[0].Shared {
		t.Error("a refused migration changes nothing")
	}
}

func TestMigratingNeedsTheSessionsWorktreesGone(t *testing.T) {
	srv, id, _ := sharedRepoProject(t)
	post(t, srv, "/v1/sessions", `{"project_id":"`+id+`","isolate":true}`, testToken)
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("an isolated session still has a worktree of it: %d %s", w.Code, w.Body.String())
	}
}

func TestMigrationFailuresLeaveTheSharedFolderInPlace(t *testing.T) {
	srv, id, dir := sharedRepoProject(t)
	ctx := context.Background()
	p := srv.projectOf(id)
	setURL := func(url string) {
		t.Helper()
		p.Repos[0].GitURL = url
		if err := srv.projects.save(*p); err != nil {
			t.Fatal(err)
		}
	}
	good := p.Repos[0].GitURL
	setURL("")
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("no URL to point origin at: %d %s", w.Code, w.Body.String())
	}
	// A URL git cannot be given as an argument: the clone cannot be prepared.
	setURL("bad\x00url")
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("a clone that cannot be prepared: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(dir + ".standalone"); !os.IsNotExist(err) {
		t.Errorf("the half-made clone is cleaned up: %v", err)
	}
	setURL(good)

	restoreRemove := worktreeRemove
	worktreeRemove = func(context.Context, string, string, bool) error { return errors.New("git said no") }
	t.Cleanup(func() { worktreeRemove = restoreRemove })
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("a worktree git will not remove: %d %s", w.Code, w.Body.String())
	}
	worktreeRemove = restoreRemove

	restoreRename := renamePath
	renamePath = func(string, string) error { return errors.New("no rename") }
	t.Cleanup(func() { renamePath = restoreRename })
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("a clone that cannot take the place: %d %s", w.Code, w.Body.String())
	}
	renamePath = restoreRename
	if _, ok, err := gitx.LiveWorktreeAt(ctx, srv.projectOf(id).Repos[0].Dir, dir); err != nil || !ok {
		t.Errorf("the worktree is put back when the swap fails: %v %v", ok, err)
	}
}

func TestMigrationSavedAgainstAProjectThatChangedMeanwhile(t *testing.T) {
	srv, id, _ := sharedRepoProject(t)
	restore := beforeProjectUpdate
	t.Cleanup(func() { beforeProjectUpdate = restore })
	// The repository was taken out of the project while the clone was made: nothing is recorded.
	beforeProjectUpdate = func(string) {
		if _, err := srv.projects.update(id, func(cur *Project) { cur.Repos = nil }); err != nil {
			t.Error(err)
		}
	}
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusOK {
		t.Fatalf("migrating: %d %s", w.Code, w.Body.String())
	}
	if len(srv.projectOf(id).Repos) != 0 {
		t.Error("a repository that is gone is not brought back")
	}
}

func TestMigrationOfAProjectDeletedMeanwhile(t *testing.T) {
	srv, id, _ := sharedRepoProject(t)
	restore := beforeProjectUpdate
	t.Cleanup(func() { beforeProjectUpdate = restore })
	beforeProjectUpdate = func(pid string) { _ = srv.projects.delete(pid) }
	if w := post(t, srv, standalonePath(id, "web-client"), "{}", testToken); w.Code != http.StatusInternalServerError {
		t.Fatalf("a project that is gone: %d %s", w.Code, w.Body.String())
	}
}

// On start, every shared folder that can move becomes a plain clone, and one that cannot yet
// (uncommitted work) stays shared, with the project's other repositories untouched.
func TestStartingMigratesTheSharedRepositoriesThatCan(t *testing.T) {
	srv, id, dir := sharedRepoProject(t)
	newSharedSession(t, srv, id) // a live session is told about the change
	srv.migrateSharedRepos(context.Background())
	if repo := srv.projectOf(id).Repos[0]; repo.Shared {
		t.Fatalf("a clean shared repository migrates on start: %+v", repo)
	}
	srv.migrateSharedRepos(context.Background()) // a plain clone is left alone

	srv2, id2, dir2 := sharedRepoProject(t)
	if err := os.WriteFile(filepath.Join(dir2, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv2.migrateSharedRepos(context.Background())
	if repo := srv2.projectOf(id2).Repos[0]; !repo.Shared {
		t.Errorf("a folder with uncommitted work stays shared: %+v", repo)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("the migrated folder is intact: %v", err)
	}
}

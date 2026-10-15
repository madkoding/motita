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

// The catalog: a repository is cloned once in the workspace, and each project that uses it has a
// worktree of that clone on a branch of its own.

// catalogList is what GET /v1/repos answers.
func catalogList(t *testing.T, srv *Server) []catalogRepo {
	t.Helper()
	w := get(t, srv, "/v1/repos", testToken)
	var out struct {
		Repos []catalogRepo `json:"repos"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("the catalog: %d %s", w.Code, w.Body)
	}
	return out.Repos
}

// newProjectID creates one more project, in the folder dir, and answers its id.
func newProjectID(t *testing.T, srv *Server, dir string) string {
	t.Helper()
	code, out := createProject(t, srv, `{"title":"`+dir+`","dir":"`+dir+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("creating %s: %d %v", dir, code, out)
	}
	return out["id"].(string)
}

func TestTwoProjectsShareOneCloneOfARepository(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	first := onlyProjectID(t, srv)
	second := newProjectID(t, srv, "other")
	src := sourceRepo(t)
	if code, _ := addRepo(t, srv, first, `{"git_url":"`+src+`"}`); code != http.StatusCreated {
		t.Fatalf("adding to the first: %d", code)
	}
	code, p := addRepo(t, srv, second, `{"git_url":"`+src+`","name":"client"}`)
	clone := filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "web-client")
	if code != http.StatusCreated || p.Repos[0].Dir != clone || !p.Repos[0].Shared {
		t.Fatalf("the second project must reuse the clone: %d %+v", code, p.Repos)
	}
	ctx := context.Background()
	otherDir := filepath.Join(srv.opts.WorkspaceDir, "other", "client")
	if got := gitx.Display(ctx, otherDir); got != "project/other" {
		t.Errorf("each project has a branch of its own, the second is on %q", got)
	}
	// A commit in one project is in the repository the other one uses.
	mine := filepath.Join(projectDir, "web-client")
	if err := os.WriteFile(filepath.Join(mine, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, mine, "add", "-A")
	mustGit(t, mine, "-c", "user.name=t", "-c", "user.email=t@x", "commit", "-qm", "a")
	mustGit(t, otherDir, "log", "-1", "project/proj")

	list := catalogList(t, srv)
	if len(list) != 1 || list[0].Dir != clone || list[0].GitURL != src || len(list[0].Projects) != 2 {
		t.Fatalf("the catalog lists the clone and both projects: %+v", list)
	}

	// Taken out of the second project, the clone stays for the first; the second's branch, with
	// nothing unmerged on it, goes.
	w := send(t, srv, http.MethodDelete, "/v1/projects/"+second+"/repos/client", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("removing it from the second: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(otherDir); !os.IsNotExist(err) {
		t.Errorf("the second project's folder of it must be gone: %v", err)
	}
	if gitx.BranchExists(ctx, clone, "project/other") || gitx.Repo(ctx, clone) != nil {
		t.Errorf("the clone stays, without the second project's branch")
	}
	if list := catalogList(t, srv); len(list) != 1 || len(list[0].Projects) != 1 {
		t.Errorf("the catalog keeps the clone, used by one project: %+v", list)
	}

	// The first project deleted, its worktree is cleared from the clone; its branch holds a
	// commit that is merged nowhere, so it stays.
	if w := send(t, srv, http.MethodDelete, "/v1/projects/"+first+"?force=1", testToken, ""); w.Code >= 300 {
		t.Fatalf("deleting the first project: %d %s", w.Code, w.Body)
	}
	if wts, _ := gitx.Worktrees(ctx, clone); len(wts) != 1 {
		t.Errorf("only the clone's own checkout may be left: %+v", wts)
	}
	if !gitx.BranchExists(ctx, clone, "project/proj") {
		t.Error("a branch with unmerged work is kept")
	}
}

func TestAnEmptyRepositoryIsClonedIntoTheProject(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	empty := filepath.Join(t.TempDir(), "blank")
	mustGit(t, t.TempDir(), "init", "-q", "--bare", empty)

	code, p := addRepo(t, srv, id, `{"git_url":"`+empty+`"}`)
	dest := filepath.Join(projectDir, "blank")
	if code != http.StatusCreated || p.Repos[0].Dir != dest || p.Repos[0].Shared {
		t.Fatalf("an empty repository has nothing to branch from, so the project takes the clone: %d %+v",
			code, p.Repos)
	}
	if _, err := os.Stat(filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "blank")); !os.IsNotExist(err) {
		t.Errorf("the catalog keeps no clone of it: %v", err)
	}

	// One the catalog already holds empty is cloned again for the project.
	again := filepath.Join(t.TempDir(), "again")
	mustGit(t, t.TempDir(), "init", "-q", "--bare", again)
	mustGit(t, t.TempDir(), "clone", "-q", again, filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "again"))
	if code, p := addRepo(t, srv, id, `{"git_url":"`+again+`"}`); code != http.StatusCreated ||
		p.Repos[1].Dir != filepath.Join(projectDir, "again") {
		t.Fatalf("an empty clone in the catalog: %d %+v", code, p.Repos)
	}
	// A clone of its own goes with the repository.
	if w := send(t, srv, http.MethodDelete, "/v1/projects/"+id+"/repos/blank", testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("removing it: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("the project's own clone must be gone: %v", err)
	}
}

func TestWithoutACatalogTheProjectClonesItsOwn(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	// A project of its own in the catalog's folder, from before the name was kept for it.
	old := Project{ID: "old", Title: "repos", Dir: filepath.Join(srv.opts.WorkspaceDir, catalogFolder)}
	if err := srv.projects.save(old); err != nil {
		t.Fatal(err)
	}
	code, p := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`"}`)
	if code != http.StatusCreated || p.Repos[0].Dir != filepath.Join(projectDir, "web-client") || p.Repos[0].Shared {
		t.Fatalf("no catalog, a clone of its own: %d %+v", code, p.Repos)
	}
	if list := catalogList(t, srv); len(list) != 0 {
		t.Errorf("no catalog lists nothing: %+v", list)
	}
	if (&Server{}).catalogDir() != "" {
		t.Error("no workspace, no catalog")
	}
}

func TestAProjectBranchInUseStopsTheWorktree(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	src := sourceRepo(t)
	clone := filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "web-client")
	mustGit(t, t.TempDir(), "clone", "-q", src, clone)
	mustGit(t, clone, "worktree", "add", "-q", "-b", "project/proj", filepath.Join(t.TempDir(), "elsewhere"))
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+src+`"}`); code < 400 {
		t.Errorf("a branch checked out elsewhere: %d", code)
	}
}

func TestTheCatalogSkipsWhatIsNotAClone(t *testing.T) {
	srv, _ := projectServer(t)
	root := filepath.Join(srv.opts.WorkspaceDir, catalogFolder)
	if err := os.MkdirAll(filepath.Join(root, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if list := catalogList(t, srv); len(list) != 0 {
		t.Errorf("a folder with no origin and a file are not clones: %+v", list)
	}
}

func TestTheCatalogFolderCannotBeAProject(t *testing.T) {
	srv, _ := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"r","dir":"repos"}`); code != http.StatusBadRequest {
		t.Errorf("the catalog's folder is kept: %d", code)
	}
}

func TestAnUnusedRepositoryLeavesTheCatalog(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	clone := filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "web-client")
	del := func(path string) int {
		return send(t, srv, http.MethodDelete, path, testToken, "").Code
	}
	for _, name := range []string{"missing", "a..b", ".hidden"} {
		if code := del("/v1/repos/" + name); code != http.StatusNotFound {
			t.Errorf("%q: %d", name, code)
		}
	}
	if code := del("/v1/repos/web-client"); code != http.StatusConflict {
		t.Fatalf("a repository a project uses stays: %d", code)
	}
	if code := del("/v1/projects/" + id + "/repos/web-client"); code != http.StatusOK {
		t.Fatalf("taking it out of the project: %d", code)
	}
	// Uncommitted work stops it, until the user confirms.
	if err := os.WriteFile(filepath.Join(clone, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := del("/v1/repos/web-client"); code != http.StatusConflict {
		t.Fatalf("uncommitted work must stop it: %d", code)
	}
	old := removeAll
	removeAll = func(string) error { return os.ErrPermission }
	if code := del("/v1/repos/web-client?force=1"); code != http.StatusInternalServerError {
		t.Errorf("a removal that fails: %d", code)
	}
	removeAll = old
	if code := del("/v1/repos/web-client?force=1"); code != http.StatusNoContent {
		t.Fatalf("a confirmed deletion: %d", code)
	}
	if pathExists(clone) || len(catalogList(t, srv)) != 0 {
		t.Error("the clone must be gone")
	}
	if code := del("/v1/repos/x"); code != http.StatusNotFound {
		t.Errorf("after: %d", code)
	}
	if (&Server{}).allProjects() != nil {
		t.Error("no store, no projects")
	}
}

func TestAnIntegrationBringsTheProjectFoldersUpToDate(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	first := onlyProjectID(t, srv)
	second := newProjectID(t, srv, "other")
	src := sourceRepo(t)
	for _, id := range []string{first, second} {
		if code, _ := addRepo(t, srv, id, `{"git_url":"`+src+`"}`); code != http.StatusCreated {
			t.Fatalf("adding: %d", code)
		}
	}
	// One of its own (an empty repository) has no folder to bring up to date.
	empty := filepath.Join(t.TempDir(), "own")
	mustGit(t, t.TempDir(), "init", "-q", "--bare", empty)
	if code, _ := addRepo(t, srv, first, `{"git_url":"`+empty+`"}`); code != http.StatusCreated {
		t.Fatalf("adding a second: %d", code)
	}
	ctx := context.Background()
	clone := filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "web-client")
	// Work merged into the repository's main branch.
	if err := os.WriteFile(filepath.Join(clone, "merged.txt"), []byte("m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, clone, "add", "-A")
	mustGit(t, clone, "-c", "user.name=t", "-c", "user.email=t@x", "commit", "-qm", "merged")
	main := mustGit(t, clone, "rev-parse", "main")
	// The second project's folder has work of its own left in it: it does not move.
	otherDir := filepath.Join(srv.opts.WorkspaceDir, "other", "web-client")
	if err := os.WriteFile(filepath.Join(otherDir, "wip.txt"), []byte("w\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.syncSharedRepos(ctx, srv.projectOf(first))
	if got := mustGit(t, filepath.Join(projectDir, "web-client"), "rev-parse", "HEAD"); got != main {
		t.Errorf("the first project's folder must be on main's commit, it is on %s", got)
	}
	if got := mustGit(t, otherDir, "rev-parse", "HEAD"); got == main {
		t.Error("a folder with work left in it must not move")
	}
	// The list says it stayed behind, and the one that moved is not.
	w := get(t, srv, "/v1/projects", testToken)
	var list struct {
		Projects []Project `json:"projects"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	behind := map[string]int{}
	for _, p := range list.Projects {
		for _, r := range p.Repos {
			behind[p.Title+"/"+r.Name] = r.Behind
		}
	}
	if behind["other/web-client"] != 1 || behind["proj/web-client"] != 0 {
		t.Errorf("how far behind main each folder is: %v", behind)
	}
	// A folder on another branch is the user's choice: it stays.
	mine := filepath.Join(projectDir, "web-client")
	mustGit(t, mine, "checkout", "-q", "-b", "elsewhere")
	srv.syncSharedRepos(ctx, srv.projectOf(first))
	if got := gitx.Display(ctx, mine); got != "elsewhere" {
		t.Errorf("a folder on another branch stays there: %q", got)
	}
	srv.syncSharedRepos(ctx, nil) // no project: nothing to do
}

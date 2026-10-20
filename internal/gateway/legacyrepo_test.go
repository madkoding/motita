package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/gitx"
)

// A project that already has repositories inside it, from before projects became plain folders:
// nothing adds one any more, so the tests that need one make it the way the old API did.

// addRepo puts a repository in project id and answers 201 with the project, or the status the
// removed endpoint would have: 400 for a body that names nothing, 409 for a name or folder taken.
// The body carries git_url, and optionally name and shared.
func addRepo(t *testing.T, srv *Server, id, body string) (int, Project) {
	t.Helper()
	var in struct {
		GitURL string `json:"git_url"`
		Name   string `json:"name"`
		Shared bool   `json:"shared"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	p := srv.projectOf(id)
	if p == nil {
		return http.StatusNotFound, Project{}
	}
	if in.Path != "" {
		return addLocalRepo(t, srv, p, in.Path, in.Name)
	}
	gitURL := strings.TrimSpace(in.GitURL)
	name := repoNameOf(in.Name, gitURL)
	switch {
	case gitURL == "" || name == "":
		return http.StatusBadRequest, Project{}
	case p.repoIndex(name) >= 0 || pathExists(filepath.Join(p.Dir, name)):
		return http.StatusConflict, Project{}
	default:
		// A URL and a free folder name: the clone can start.
	}
	ctx := context.Background()
	if in.Shared {
		defer srv.lockCatalogRepo(gitURL)()
	}
	repo, err := srv.placeRepo(ctx, p, name, gitURL, filepath.Join(p.Dir, name), in.Shared)
	if err != nil {
		return http.StatusBadGateway, Project{}
	}
	_ = gitx.EnsureIgnoreBase(ctx, p.Dir)
	updated, err := srv.updateProject(p.ID, func(cur *Project) { cur.Repos = append(cur.Repos, repo) })
	if err != nil {
		return http.StatusInternalServerError, Project{}
	}
	for _, c := range srv.sessionsOfProject(p.ID) {
		srv.attachProjectRepos(ctx, updated, c.workspaceDir(), c.id)
		applyRuntimeTo(c.svc, updated, c.workspaceDir())
	}
	return http.StatusCreated, *updated
}

// addLocalRepo puts a repository the machine already had in p as the old endpoint did: a worktree
// of it on a branch of the project's own, the repository's own branch and files left alone.
func addLocalRepo(t *testing.T, srv *Server, p *Project, dir, rawName string) (int, Project) {
	t.Helper()
	ctx := context.Background()
	name := repoNameOf(rawName, dir)
	dest := filepath.Join(p.Dir, name)
	switch {
	case !gitx.IsRepoRoot(ctx, dir), !gitx.HasCommits(ctx, dir):
		return http.StatusBadRequest, Project{}
	case p.repoIndex(name) >= 0 || pathExists(dest):
		return http.StatusConflict, Project{}
	default:
		// A repository with commits and a free folder name: it can be placed.
	}
	branch := freeProjectBranch(ctx, dir, p)
	main := gitx.Display(ctx, dir)
	for _, candidate := range []string{defaultMainBranch, "master"} {
		if gitx.BranchExists(ctx, dir, candidate) {
			main = candidate
			break
		}
	}
	if err := gitx.AddWorktreeFrom(ctx, dir, dest, branch, main); err != nil {
		t.Fatalf("worktree of %s: %v", dir, err)
	}
	repo := ProjectRepo{Name: name, GitURL: originOf(dir, srv.gitCommandEnv()), Dir: dir, MainBranch: main,
		Shared: true, Local: true, ProjectBranch: branch}
	updated, err := srv.updateProject(p.ID, func(cur *Project) { cur.Repos = append(cur.Repos, repo) })
	if err != nil {
		return http.StatusInternalServerError, Project{}
	}
	for _, c := range srv.sessionsOfProject(p.ID) {
		srv.attachProjectRepos(ctx, updated, c.workspaceDir(), c.id)
		applyRuntimeTo(c.svc, updated, c.workspaceDir())
	}
	return http.StatusCreated, *updated
}

func TestTheCatalogNeedsAWorkspaceAndAFreeFolder(t *testing.T) {
	bare := newTestServer(t, &fakeService{})
	if dir := bare.catalogDir(); dir != "" {
		t.Errorf("no workspace, no catalog: %q", dir)
	}
	if _, _, err := bare.catalogClone("https://example.com/a/b", "b"); !errors.Is(err, errNoCatalog) {
		t.Errorf("cloning without a catalog: %v", err)
	}

	srv, _ := fullDiskServer(t)
	if _, err := os.Stat(srv.catalogDir()); srv.catalogDir() == "" || !os.IsNotExist(err) {
		t.Errorf("a workspace has a catalog folder, made when the first clone needs it: %q", srv.catalogDir())
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, _, err := srv.catalogClone(missing, "missing"); err == nil {
		t.Error("a repository that cannot be cloned is the answer")
	}
	// Creating one there is refused now; a project from before that is still read as it was.
	old := Project{ID: "old", Title: "repos", Dir: filepath.Join(srv.opts.WorkspaceDir, catalogFolder)}
	if err := srv.projects.save(old); err != nil {
		t.Fatal(err)
	}
	if dir := srv.catalogDir(); dir != "" {
		t.Errorf("a project of its own is in that folder, so it is not the catalog: %q", dir)
	}
}

func TestASharedRepositoryWithoutACatalogIsACloneOfItsOwn(t *testing.T) {
	srv, dir, _ := newSessionInProject(t)
	srv.opts.WorkspaceDir = ""
	id := onlyProjectID(t, srv)
	code, p := addRepo(t, srv, id, `{"shared":true,"git_url":"`+sourceRepo(t)+`"}`)
	if code != http.StatusCreated || p.Repos[0].Shared {
		t.Fatalf("adding: %d %+v", code, p.Repos)
	}
	if _, err := os.Stat(filepath.Join(dir, "web-client", ".git")); err != nil {
		t.Errorf("the project holds a clone: %v", err)
	}
}

func TestDeletingAProjectFreesItsBranchesInTheCatalog(t *testing.T) {
	srv, _ := fullDiskServer(t)
	if code, out := createProject(t, srv, `{"title":"owned","dir":"owned"}`); code != http.StatusCreated {
		t.Fatalf("creating: %d %v", code, out)
	}
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, id, `{"shared":true,"git_url":"`+sourceRepo(t)+`"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	clone := srv.projectOf(id).Repos[0].Dir
	srv.mainRefresh.wait()
	if w := doDelete(t, srv, "/v1/projects/"+id+"?force=1"); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if out := mustGit(t, clone, "branch", "--list", "project/*"); out != "" {
		t.Errorf("the project's branch is left in the catalog: %s", out)
	}
	if out := mustGit(t, clone, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("the project's worktree is left registered:\n%s", out)
	}
}

func TestAnUpdateThatCannotBeSavedIsReported(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if err := os.MkdirAll(filepath.Join(srv.projects.dir, id+".json.tmp", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if w := patchProject(t, srv, id, `{"title":"other"}`); w.Code != http.StatusInternalServerError {
		t.Errorf("a save that fails: %d %s", w.Code, w.Body)
	}
}

func TestMovingAProjectToAFolderThatDoesNotExistIsRefused(t *testing.T) {
	srv, _, id, _ := localProject(t)
	missing := filepath.Join(t.TempDir(), "nowhere")
	if w := patchProject(t, srv, id, `{"path":"`+missing+`"}`); w.Code != http.StatusBadRequest {
		t.Errorf("a folder that is not there: %d %s", w.Code, w.Body)
	}
}

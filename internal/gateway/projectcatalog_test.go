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

// The catalog: a repository is cloned once in the workspace, and each project that uses it has a
// worktree of that clone on a branch of its own.

// newProjectID creates one more project, in the folder dir, and answers its id.
func newProjectID(t *testing.T, srv *Server, dir string) string {
	t.Helper()
	code, out := createProject(t, srv, `{"title":"`+dir+`","dir":"`+dir+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("creating %s: %d %v", dir, code, out)
	}
	return out["id"].(string)
}

func TestAProjectBranchInUseStopsTheWorktree(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	src := sourceRepo(t)
	clone := filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "web-client")
	mustGit(t, t.TempDir(), "clone", "-q", src, clone)
	mustGit(t, clone, "worktree", "add", "-q", "-b", "project/proj", filepath.Join(t.TempDir(), "elsewhere"))
	// The name is taken by a branch checked out elsewhere: the project's worktree takes another.
	code, p := addRepo(t, srv, id, `{"shared":true,"git_url":"`+src+`"}`)
	if code != http.StatusCreated || !strings.HasPrefix(p.Repos[0].ProjectBranch, "project/proj-") {
		t.Errorf("a branch checked out elsewhere: %d %+v", code, p.Repos)
	}
}

// Both names taken by branches checked out elsewhere: there is nothing left to try, and it says so.
func TestAProjectWithNoFreeBranchStopsTheWorktree(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	src := sourceRepo(t)
	clone := filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "web-client")
	mustGit(t, t.TempDir(), "clone", "-q", src, clone)
	for _, branch := range []string{"project/proj", "project/proj-" + id[:8]} {
		mustGit(t, clone, "worktree", "add", "-q", "-b", branch, filepath.Join(t.TempDir(), "elsewhere"))
	}
	if code, _ := addRepo(t, srv, id, `{"shared":true,"git_url":"`+src+`"}`); code < 400 {
		t.Errorf("every branch checked out elsewhere: %d", code)
	}
}

func TestTheCatalogFolderCannotBeAProject(t *testing.T) {
	srv, _ := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"r","dir":"repos"}`); code != http.StatusBadRequest {
		t.Errorf("the catalog's folder is kept: %d", code)
	}
}

func TestAnIntegrationBringsTheProjectFoldersUpToDate(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	first := onlyProjectID(t, srv)
	second := newProjectID(t, srv, "other")
	src := sourceRepo(t)
	for _, id := range []string{first, second} {
		if code, _ := addRepo(t, srv, id, `{"shared":true,"git_url":"`+src+`"}`); code != http.StatusCreated {
			t.Fatalf("adding: %d", code)
		}
	}
	// One of its own (an empty repository) has no folder to bring up to date.
	empty := filepath.Join(t.TempDir(), "own")
	mustGit(t, t.TempDir(), "init", "-q", "--bare", empty)
	if code, _ := addRepo(t, srv, first, `{"shared":true,"git_url":"`+empty+`"}`); code != http.StatusCreated {
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

func TestTheAgentIsToldWhichRepositoriesAreShared(t *testing.T) {
	note := projectReposNote(&Project{Repos: []ProjectRepo{{Name: "api", GitURL: "u/api", Shared: true}}})
	if !strings.Contains(note, "- api (u/api): shared with other projects") {
		t.Errorf("the note must say the repository is shared: %q", note)
	}
}

// A project whose folder is called like another's, or like one that was deleted and left work on
// its branch, gets a branch of its own instead of starting on that work.
func TestAProjectBranchIsNeverSharedWithAnotherProject(t *testing.T) {
	srv, ws := projectServer(t)
	src := sourceRepo(t)
	mk := func(title, dir string) string {
		if code, _ := createProject(t, srv, `{"title":"`+title+`","dir":"`+dir+`"}`); code != http.StatusCreated {
			t.Fatalf("creating %s: %d", dir, code)
		}
		all, _ := srv.projects.loadAll()
		for _, p := range all {
			if p.Title == title {
				return p.ID
			}
		}
		t.Fatalf("no project %s", title)
		return ""
	}
	first, second := mk("one", "same"), mk("two", "twin")
	if code, _ := addRepo(t, srv, first, `{"shared":true,"git_url":"`+src+`"}`); code != http.StatusCreated {
		t.Fatalf("adding to the first: %d", code)
	}
	// Work left on the name the second project would take, by one that was deleted.
	clone := filepath.Join(ws, catalogFolder, "web-client")
	mustGit(t, clone, "branch", "project/twin")
	if code, _ := addRepo(t, srv, second, `{"shared":true,"git_url":"`+src+`"}`); code != http.StatusCreated {
		t.Fatalf("adding to the second: %d", code)
	}
	ctx := context.Background()
	a := gitx.Display(ctx, filepath.Join(ws, "same", "web-client"))
	b := gitx.Display(ctx, filepath.Join(ws, "twin", "web-client"))
	if a != "project/same" || !strings.HasPrefix(b, "project/twin-") {
		t.Errorf("branches %q and %q: the second project needs one of its own", a, b)
	}
	if got := srv.projectOf(second).Repos[0].ProjectBranch; got != b {
		t.Errorf("the project records %q, the folder is on %q", got, b)
	}
	// A project saved before the branch was recorded is still on the older name.
	if got := projectBranch(&Project{Dir: "/w/old"}, ProjectRepo{}); got != "project/old" {
		t.Errorf("older project: %q", got)
	}
	if got := freeProjectBranch(ctx, src, &Project{ID: "abc", Dir: "/w/none"}); got != "project/none" {
		t.Errorf("a free name is kept: %q", got)
	}
	if got := freeProjectBranch(ctx, src, &Project{ID: "abc", Dir: "/w/master"}); strings.Contains(got, "abc") {
		t.Errorf("a free name is kept: %q", got)
	}
}

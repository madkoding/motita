package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// plainProject saves a project whose folder is NOT a repository and returns its id and folder.
func plainProject(t *testing.T, srv *Server, ws, name string) (id, dir string) {
	t.Helper()
	dir = filepath.Join(ws, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id = "p-plain-" + name
	if err := srv.projects.save(Project{ID: id, Title: name, Dir: dir, Created: time.Now()}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	return id, dir
}

// repoWithOrigin makes a repository at dir whose origin holds one commit it does not have.
func repoWithOrigin(t *testing.T, ws, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	remote := filepath.Join(ws, filepath.Base(dir)+"-origin.git")
	mustRun(t, "git", "clone", "--bare", "-q", dir, remote)
	mustRun(t, "git", "-C", dir, "remote", "add", "origin", remote)
	elsewhere := filepath.Join(ws, filepath.Base(dir)+"-elsewhere")
	mustRun(t, "git", "clone", "-q", remote, elsewhere)
	mustRun(t, "git", "-C", elsewhere, "config", "user.email", "o@example.com")
	mustRun(t, "git", "-C", elsewhere, "config", "user.name", "O")
	if err := os.WriteFile(filepath.Join(elsewhere, "upstream.txt"), []byte("up\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", elsewhere, "add", "upstream.txt")
	mustRun(t, "git", "-C", elsewhere, "commit", "-qm", "upstream change")
	mustRun(t, "git", "-C", elsewhere, "push", "-q", "origin", "main")
}

// A folder that is not a repository has the repositories inside it, one or two folders down, and
// skips what a build leaves, hidden folders, files and anything deeper.
func TestNestedReposAreFoundInAPlainFolder(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	id, dir := plainProject(t, srv, ws, "services")
	for _, rel := range []string{"api", "group/web", "node_modules/dep", ".hidden/x", "a/b/c/deep"} {
		d := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		gitInit(t, d)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api", "wip.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := nestedReposOf(context.Background(), srv.projectOf(id))
	if len(got) != 2 || got[0].Path != "api" || got[1].Path != "group/web" {
		t.Fatalf("nested = %+v, want api and group/web", got)
	}
	if got[0].Branch != "main" || got[0].MainBranch != "main" || got[0].Changes != 1 {
		t.Errorf("api = %+v, want branch main, main branch main and one change", got[0])
	}
	w := get(t, srv, "/v1/projects", testToken)
	if !strings.Contains(w.Body.String(), `"nested_repos"`) || !strings.Contains(w.Body.String(), `"group/web"`) {
		t.Errorf("the project listing must carry the repositories: %s", w.Body.String())
	}
}

// A project that is a repository itself is one repository: what is inside is not listed. A folder
// that does not exist, or has no folder, lists nothing.
func TestNestedReposAreNotListedForARepositoryOrAMissingFolder(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	id := makeProject(t, srv, "repo")
	p := srv.projectOf(id)
	inner := filepath.Join(p.Dir, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, inner)
	ctx := context.Background()
	if got := nestedReposOf(ctx, p); got != nil {
		t.Errorf("a repository lists %+v, want nothing", got)
	}
	if got := nestedReposOf(ctx, &Project{}); got != nil {
		t.Errorf("a project with no folder lists %+v, want nothing", got)
	}
	if got := nestedReposOf(ctx, &Project{Dir: filepath.Join(ws, "gone")}); got != nil {
		t.Errorf("a folder that does not exist lists %+v, want nothing", got)
	}
}

// "Update now" of a repository inside a plain folder brings its main branch up to date, never
// forces a dirty one, refuses a path the folder does not hold and a second run at once.
func TestRefreshNestedRepo(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	id, dir := plainProject(t, srv, ws, "services")
	api := filepath.Join(dir, "api")
	repoWithOrigin(t, ws, api)
	url := "/v1/projects/" + id + "/main/refresh?repo="

	if w := post(t, srv, url+"nope", "{}", testToken); w.Code != http.StatusNotFound {
		t.Errorf("a path the folder does not hold = %d, want 404", w.Code)
	}
	if w := post(t, srv, url+"..%2F..", "{}", testToken); w.Code != http.StatusNotFound {
		t.Errorf("a path outside the folder = %d, want 404", w.Code)
	}
	if err := os.WriteFile(filepath.Join(api, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := post(t, srv, url+"api", "{}", testToken)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "uncommitted") || hasUpstream(api) {
		t.Fatalf("dirty refresh = %d %s, want a notice and nothing moved", w.Code, w.Body.String())
	}
	if err := os.Remove(filepath.Join(api, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	w = post(t, srv, url+"api", "{}", testToken)
	if w.Code != http.StatusOK || !hasUpstream(api) || strings.Contains(w.Body.String(), "uncommitted") {
		t.Fatalf("clean refresh = %d %s, upstream=%v", w.Code, w.Body.String(), hasUpstream(api))
	}
	if srv.mainRefresh.warning(id) != "" {
		t.Errorf("a nested update must not write the project's own notice: %q", srv.mainRefresh.warning(id))
	}

	srv.mainRefresh.begin(id + "\x00api")
	if w := post(t, srv, url+"api", "{}", testToken); w.Code != http.StatusConflict {
		t.Errorf("a second update at once = %d, want 409", w.Code)
	}
}

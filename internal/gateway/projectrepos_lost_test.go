package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func commitHere(t *testing.T, dir string) {
	t.Helper()
	mustGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@localhost",
		"commit", "-q", "--allow-empty", "-m", "local work")
}

// update changes only what the change touches, on the project as it is NOW: a copy taken before a
// long clone must not undo an edit made meanwhile, nor bring back a deleted project.
func TestProjectStoreUpdateMergesOntoTheCurrentProject(t *testing.T) {
	srv, _ := projectServer(t)
	ps := srv.projects
	if err := ps.save(Project{ID: "p1", Title: "old", Dir: "/x"}); err != nil {
		t.Fatal(err)
	}
	stale, _ := ps.load("p1")
	if err := ps.save(Project{ID: "p1", Title: "renamed", Dir: "/x"}); err != nil {
		t.Fatal(err)
	}
	got, err := ps.update(stale.ID, func(p *Project) { p.Repos = append(p.Repos, ProjectRepo{Name: "r"}) })
	if err != nil || got.Title != "renamed" || len(got.Repos) != 1 {
		t.Fatalf("update on the current project: %+v %v", got, err)
	}
	if saved, _ := ps.load("p1"); saved.Title != "renamed" || len(saved.Repos) != 1 {
		t.Errorf("saved: %+v", saved)
	}
	if err := ps.delete("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.update("p1", func(*Project) {}); !errors.Is(err, errProjectGone) {
		t.Errorf("a deleted project must stay deleted: %v", err)
	}
	if saved, _ := ps.load("p1"); saved != nil {
		t.Errorf("update brought the project back: %+v", saved)
	}
	if _, err := ps.update("../x", func(*Project) {}); err == nil {
		t.Error("an id that is not a name is refused")
	}
	if err := os.WriteFile(filepath.Join(ps.dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.update("bad", func(*Project) {}); err == nil {
		t.Error("a file that is not a project is refused")
	}
	if err := os.Mkdir(filepath.Join(ps.dir, "dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.update("dir", func(*Project) {}); err == nil {
		t.Error("a file that cannot be read is refused")
	}
}

// A PATCH of the project settings keeps a repository added while it ran, and a project deleted
// meanwhile stays deleted (F76): only the fields the request names are written.
func TestPatchingAProjectKeepsWhatChangedMeanwhile(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	beforeProjectUpdate = func(pid string) {
		_, _ = srv.projects.update(pid, func(p *Project) { p.Repos = append(p.Repos, ProjectRepo{Name: "late"}) })
	}
	t.Cleanup(func() { beforeProjectUpdate = func(string) {} })
	if w := patchProject(t, srv, id, `{"title":"renamed"}`); w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	saved, _ := srv.projects.load(id)
	if saved.Title != "renamed" || len(saved.Repos) != 1 || saved.Repos[0].Name != "late" {
		t.Errorf("the repository added meanwhile was lost: %+v", saved)
	}

	beforeProjectUpdate = func(pid string) { _ = srv.projects.delete(pid) }
	if w := patchProject(t, srv, id, `{"title":"again"}`); w.Code != http.StatusNotFound {
		t.Errorf("a patch of a project deleted meanwhile: %d %s", w.Code, w.Body)
	}
	if p, _ := srv.projects.load(id); p != nil {
		t.Errorf("the project came back: %+v", p)
	}
}

// Deleting a project stops on commits its own clones hold that no remote has, as removing one
// repository does (F77); ?force=1 confirms the discard.
func TestDeletingAProjectStopsOnUnpublishedCommits(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	empty := filepath.Join(t.TempDir(), "blank")
	mustGit(t, t.TempDir(), "init", "-q", "--bare", empty)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+empty+`"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	own := filepath.Join(projectDir, "blank")
	commitHere(t, own)
	path := "/v1/projects/" + id
	if w := send(t, srv, http.MethodDelete, path, testToken, ""); w.Code != http.StatusConflict {
		t.Fatalf("an unpublished commit must stop the deletion: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("the clone must still be there: %v", err)
	}
	if w := send(t, srv, http.MethodDelete, path+"?force=1", testToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("a confirmed deletion: %d %s", w.Code, w.Body)
	}
}

// loadAll reads the projects and only the projects: folders, other files, unreadable and
// unparseable ones are left out of the list instead of failing it.
func TestLoadAllSkipsWhatIsNotAProject(t *testing.T) {
	ps, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.save(Project{ID: "p1", Title: "kept", Dir: "/x"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(ps.dir, "folder.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir, "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ps.dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ps.dir, "nowhere"), filepath.Join(ps.dir, "ghost.json")); err != nil {
		t.Skip("symlinks are not available here")
	}
	all, err := ps.loadAll()
	if err != nil || len(all) != 1 || all[0].ID != "p1" {
		t.Errorf("only the project is listed: %+v %v", all, err)
	}
}

// A repository URL that cannot name a folder is refused before anything is created, and one that
// cannot be cloned leaves no project behind, nor the folder this request made.
func TestCreateProjectFromAURLThatFailsLeavesNothing(t *testing.T) {
	srv, ws := projectServer(t)
	hidden := `{"title":"n","dir":"n","git_url":"https://example.com/.hidden"}`
	if code, _ := createProject(t, srv, hidden); code != http.StatusBadRequest {
		t.Errorf("a URL with no usable folder name: %d", code)
	}
	code, _ := createProject(t, srv, `{"title":"f","dir":"f","git_url":"`+filepath.Join(ws, "no-such-origin")+`"}`)
	if code == http.StatusCreated {
		t.Fatalf("a clone that fails must fail the request: %d", code)
	}
	if _, err := os.Stat(filepath.Join(ws, "f")); err == nil {
		t.Error("the folder this request made goes with it")
	}
	if got, _ := srv.projects.loadAll(); len(got) != 0 {
		t.Errorf("no project record is left: %+v", got)
	}
}

// When the clone worked but the project cannot be saved with it, the request fails with 500.
func TestCloneIntoNewProjectReportsASaveThatFails(t *testing.T) {
	origin := t.TempDir()
	gitRepo(t, origin)
	srv, ws := projectServer(t)
	p := Project{ID: "pclone", Title: "c", Dir: filepath.Join(ws, "c")}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := srv.projects.save(p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srv.projects.dir, p.ID+".json.tmp", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/projects", nil)
	_, _, ok := srv.cloneIntoNewProject(w, r, p, origin, "origin", false)
	if ok || w.Code != http.StatusInternalServerError {
		t.Errorf("a save that fails: ok=%v code=%d", ok, w.Code)
	}
}

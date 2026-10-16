package gateway

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
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

// Two repositories added to one project at once both end up in it: each add waits for the other
// and decides from the project as it then is.
func TestTwoConcurrentAddsKeepBothRepositories(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	bodies := []string{
		`{"git_url":"` + sourceRepo(t) + `","name":"one"}`,
		`{"git_url":"` + sourceRepo(t) + `","name":"two"}`,
	}
	codes := make([]int, len(bodies))
	var wg sync.WaitGroup
	for i, body := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = send(t, srv, http.MethodPost, "/v1/projects/"+id+"/repos", testToken, body).Code
		}()
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusCreated {
			t.Errorf("add %d: %d", i, code)
		}
	}
	if p := srv.projectOf(id); p == nil || len(p.Repos) != 2 {
		t.Errorf("both repositories must be in the project: %+v", p)
	}
}

// Taking a clone out of a project, or out of the catalog, deletes it: commits no remote has would
// go with it, however clean the working tree is.
func TestADeletedCloneWithUnpublishedCommitsNeedsConfirmation(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	empty := filepath.Join(t.TempDir(), "blank")
	mustGit(t, t.TempDir(), "init", "-q", "--bare", empty)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+empty+`"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	own := filepath.Join(projectDir, "blank")
	commitHere(t, own)
	path := "/v1/projects/" + id + "/repos/blank"
	if w := send(t, srv, http.MethodDelete, path, testToken, ""); w.Code != http.StatusConflict {
		t.Fatalf("an unpublished commit must stop the removal: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("the clone must still be there: %v", err)
	}
	if w := send(t, srv, http.MethodDelete, path+"?force=1", testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("a confirmed removal: %d %s", w.Code, w.Body)
	}

	clone := filepath.Join(srv.opts.WorkspaceDir, catalogFolder, "web-client")
	mustGit(t, t.TempDir(), "clone", "-q", sourceRepo(t), clone)
	commitHere(t, clone)
	if w := send(t, srv, http.MethodDelete, "/v1/repos/web-client", testToken, ""); w.Code != http.StatusConflict {
		t.Fatalf("an unpublished commit must stop the deletion: %d %s", w.Code, w.Body)
	}
	w := send(t, srv, http.MethodDelete, "/v1/repos/web-client?force=1", testToken, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("a confirmed deletion: %d %s", w.Code, w.Body)
	}
}

// A project deleted while a repository operation runs stays deleted: the operation answers 404,
// and an added repository's folder does not outlive it.
func TestAProjectDeletedMidOperationStaysDeleted(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	vanish := func() {
		beforeProjectUpdate = func(pid string) { _ = srv.projects.delete(pid) }
		t.Cleanup(func() { beforeProjectUpdate = func(string) {} })
	}
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`","name":"keep"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}

	vanish()
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`","name":"late"}`); code != http.StatusNotFound {
		t.Errorf("an add to a project deleted meanwhile: %d", code)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "late")); !os.IsNotExist(err) {
		t.Errorf("the folder of the lost add must be cleaned up: %v", err)
	}
	if p, _ := srv.projects.load(id); p != nil {
		t.Errorf("the project must not come back: %+v", p)
	}

	// Remove, and detach, on a project that is deleted just before they save.
	srv2, _, _ := newSessionInProject(t)
	id2 := onlyProjectID(t, srv2)
	if code, _ := addRepo(t, srv2, id2, `{"git_url":"`+sourceRepo(t)+`","name":"keep"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	beforeProjectUpdate = func(pid string) { _ = srv2.projects.delete(pid) }
	w := send(t, srv2, http.MethodDelete, "/v1/projects/"+id2+"/repos/keep", testToken, "")
	if w.Code != http.StatusNotFound {
		t.Errorf("a removal from a project deleted meanwhile: %d %s", w.Code, w.Body)
	}
	srv3, _, id3 := bareProject(t)
	beforeProjectUpdate = func(pid string) { _ = srv3.projects.delete(pid) }
	if code, _ := detachMain(t, srv3, id3); code != http.StatusNotFound {
		t.Errorf("a detach of a project deleted meanwhile: %d", code)
	}
}

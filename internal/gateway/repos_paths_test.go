package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoriesAreOptionalWithoutAProjectDirectory(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if w := get(t, srv, "/v1/repos", testToken); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"repositories":[]`) {
		t.Errorf("an empty list: %d %s", w.Code, w.Body)
	}
	if w := send(t, srv, http.MethodPatch, "/v1/repos/x", testToken, `{}`); w.Code != http.StatusNotImplemented {
		t.Errorf("patching: %d", w.Code)
	}
	if w := send(t, srv, http.MethodPost, "/v1/repos", testToken, `{}`); w.Code != http.StatusNotImplemented {
		t.Errorf("creating: %d", w.Code)
	}
	if w := post(t, srv, "/v1/sessions", `{"repo_id":"x"}`, testToken); w.Code != http.StatusNotFound {
		t.Errorf("a session on a repository nobody recorded: %d", w.Code)
	}
	srv.mirrorToRepository(&Project{RepoID: "x"})
	srv.forgetRepositoriesAt("/nowhere")
	srv.migrateRepositories()
	if got, err := srv.repoRecord("x"); got != nil || err != nil {
		t.Errorf("no store, no record: %v %v", got, err)
	}
}

func TestAProjectDirectoryThatCannotHoldRepositoriesStillServesProjects(t *testing.T) {
	pdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pdir, repoStoreFolder), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ProjectDir = pdir; o.WorkspaceDir = t.TempDir() })
	if srv.projects == nil || srv.repos != nil {
		t.Errorf("projects work, repositories are off: %v %v", srv.projects, srv.repos)
	}
}

func TestChangingRepositorySettingsReportsWhatIsWrong(t *testing.T) {
	srv, _ := projectServer(t)
	_, repo := createRepository(t, srv, `{"git_url":"`+sourceRepo(t)+`"}`)
	path := "/v1/repos/" + repo.ID

	if w := send(t, srv, http.MethodPatch, path, testToken, `{`); w.Code != http.StatusBadRequest {
		t.Errorf("broken JSON: %d", w.Code)
	}
	if w := send(t, srv, http.MethodPatch, path, testToken, `{}`); w.Code != http.StatusOK {
		t.Errorf("nothing to change: %d", w.Code)
	}
	all := `{"main_branch":"main","merge_method":"squash","pr_max_fixes":2,"auto_merge":true,"auto_continue":true}`
	w := send(t, srv, http.MethodPatch, path, testToken, all)
	if w.Code != http.StatusOK {
		t.Fatalf("every setting: %d %s", w.Code, w.Body)
	}
	got, _ := srv.repos.load(repo.ID)
	if got.MergeMethod != "squash" || got.PRMaxFixes != 2 || !got.AutoMerge || !got.AutoContinue || got.MainBranch != "main" {
		t.Errorf("saved: %+v", got)
	}

	// The save fails when its temporary file cannot be written.
	if err := os.Mkdir(filepath.Join(srv.repos.dir, repo.ID+".json.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if w := send(t, srv, http.MethodPatch, path, testToken, `{"auto_merge":false}`); w.Code != http.StatusInternalServerError {
		t.Errorf("a save that fails: %d", w.Code)
	}
}

func TestRecordingARepositoryReportsWhatIsWrong(t *testing.T) {
	srv, _ := projectServer(t)
	for body, want := range map[string]int{
		`{`:                              http.StatusBadRequest,
		`{}`:                             http.StatusBadRequest,
		`{"git_url":"https://x/.."}`:     http.StatusBadRequest,
		`{"path":"` + t.TempDir() + `"}`: http.StatusForbidden,
	} {
		if code, _ := createRepository(t, srv, body); code != want {
			t.Errorf("%s: %d, want %d", body, code, want)
		}
	}
	if code, _ := createRepository(t, srv, `{"git_url":"/nowhere/at/all"}`); code < 400 {
		t.Errorf("a clone that fails: %d", code)
	}
	failing := randReader
	randReader = failingReader{}
	code, _ := createRepository(t, srv, `{"git_url":"`+sourceRepo(t)+`"}`)
	randReader = failing
	if code != http.StatusInternalServerError {
		t.Errorf("a record that cannot get an id: %d", code)
	}
}

func TestDeletingARepositoryProtectsWorkThatExistsNowhereElse(t *testing.T) {
	srv, _ := projectServer(t)
	_, repo := createRepository(t, srv, `{"git_url":"`+sourceRepo(t)+`"}`)
	path := "/v1/repos/" + repo.ID

	if err := os.WriteFile(filepath.Join(repo.Dir, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := send(t, srv, http.MethodDelete, path, testToken, ""); w.Code != http.StatusConflict {
		t.Errorf("uncommitted work: %d", w.Code)
	}
	mustGit(t, repo.Dir, "add", "wip.txt")
	mustGit(t, repo.Dir, "commit", "-qm", "feat: only here")
	if w := send(t, srv, http.MethodDelete, path+"?force=1", testToken, ""); w.Code != http.StatusNoContent {
		t.Errorf("a confirmed discard: %d %s", w.Code, w.Body)
	}
}

func TestDeletingARepositoryWithCommitsNoRemoteHasIsRefused(t *testing.T) {
	srv, _ := projectServer(t)
	_, repo := createRepository(t, srv, `{"git_url":"`+sourceRepo(t)+`"}`)
	if err := os.WriteFile(filepath.Join(repo.Dir, "c.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo.Dir, "add", "c.txt")
	mustGit(t, repo.Dir, "commit", "-qm", "feat: only here")
	if w := send(t, srv, http.MethodDelete, "/v1/repos/"+repo.ID, testToken, ""); w.Code != http.StatusConflict {
		t.Errorf("unpublished commits: %d", w.Code)
	}
}

func TestDeletingARepositoryReportsWhatTheDiskRefuses(t *testing.T) {
	srv, _ := projectServer(t)
	_, repo := createRepository(t, srv, `{"git_url":"`+sourceRepo(t)+`"}`)
	restore := removeAll
	removeAll = func(string) error { return errors.New("no") }
	w := send(t, srv, http.MethodDelete, "/v1/repos/"+repo.ID, testToken, "")
	removeAll = restore
	if w.Code != http.StatusInternalServerError {
		t.Errorf("a clone that cannot be removed: %d", w.Code)
	}
	// The record's file is a folder with something in it: it cannot be removed either.
	stuck := Repository{ID: "stuck", Dir: t.TempDir(), Local: true}
	if err := os.MkdirAll(filepath.Join(srv.repos.dir, "stuck.json", "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.deleteRepository(rec, httptest.NewRequest(http.MethodDelete, "/v1/repos/stuck", nil), &stuck)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a record that cannot be removed: %d", rec.Code)
	}
}

func TestLinkingARepositoryReportsWhatIsWrong(t *testing.T) {
	srv, _ := projectServer(t)
	_, repo := createRepository(t, srv, `{"git_url":"`+sourceRepo(t)+`"}`)
	empty := t.TempDir()
	mustGit(t, empty, "init", "-q", "-b", "main")
	_, bare := createRepository(t, srv, `{"git_url":"`+empty+`","name":"empty-one"}`)
	_, made := createProject(t, srv, `{"title":"p","dir":"p"}`)
	id := made["id"].(string)
	p := srv.projectOf(id)

	if err := os.Mkdir(filepath.Join(p.Dir, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		body string
		want int
	}{
		{`{"repo_id":"` + repo.ID + `","name":"a b"}`, http.StatusBadRequest},
		{`{"repo_id":"` + repo.ID + `","name":"taken"}`, http.StatusConflict},
		{`{"repo_id":"` + bare.ID + `"}`, http.StatusConflict},
	} {
		if code, _ := addRepo(t, srv, id, c.body); code != c.want {
			t.Errorf("%s: %d, want %d", c.body, code, c.want)
		}
	}
	// A project whose folder is gone cannot take the worktree.
	if err := os.RemoveAll(p.Dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Dir, []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := addRepo(t, srv, id, `{"repo_id":"`+repo.ID+`"}`); code != http.StatusInternalServerError {
		t.Errorf("a worktree that cannot be made: %d", code)
	}
}

func TestMigratingRepositoriesReportsWhatCannotBeDone(t *testing.T) {
	srv, id := repositoriesProject(t)
	// A project deleted while it is being linked is not an error for the others.
	if _, err := srv.projects.update(id, func(p *Project) {
		p.RepoID = ""
		p.Repos[0].RepoID = ""
	}); err != nil {
		t.Fatal(err)
	}
	beforeProjectUpdate = func(pid string) { _ = srv.projects.delete(pid) }
	t.Cleanup(func() { beforeProjectUpdate = func(string) {} })
	srv.migrateProject(*srv.projectOf(id), nil)
	beforeProjectUpdate = func(string) {}

	failing := randReader
	randReader = failingReader{}
	_, got := srv.record(nil, Repository{Dir: t.TempDir()})
	randReader = failing
	if got != "" {
		t.Errorf("no id, no record: %q", got)
	}

	saved := srv.repos.dir
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)
	srv.repos.dir = file
	_, got = srv.record(nil, Repository{Dir: t.TempDir()})
	srv.migrateRepositories()
	srv.repos.dir = saved
	if got != "" {
		t.Errorf("a record that cannot be saved: %q", got)
	}
}

func TestACloneWithNoProjectGetsARecordWhenTheCatalogIsRead(t *testing.T) {
	srv, ws := projectServer(t)
	src := sourceRepo(t)
	dir := filepath.Join(ws, catalogFolder, "loose")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, filepath.Dir(dir), "clone", "-q", src, dir)
	_ = os.Mkdir(filepath.Join(ws, catalogFolder, ".hidden"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, catalogFolder, "note"), nil, 0o600)
	srv.migrateRepositories()
	repos := listRepositories(t, srv)
	if len(repos) != 1 || repos[0].Name != "loose" || repos[0].GitURL == "" {
		t.Errorf("recorded from the catalog: %+v", repos)
	}
}

func TestCreatingAProjectReportsABadRepositoryName(t *testing.T) {
	srv, _ := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"x","dir":"x","git_url":"https://example.com/.hidden"}`); code != http.StatusBadRequest {
		t.Errorf("an unusable repository name: %d", code)
	}
}

func TestACloneThatCannotBeRecordedFailsTheNewProject(t *testing.T) {
	srv, _ := projectServer(t)
	beforeProjectUpdate = func(pid string) { _ = srv.projects.delete(pid) }
	t.Cleanup(func() { beforeProjectUpdate = func(string) {} })
	if code, _ := createProject(t, srv, `{"title":"c","dir":"c","git_url":"`+sourceRepo(t)+`"}`); code != http.StatusInternalServerError {
		t.Errorf("a project that vanished while it was cloned: %d", code)
	}
}

func TestWithoutAWorkspaceThereIsNoCatalogToRead(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ProjectDir = t.TempDir() })
	srv.migrateRepositories()
	if got := listRepositories(t, srv); len(got) != 0 {
		t.Errorf("nothing to record: %+v", got)
	}
}

func TestDeletingACatalogCloneByNameForgetsItsRecord(t *testing.T) {
	srv, _ := projectServer(t)
	_, repo := createRepository(t, srv, `{"git_url":"`+sourceRepo(t)+`"}`)
	if w := send(t, srv, http.MethodDelete, "/v1/repos/"+filepath.Base(repo.Dir), testToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("deleting by name: %d %s", w.Code, w.Body)
	}
	if got, _ := srv.repos.load(repo.ID); got != nil {
		t.Errorf("the record goes with the clone: %+v", got)
	}
}

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
)

// repositoriesProject is a project whose folder is a repository, with a plain clone inside it.
func repositoriesProject(t *testing.T) (*Server, string) {
	t.Helper()
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`"}`); code != http.StatusCreated {
		t.Fatalf("adding the repository: %d", code)
	}
	return srv, id
}

func listRepositories(t *testing.T, srv *Server) []repositoryView {
	t.Helper()
	w := get(t, srv, "/v1/repos", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("listing: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Repositories []repositoryView `json:"repositories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Repositories
}

func TestEveryRepositoryOfAProjectGetsARecordAndALink(t *testing.T) {
	srv, id := repositoriesProject(t)
	srv.migrateRepositories()

	p := srv.projectOf(id)
	if p.RepoID == "" || len(p.Repos) != 1 || p.Repos[0].RepoID == "" || p.RepoID == p.Repos[0].RepoID {
		t.Fatalf("the project links its folder and its clone to a record each: %+v", p)
	}
	repos := listRepositories(t, srv)
	if len(repos) != 2 {
		t.Fatalf("one record per repository, got %d: %+v", len(repos), repos)
	}
	for _, r := range repos {
		if len(r.Projects) != 1 || r.Projects[0].ID != id {
			t.Errorf("%s is used by the project: %+v", r.Name, r.Projects)
		}
	}
}

func TestMigratingRepositoriesTwiceChangesNothing(t *testing.T) {
	srv, id := repositoriesProject(t)
	srv.migrateRepositories()
	before := listRepositories(t, srv)
	linked := *srv.projectOf(id)

	srv.migrateRepositories()
	after := listRepositories(t, srv)
	if len(after) != len(before) {
		t.Fatalf("a second run adds nothing: %d -> %d", len(before), len(after))
	}
	if again := srv.projectOf(id); again.RepoID != linked.RepoID || again.Repos[0].RepoID != linked.Repos[0].RepoID {
		t.Errorf("links are kept: %+v vs %+v", again, linked)
	}
}

func TestProjectSettingsMoveToTheRepositoryOfItsFolder(t *testing.T) {
	srv, id := repositoriesProject(t)
	if w := patchProject(t, srv, id, `{"merge_method":"squash","auto_merge":true}`); w.Code != http.StatusOK {
		t.Fatalf("setting the project: %d", w.Code)
	}
	srv.migrateRepositories()
	rec, err := srv.repos.load(srv.projectOf(id).RepoID)
	if err != nil || rec == nil || rec.MergeMethod != "squash" || !rec.AutoMerge {
		t.Fatalf("the record starts with the project's settings: %+v %v", rec, err)
	}
}

func TestChangingARepositoryAlsoChangesTheProjectThatIsIt(t *testing.T) {
	srv, id := repositoriesProject(t)
	srv.migrateRepositories()
	repoID := srv.projectOf(id).RepoID

	w := send(t, srv, http.MethodPatch, "/v1/repos/"+repoID, testToken, `{"merge_method":"rebase","pr_max_fixes":3}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patching: %d %s", w.Code, w.Body.String())
	}
	if p := srv.projectOf(id); p.MergeMethod != "rebase" || p.PRMaxFixes != 3 {
		t.Errorf("the project follows its repository: %+v", p)
	}
	for body, want := range map[string]int{
		`{"merge_method":"fast"}`: http.StatusBadRequest,
		`{"pr_max_fixes":-1}`:     http.StatusBadRequest,
		`{"main_branch":"nope"}`:  http.StatusBadRequest,
	} {
		if got := send(t, srv, http.MethodPatch, "/v1/repos/"+repoID, testToken, body).Code; got != want {
			t.Errorf("%s answered %d, want %d", body, got, want)
		}
	}
	if got := send(t, srv, http.MethodPatch, "/v1/repos/missing", testToken, `{}`).Code; got != http.StatusNotFound {
		t.Errorf("an unknown repository: %d", got)
	}
}

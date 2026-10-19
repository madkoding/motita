package gateway

import (
	"context"
	"encoding/json"
	"github.com/madkoding/motita/internal/gitx"
	"net/http"
	"os"
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

func createRepository(t *testing.T, srv *Server, body string) (int, Repository) {
	t.Helper()
	w := send(t, srv, http.MethodPost, "/v1/repos", testToken, body)
	var r Repository
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	return w.Code, r
}

// linkToTwoProjects makes two projects and links the repository to both.
func linkToTwoProjects(t *testing.T, srv *Server, repo Repository) []string {
	t.Helper()
	var ids []string
	for _, dir := range []string{"one", "two"} {
		_, out := createProject(t, srv, `{"title":"`+dir+`","dir":"`+dir+`"}`)
		id := out["id"].(string)
		ids = append(ids, id)
		code, p := addRepo(t, srv, id, `{"repo_id":"`+repo.ID+`"}`)
		if code != http.StatusCreated || len(p.Repos) != 1 || p.Repos[0].RepoID != repo.ID || !p.Repos[0].Shared {
			t.Fatalf("linking to %s: %d %+v", dir, code, p)
		}
	}
	return ids
}

func TestARepositoryIsClonedOnceAndLinkedToManyProjects(t *testing.T) {
	srv, _ := projectServer(t)
	src := sourceRepo(t)
	code, repo := createRepository(t, srv, `{"git_url":"`+src+`"}`)
	if code != http.StatusCreated || repo.ID == "" || repo.Name != "web-client" {
		t.Fatalf("creating: %d %+v", code, repo)
	}
	if code, _ := createRepository(t, srv, `{"git_url":"`+src+`"}`); code != http.StatusConflict {
		t.Errorf("the same repository twice: %d", code)
	}
	ids := linkToTwoProjects(t, srv, repo)
	if code, _ := addRepo(t, srv, ids[0], `{"repo_id":"`+repo.ID+`"}`); code != http.StatusConflict {
		t.Errorf("linking twice: %d", code)
	}
	if code, _ := addRepo(t, srv, ids[0], `{"repo_id":"nope"}`); code != http.StatusNotFound {
		t.Errorf("an unknown repository: %d", code)
	}
	if users := listRepositories(t, srv)[0].Projects; len(users) != 2 {
		t.Errorf("both projects use the one clone: %+v", users)
	}
	if w := send(t, srv, http.MethodDelete, "/v1/repos/"+repo.ID, testToken, ""); w.Code != http.StatusConflict {
		t.Errorf("a repository in use is not deleted: %d", w.Code)
	}
	for _, id := range ids {
		if w := send(t, srv, http.MethodDelete, "/v1/projects/"+id+"/repos/web-client", testToken, ""); w.Code >= 300 {
			t.Fatalf("unlinking: %d %s", w.Code, w.Body)
		}
	}
	if w := send(t, srv, http.MethodDelete, "/v1/repos/"+repo.ID, testToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("deleting: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(repo.Dir); !os.IsNotExist(err) {
		t.Errorf("the catalog's clone goes with its record: %v", err)
	}
	if got := listRepositories(t, srv); len(got) != 0 {
		t.Errorf("no record is left: %+v", got)
	}
}

func TestAFolderOfTheUserIsRecordedAndNeverDeleted(t *testing.T) {
	srv, _ := fullDiskServer(t)
	dir := sourceRepo(t)
	if code, _ := createRepository(t, srv, `{"path":"`+t.TempDir()+`"}`); code != http.StatusBadRequest {
		t.Errorf("a folder that is not a repository: %d", code)
	}
	code, repo := createRepository(t, srv, `{"path":"`+dir+`"}`)
	if code != http.StatusCreated || !repo.Local {
		t.Fatalf("recording: %d %+v", code, repo)
	}
	if w := send(t, srv, http.MethodDelete, "/v1/repos/"+repo.ID, testToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("forgetting: %d", w.Code)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the user's folder stays: %v", err)
	}
	if code, _ := createRepository(t, srv, `{}`); code != http.StatusBadRequest {
		t.Errorf("nothing asked: %d", code)
	}
}

func TestASessionWorksOnARepositoryWithoutAProject(t *testing.T) {
	srv, _ := projectServer(t)
	src := sourceRepo(t)
	_, repo := createRepository(t, srv, `{"git_url":"`+src+`"}`)

	w := post(t, srv, "/v1/sessions", `{"repo_id":"`+repo.ID+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("creating: %d %s", w.Code, w.Body)
	}
	var st struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st.ProjectID != repoScopeID(repo.ID) {
		t.Fatalf("the session is anchored to the repository: %+v", st)
	}
	c, _ := srv.lookup(st.ID)
	if c == nil || !gitx.SamePath(c.workspaceDir(), repo.Dir) {
		t.Fatalf("the session runs in the repository's checkout")
	}
	if p := srv.scopeOf(st.ID); p != nil {
		t.Errorf("a session id is not a scope: %+v", p)
	}
	if p := srv.scopeOf(st.ProjectID); p == nil || p.Dir != repo.Dir || p.GitURL != repo.GitURL {
		t.Errorf("the repository stands in for the project: %+v", p)
	}
	if got := srv.projectOf(st.ProjectID); got != nil {
		t.Errorf("the stand-in is not a stored project: %+v", got)
	}
	if got := srv.repoRemote(context.Background(), c, c.workspaceDir()); got != repo.GitURL {
		t.Errorf("the pull request goes to the repository: %q", got)
	}
	if w := send(t, srv, http.MethodDelete, "/v1/repos/"+repo.ID, testToken, ""); w.Code != http.StatusConflict {
		t.Errorf("a repository with sessions is not deleted: %d", w.Code)
	}

	iso := post(t, srv, "/v1/sessions", `{"repo_id":"`+repo.ID+`","isolate":true}`, testToken)
	_ = json.Unmarshal(iso.Body.Bytes(), &st)
	if c2, _ := srv.lookup(st.ID); c2 == nil || gitx.SamePath(c2.workspaceDir(), repo.Dir) {
		t.Errorf("an isolated session gets a worktree of its own")
	}
	if w := post(t, srv, "/v1/sessions", `{"repo_id":"nope"}`, testToken); w.Code != http.StatusNotFound {
		t.Errorf("an unknown repository: %d", w.Code)
	}
}

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

// The main branch of a project: chosen at creation, editable, offered by a selector, and the
// branch the project's checkout goes back to when its last session is deleted.

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func projectServer(t *testing.T) (*Server, string) {
	t.Helper()
	ws := t.TempDir()
	srv, _ := startServer(t, Options{
		Token: testToken, WorkspaceDir: ws,
		ProjectDir: filepath.Join(ws, "projects"), SessionDir: filepath.Join(ws, "sessions"),
		NewService: func() (Service, error) { return &fakeService{}, nil },
	})
	return srv, ws
}

func createProject(t *testing.T, srv *Server, body string) (int, map[string]any) {
	t.Helper()
	w := post(t, srv, "/v1/projects", body, testToken)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestSettleMainBranchPicksWhatTheRepositoryHas(t *testing.T) {
	ctx := context.Background()
	mk := func(branch string) string {
		d := t.TempDir()
		gitRepo(t, d)
		if branch != "main" {
			mustGit(t, d, "branch", "-m", "main", branch)
		}
		return d
	}
	// Asked for master, the repository has it too: the checkout moves there.
	both := mk("main")
	mustGit(t, both, "branch", "master")
	if got := settleMainBranch(ctx, both, "master"); got != "master" || gitx.Display(ctx, both) != "master" {
		t.Errorf("the requested branch wins: %q on %q", got, gitx.Display(ctx, both))
	}
	// Asked for main and the repository only has master: master.
	if got := settleMainBranch(ctx, mk("master"), "main"); got != "master" {
		t.Errorf("fall back to master, got %q", got)
	}
	// Neither: the branch it is on.
	if got := settleMainBranch(ctx, mk("trunk"), "main"); got != "trunk" {
		t.Errorf("fall back to the current branch, got %q", got)
	}
	// Nothing at all (no repository): what was asked for.
	if got := settleMainBranch(ctx, t.TempDir(), "main"); got != "main" {
		t.Errorf("no repository: %q", got)
	}
	// A checkout that cannot happen keeps the branch the clone is on.
	clash := mk("main")
	mustGit(t, clash, "checkout", "-q", "-b", "master")
	_ = os.WriteFile(filepath.Join(clash, "README.md"), []byte("master\n"), 0o644)
	mustGit(t, clash, "commit", "-qam", "on master")
	mustGit(t, clash, "checkout", "-q", "main")
	_ = os.WriteFile(filepath.Join(clash, "README.md"), []byte("dirty\n"), 0o644)
	if got := settleMainBranch(ctx, clash, "master"); got != "main" {
		t.Errorf("a refused checkout must leave the clone's branch, got %q", got)
	}
}

func TestMainBranchOfAProjectFromBeforeTheSetting(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	gitRepo(t, repo)
	if got := mainBranchOf(ctx, &Project{Dir: repo}); got != "main" {
		t.Errorf("a legacy project on main: %q", got)
	}
	mustGit(t, repo, "branch", "-m", "main", "master")
	if got := mainBranchOf(ctx, &Project{Dir: repo}); got != "master" {
		t.Errorf("a legacy project on master: %q", got)
	}
	mustGit(t, repo, "branch", "-m", "master", "trunk")
	if got := mainBranchOf(ctx, &Project{Dir: repo}); got != "" {
		t.Errorf("nothing to name: %q", got)
	}
	if got := mainBranchOf(ctx, &Project{Dir: t.TempDir()}); got != "" {
		t.Errorf("not a repository: %q", got)
	}
	if got := mainBranchOf(ctx, &Project{Dir: repo, MainBranch: "dev"}); got != "dev" {
		t.Errorf("the saved one wins: %q", got)
	}
}

func TestEditingAProject(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	mustGit(t, projectDir, "branch", "release")
	id := onlyProjectID(t, srv)
	patch := func(body string) (int, map[string]any) {
		w := send(t, srv, http.MethodPatch, "/v1/projects/"+id, testToken, body)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	code, out := patch(`{"title":" Renamed ","description":" about ","main_branch":"release"}`)
	if code != http.StatusOK || out["title"] != "Renamed" || out["description"] != "about" ||
		out["main_branch"] != "release" {
		t.Fatalf("a full edit: %d %v", code, out)
	}
	// Only what is sent changes.
	if code, out = patch(`{"description":""}`); code != http.StatusOK || out["title"] != "Renamed" ||
		out["main_branch"] != "release" {
		t.Errorf("a partial edit keeps the rest: %d %v", code, out)
	}
	for body, want := range map[string]int{
		`{"title":"  "}`:             http.StatusBadRequest,
		`{"main_branch":"bad name"}`: http.StatusBadRequest,
		`{"main_branch":"ghost"}`:    http.StatusBadRequest,
		`not json`:                   http.StatusBadRequest,
	} {
		if got, _ := patch(body); got != want {
			t.Errorf("PATCH %s answered %d, want %d", body, got, want)
		}
	}
	withPodman(t, false)
	if got, _ := patch(`{"podman":true}`); got != http.StatusConflict {
		t.Errorf("podman without podman: %d", got)
	}
	withPodman(t, true)
	if got, out := patch(`{"podman":true}`); got != http.StatusOK || out["podman"] != true {
		t.Errorf("podman with podman: %d %v", got, out)
	}
	if w := send(t, srv, http.MethodPatch, "/v1/projects/nope", testToken, `{}`); w.Code != http.StatusNotFound {
		t.Errorf("an unknown project: %d", w.Code)
	}

	// A folder that is not a repository only remembers the name.
	plain := filepath.Join(srv.opts.WorkspaceDir, "plain")
	_ = os.MkdirAll(plain, 0o755)
	_ = os.WriteFile(filepath.Join(plain, "a.txt"), []byte("x"), 0o644)
	_, made := createProject(t, srv, `{"title":"plain","dir":"plain"}`)
	pid, _ := made["id"].(string)
	w := send(t, srv, http.MethodPatch, "/v1/projects/"+pid, testToken, `{"main_branch":"trunk"}`)
	if w.Code != http.StatusOK {
		t.Errorf("a non-repository project: %d %s", w.Code, w.Body.String())
	}
}

func onlyProjectID(t *testing.T, srv *Server) string {
	t.Helper()
	all, err := srv.projects.loadAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("expected one project: %v %v", all, err)
	}
	return all[0].ID
}

func TestProjectBranchesEndpoint(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	mustGit(t, projectDir, "branch", "release")
	id := onlyProjectID(t, srv)

	w := send(t, srv, http.MethodGet, "/v1/projects/"+id+"/branches", testToken, "")
	var out struct {
		Branches []string `json:"branches"`
		Main     string   `json:"main_branch"`
		Current  string   `json:"current"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusOK || strings.Join(out.Branches, ",") != "main,release" || out.Main != "main" ||
		out.Current != "main" {
		t.Errorf("branches: %d %+v (session branches are not offered)", w.Code, out)
	}
	if w := send(t, srv, http.MethodGet, "/v1/projects/nope/branches", testToken, ""); w.Code != http.StatusNotFound {
		t.Errorf("an unknown project: %d", w.Code)
	}

	// A folder with no repository offers only the configured name; a listing failure offers it too.
	_, made := createProject(t, srv, `{"title":"plain","dir":"plain2"}`)
	pid, _ := made["id"].(string)
	if _, err := srv.projects.update(pid, func(p *Project) { p.MainBranch = "trunk" }); err != nil {
		t.Fatal(err)
	}
	w = send(t, srv, http.MethodGet, "/v1/projects/"+pid+"/branches", testToken, "")
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Branches) != 1 || out.Branches[0] != "trunk" {
		t.Errorf("a non-repository offers the configured branch: %+v", out)
	}
}

func TestListedProjectsCarryTheirMainBranch(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	w := send(t, srv, http.MethodGet, "/v1/projects", testToken, "")
	var out struct {
		Projects []Project `json:"projects"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Projects) != 1 || out.Projects[0].MainBranch != "main" {
		t.Errorf("the list must say the main branch: %+v", out.Projects)
	}
}

func TestDeletingTheLastSessionReturnsTheProjectToItsMainBranch(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	ctx := context.Background()
	mustGit(t, projectDir, "branch", "feature")

	// Another session keeps the project where it is.
	second := post(t, srv, "/v1/sessions", `{"project_id":"`+onlyProjectID(t, srv)+`"}`, testToken)
	var other SessionStatus
	_ = json.Unmarshal(second.Body.Bytes(), &other)
	mustGit(t, projectDir, "checkout", "-q", "feature")
	if rec := doDelete(t, srv, "/v1/sessions/"+other.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("deleting the first: %d %s", rec.Code, rec.Body.String())
	}
	if got := gitx.Display(ctx, projectDir); got != "feature" {
		t.Fatalf("a project that still has a session stays put, on %q", got)
	}

	// Uncommitted work in the checkout is never carried to another branch.
	_ = os.WriteFile(filepath.Join(projectDir, "README.md"), []byte("edited\n"), 0o644)
	if rec := doDelete(t, srv, "/v1/sessions/"+id); rec.Code != http.StatusNoContent {
		t.Fatalf("deleting the last: %d %s", rec.Code, rec.Body.String())
	}
	if got := gitx.Display(ctx, projectDir); got != "feature" {
		t.Fatalf("a dirty checkout must stay on %q", got)
	}

	// Clean, with no sessions: back on main.
	mustGit(t, projectDir, "checkout", "--", "README.md")
	srv.returnToMain(srv.projectOf(onlyProjectID(t, srv)))
	if got := gitx.Display(ctx, projectDir); got != "main" {
		t.Errorf("with no sessions the project goes back to main, it is on %q", got)
	}
	// Already there, or nothing to name, or a project that is gone: nothing happens.
	srv.returnToMain(srv.projectOf(onlyProjectID(t, srv)))
	srv.returnToMain(nil)
}

func TestReturnToMainSurvivesABranchThatIsGone(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	for _, c := range srv.snapshot() {
		_ = doDelete(t, srv, "/v1/sessions/"+c.id)
	}
	p := srv.projectOf(onlyProjectID(t, srv))
	p.MainBranch = "ghost"
	if err := srv.projects.save(*p); err != nil {
		t.Fatal(err)
	}
	mustGit(t, projectDir, "checkout", "-q", "-b", "feature")
	srv.returnToMain(p) // the checkout fails and is logged, not raised
	if got := gitx.Display(context.Background(), projectDir); got != "feature" {
		t.Errorf("a failed return leaves the checkout alone, it is on %q", got)
	}
}

// The dialog asks which branch a remote's HEAD points to before cloning anything.
func TestDefaultBranchOfARemote(t *testing.T) {
	srv, _ := projectServer(t)
	ask := func(url string) (int, string) {
		w := send(t, srv, http.MethodGet, "/v1/git/default-branch?url="+url, testToken, "")
		var out struct {
			Branch string `json:"branch"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.Branch
	}

	origin := t.TempDir()
	gitRepo(t, origin)
	mustGit(t, origin, "branch", "-m", "main", "develop")
	if code, branch := ask(origin); code != http.StatusOK || branch != "develop" {
		t.Errorf("a repository on develop: %d %q", code, branch)
	}

	// No commits, no HEAD to point anywhere: an empty answer, not an error.
	empty := t.TempDir()
	mustGit(t, empty, "init", "-q", "--bare")
	if code, branch := ask(empty); code != http.StatusOK || branch != "" {
		t.Errorf("an empty repository: %d %q", code, branch)
	}

	if code, _ := ask(""); code != http.StatusBadRequest {
		t.Errorf("no URL: %d", code)
	}
	// What a clone would refuse is not asked either: a transport helper runs a command.
	for _, url := range []string{"ext::sh%20-c%20touch%20x", "--upload-pack=touch%20x"} {
		if code, _ := ask(url); code != http.StatusBadRequest {
			t.Errorf("%s: %d", url, code)
		}
	}
	if code, _ := ask(filepath.Join(t.TempDir(), "nothing-here")); code != http.StatusBadGateway {
		t.Errorf("a remote that cannot be read: %d", code)
	}
}

func TestANewProjectIsAPlainFolderWithNoBranch(t *testing.T) {
	srv, ws := projectServer(t)
	code, out := createProject(t, srv, `{"title":"a","dir":"a","main_branch":"master"}`)
	if code != http.StatusCreated || out["repo_id"] != "" {
		t.Fatalf("creating: %d %v", code, out)
	}
	if p := srv.projectOf(out["id"].(string)); p.MainBranch != "" || p.RepoID != "" || len(p.Repos) != 0 {
		t.Errorf("a plain folder has no branch and no repository: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(ws, "a", ".git")); err == nil {
		t.Error("nothing is initialised in the folder")
	}
}

// A repository given while creating a project is cloned INSIDE the plain folder and recorded.
func TestCreateProjectFromAURLClonesIntoThePlainFolder(t *testing.T) {
	origin := t.TempDir()
	gitRepo(t, origin)
	srv, ws := projectServer(t)
	code, out := createProject(t, srv, `{"title":"c","dir":"c","git_url":"`+origin+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("creating: %d %v", code, out)
	}
	p := srv.projectOf(out["id"].(string))
	if p.GitURL != "" || p.RepoID != "" || len(p.Repos) != 1 || p.Repos[0].RepoID == "" {
		t.Fatalf("the clone is the project's repository, the folder is plain: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(ws, "c", ".git")); err == nil {
		t.Error("the project folder itself is not a repository")
	}
	if _, err := os.Stat(filepath.Join(p.Repos[0].Dir, ".git")); err != nil {
		t.Errorf("the clone is a repository: %v", err)
	}
	if code, _ := createProject(t, srv, `{"title":"d","dir":"d","git_url":"/nonexistent/repo-x"}`); code == http.StatusCreated {
		t.Error("a clone that fails fails the project")
	}
	if _, err := os.Stat(filepath.Join(ws, "d")); err == nil {
		t.Error("a failed creation leaves no folder behind")
	}
}

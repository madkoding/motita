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

// A project with more than one repository: the main one stays the project's folder, the others
// are cloned inside it and each session gets a worktree of every one.

// sourceRepo is a repository a project can clone, by its absolute path.
func sourceRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "web-client")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, dir)
	return dir
}

func projectJSON(t *testing.T, body []byte) Project {
	t.Helper()
	var p Project
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("not a project: %v %s", err, body)
	}
	return p
}

func addRepo(t *testing.T, srv *Server, id, body string) (int, Project) {
	t.Helper()
	w := send(t, srv, http.MethodPost, "/v1/projects/"+id+"/repos", testToken, body)
	var p Project
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return w.Code, p
}

func TestRepoNameOf(t *testing.T) {
	cases := map[[2]string]string{
		{"", "https://github.com/u/web-client.git"}: "web-client",
		{"", "git@github.com:u/api.git/"}:           "api",
		{"", "/srv/repos/lib"}:                      "lib",
		{" docs ", "https://x/y"}:                   "docs",
		{"", ""}:                                    "",
		{".hidden", "x"}:                            "",
		{"a/b", "x"}:                                "",
		{"a..b", "x"}:                               "",
	}
	for in, want := range cases {
		if got := repoNameOf(in[0], in[1]); got != want {
			t.Errorf("repoNameOf(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestAddingARepositoryToAProjectWithASession(t *testing.T) {
	srv, projectDir, sessionID := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	src := sourceRepo(t)

	code, p := addRepo(t, srv, id, `{"git_url":"`+src+`"}`)
	if code != http.StatusCreated || len(p.Repos) != 1 || p.Repos[0].Name != "web-client" ||
		p.Repos[0].MainBranch != "main" {
		t.Fatalf("adding a repository: %d %+v", code, p)
	}
	clone := filepath.Join(projectDir, gitx.ReposDir, "web-client")
	if p.Repos[0].Dir != clone || gitx.Repo(context.Background(), clone) != nil {
		t.Fatalf("the repository is cloned inside the project: %q", p.Repos[0].Dir)
	}
	// The main repository does not see the clone as its own change.
	if n, _ := gitx.WorkingTreeChanges(context.Background(), projectDir); n != 0 {
		t.Errorf("the main repository must ignore the other repositories, it has %d changes", n)
	}
	// The session already open gets a worktree of it, on its own branch.
	ws := convNamed(t, srv, sessionID).workspaceDir()
	wt := filepath.Join(ws, gitx.ReposDir, "web-client")
	if got := gitx.Display(context.Background(), wt); got != sessionBranch(sessionID) {
		t.Errorf("the session's worktree of the repository is on %q", got)
	}
	if n, _ := gitx.WorkingTreeChanges(context.Background(), ws); n != 0 {
		t.Errorf("the session's own worktree must ignore the nested one, it has %d changes", n)
	}
	// Asked again (a restart), the worktree that is there is the one.
	srv.attachProjectRepos(context.Background(), srv.projectOf(id), ws, sessionID)
	if got := gitx.Display(context.Background(), wt); got != sessionBranch(sessionID) {
		t.Errorf("attaching again must keep the worktree, it is on %q", got)
	}

	// Saved, and listed with what it says right now.
	w := get(t, srv, "/v1/projects", testToken)
	var list struct {
		Projects []Project `json:"projects"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Projects) != 1 || len(list.Projects[0].Repos) != 1 || list.Projects[0].Repos[0].Branch != "main" {
		t.Fatalf("the list must carry the repository: %s", w.Body)
	}

	// The same name again is refused, and a new session gets its worktree too.
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+src+`","name":"web-client"}`); code != http.StatusConflict {
		t.Errorf("a second repository of the same name: %d", code)
	}
	created := post(t, srv, "/v1/sessions", `{"project_id":"`+id+`"}`, testToken)
	var second SessionStatus
	_ = json.Unmarshal(created.Body.Bytes(), &second)
	if _, ok, _ := gitx.LiveWorktreeAt(context.Background(), clone,
		filepath.Join(second.Workspace, gitx.ReposDir, "web-client")); !ok {
		t.Errorf("a new session must get a worktree of every repository: %s", created.Body)
	}
}

func TestAddingARepositoryIsValidated(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, "nope", `{"git_url":"/x"}`); code != http.StatusNotFound {
		t.Errorf("unknown project: %d", code)
	}
	if code, _ := addRepo(t, srv, id, `{`); code != http.StatusBadRequest {
		t.Errorf("a broken body: %d", code)
	}
	if code, _ := addRepo(t, srv, id, `{"git_url":" "}`); code != http.StatusBadRequest {
		t.Errorf("no URL: %d", code)
	}
	if code, _ := addRepo(t, srv, id, `{"git_url":"/x","name":"../up"}`); code != http.StatusBadRequest {
		t.Errorf("a name that is a path: %d", code)
	}
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+filepath.Join(t.TempDir(), "missing")+`"}`); code < 400 {
		t.Errorf("a clone that fails: %d", code)
	}
	// A project that cannot be saved does not keep the repository.
	tmp := filepath.Join(srv.projects.dir, id+".json.tmp")
	if err := os.MkdirAll(filepath.Join(tmp, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`"}`); code != http.StatusInternalServerError {
		t.Errorf("a save that fails: %d", code)
	}
}

func TestRemovingARepositoryFromAProject(t *testing.T) {
	srv, projectDir, sessionID := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`","name":"lib"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	clone := filepath.Join(projectDir, gitx.ReposDir, "lib")
	wt := filepath.Join(convNamed(t, srv, sessionID).workspaceDir(), gitx.ReposDir, "lib")
	remove := func(path string) int {
		return send(t, srv, http.MethodDelete, path, testToken, "").Code
	}
	if code := remove("/v1/projects/nope/repos/lib"); code != http.StatusNotFound {
		t.Errorf("unknown project: %d", code)
	}
	if code := remove("/v1/projects/" + id + "/repos/other"); code != http.StatusNotFound {
		t.Errorf("unknown repository: %d", code)
	}
	// Work in the session's worktree of it stops the removal, until the user confirms.
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := remove("/v1/projects/" + id + "/repos/lib"); code != http.StatusConflict {
		t.Fatalf("uncommitted work must stop it: %d", code)
	}
	w := send(t, srv, http.MethodDelete, "/v1/projects/"+id+"/repos/lib?force=1", testToken, "")
	if p := projectJSON(t, w.Body.Bytes()); w.Code != http.StatusOK || len(p.Repos) != 0 {
		t.Fatalf("a confirmed removal: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Errorf("the clone must be gone: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the session's worktree of it must be gone: %v", err)
	}
	if p := srv.projectOf(id); p == nil || len(p.Repos) != 0 {
		t.Errorf("the project must be saved without it: %+v", p)
	}
}

func TestRemovingARepositoryThatCannotBeSaved(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	if err := os.MkdirAll(filepath.Join(srv.projects.dir, id+".json.tmp", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := send(t, srv, http.MethodDelete, "/v1/projects/"+id+"/repos/web-client", testToken, "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("a save that fails: %d", w.Code)
	}
}

func TestDeletingASessionGivesBackItsOtherRepositories(t *testing.T) {
	srv, _, sessionID := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`","name":"lib"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	clone := srv.projectOf(id).Repos[0].Dir
	wt := filepath.Join(convNamed(t, srv, sessionID).workspaceDir(), gitx.ReposDir, "lib")
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The preview names the work in the other repository, under its folder.
	w := get(t, srv, sessionPath(srv, sessionID, "/deletion-preview"), testToken)
	if !strings.Contains(w.Body.String(), filepath.Join(gitx.ReposDir, "lib", "new.txt")) {
		t.Fatalf("the preview must list the other repository's changes: %s", w.Body)
	}
	if w := send(t, srv, http.MethodDelete, sessionPath(srv, sessionID, ""), testToken, ""); w.Code !=
		http.StatusConflict {
		t.Fatalf("uncommitted work in another repository must stop the deletion: %d", w.Code)
	}
	if w := send(t, srv, http.MethodDelete, sessionPath(srv, sessionID, "?force=1"), testToken, ""); w.Code >= 300 {
		t.Fatalf("a confirmed deletion: %d %s", w.Code, w.Body)
	}
	if wts, _ := gitx.Worktrees(context.Background(), clone); len(wts) != 1 {
		t.Errorf("only the clone's own checkout may be left: %+v", wts)
	}
}

func TestAnUnreadableOtherRepositoryStopsTheDeletion(t *testing.T) {
	srv, _, sessionID := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`","name":"lib"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	old := worktreeInspectList
	defer func() { worktreeInspectList = old }()
	worktreeInspectList = func(ctx context.Context, dir string) ([]gitx.Change, error) {
		if filepath.Base(dir) == "lib" {
			return nil, errors.New("unreadable")
		}
		return old(ctx, dir)
	}
	if rel := srv.inspectWorktree(convNamed(t, srv, sessionID)); rel.InspectErr == nil {
		t.Errorf("an unreadable repository is not a clean one: %+v", rel)
	}
}

func TestAttachingARepositoryThatCannotBranch(t *testing.T) {
	srv, _ := projectServer(t)
	ws := t.TempDir()
	p := &Project{Dir: t.TempDir(), Repos: []ProjectRepo{{Name: "empty", Dir: t.TempDir()}}}
	srv.attachProjectRepos(context.Background(), p, ws, "s1")
	if _, err := os.Stat(filepath.Join(ws, gitx.ReposDir, "empty")); !os.IsNotExist(err) {
		t.Errorf("a repository that cannot branch is skipped: %v", err)
	}
	srv.attachProjectRepos(context.Background(), nil, ws, "s1") // no project: nothing to do
	if got := sessionRepoWorktrees(context.Background(), nil, ws); len(got) != 0 {
		t.Errorf("no project, no worktrees: %v", got)
	}
}

// noteService records what the session's agent is told about the project.
type noteService struct {
	fakeService
	note string
}

func (n *noteService) SetProjectNote(note string) { n.note = note }

func TestTheAgentIsToldAboutTheOtherRepositories(t *testing.T) {
	svc := &noteService{}
	applyRuntimeTo(svc, &Project{}, "")
	if strings.Contains(svc.note, gitx.ReposDir) {
		t.Errorf("a one-repository project names no other repository: %q", svc.note)
	}
	applyRuntimeTo(svc, &Project{Repos: []ProjectRepo{{Name: "api", GitURL: "https://x/api.git"}}}, "")
	if !strings.Contains(svc.note, gitx.ReposDir+"/api (https://x/api.git)") {
		t.Errorf("the note must name each repository: %q", svc.note)
	}
}

func TestTheIntegrationTaskNamesTheOtherRepositories(t *testing.T) {
	srv, _, sessionID := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`","name":"lib"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	ws := convNamed(t, srv, sessionID).workspaceDir()
	note := otherReposMergeNote(srv.projectOf(id), ws, sessionBranch(sessionID))
	if !strings.Contains(note, filepath.Join(ws, gitx.ReposDir, "lib")) {
		t.Errorf("the task must name the worktree of each repository: %q", note)
	}
	if otherReposMergeNote(&Project{}, ws, "b") != "" {
		t.Error("a one-repository project adds nothing")
	}
}

func convNamed(t *testing.T, srv *Server, id string) *conversation {
	t.Helper()
	c, ok := srv.lookup(id)
	if !ok {
		t.Fatalf("no session %s", id)
	}
	return c
}

func TestTheAgentIsToldWhichProjectItWorksIn(t *testing.T) {
	svc := &noteService{}
	applyRuntimeTo(svc, &Project{Title: "Shop", Dir: "/w/shop"}, "/w/worktrees/s1")
	for _, want := range []string{`project "Shop"`, "/w/worktrees/s1", "/w/shop", "it means this project"} {
		if !strings.Contains(svc.note, want) {
			t.Errorf("the note must say %q: %q", want, svc.note)
		}
	}
	// In the project's own checkout (no worktree) the root is that checkout; without a title the
	// folder names the project.
	applyRuntimeTo(svc, &Project{Dir: "/w/shop"}, "/w/shop")
	if !strings.Contains(svc.note, `project "shop"`) || !strings.Contains(svc.note, "working directory, /w/shop.") {
		t.Errorf("a session in the checkout: %q", svc.note)
	}
	if strings.Contains(svc.note, "worktree") {
		t.Errorf("no worktree to name: %q", svc.note)
	}
	if projectScopeNote(nil, "/x") != "" {
		t.Error("a session of no project gets no project note")
	}
	if n := projectScopeNote(&Project{Title: "T"}, ""); !strings.Contains(n, `"T"`) || strings.Contains(n, "Its root") {
		t.Errorf("without a directory the note names only the project: %q", n)
	}
}

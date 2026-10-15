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
	clone := filepath.Join(projectDir, "web-client")
	if p.Repos[0].Dir != clone || gitx.Repo(context.Background(), clone) != nil {
		t.Fatalf("the repository is cloned inside the project: %q", p.Repos[0].Dir)
	}
	// The main repository does not see the clone as its own change.
	if n, _ := gitx.WorkingTreeChanges(context.Background(), projectDir); n != 0 {
		t.Errorf("the main repository must ignore the other repositories, it has %d changes", n)
	}
	// The session already open gets a worktree of it, on its own branch.
	ws := convNamed(t, srv, sessionID).workspaceDir()
	wt := filepath.Join(ws, "web-client")
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
		filepath.Join(second.Workspace, "web-client")); !ok {
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
	clone := filepath.Join(projectDir, "lib")
	wt := filepath.Join(convNamed(t, srv, sessionID).workspaceDir(), "lib")
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
	wt := filepath.Join(convNamed(t, srv, sessionID).workspaceDir(), "lib")
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The preview names the work in the other repository, under its folder.
	w := get(t, srv, sessionPath(srv, sessionID, "/deletion-preview"), testToken)
	if !strings.Contains(w.Body.String(), filepath.Join("lib", "new.txt")) {
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
	if _, err := os.Stat(filepath.Join(ws, "empty")); !os.IsNotExist(err) {
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
	if strings.Contains(svc.note, "git repository") {
		t.Errorf("a one-repository project names no other repository: %q", svc.note)
	}
	applyRuntimeTo(svc, &Project{Repos: []ProjectRepo{{Name: "api", GitURL: "https://x/api.git"}}}, "")
	if !strings.Contains(svc.note, "- api (https://x/api.git)") {
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
	if !strings.Contains(note, filepath.Join(ws, "lib")) {
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

// A repository cloned before repositories became plain folders keeps its place in every session.
func TestARepositoryClonedUnderTheOldFolderKeepsItsPlace(t *testing.T) {
	p := &Project{Dir: "/proj", Repos: []ProjectRepo{
		{Name: "old", Dir: filepath.Join("/proj", gitx.ReposDir, "old")},
		{Name: "new", Dir: filepath.Join("/proj", "new")},
		{Name: "stray", Dir: "/elsewhere/stray"},
	}}
	want := []string{filepath.Join(gitx.ReposDir, "old"), "new", "stray"}
	for i, repo := range p.Repos {
		if got := repo.relPath(p.Dir); got != want[i] {
			t.Errorf("%s sits at %q, want %q", repo.Name, got, want[i])
		}
	}
	if got := sessionRepoPath("/ws", p, p.Repos[0]); got != filepath.Join("/ws", gitx.ReposDir, "old") {
		t.Errorf("a session finds the old clone at %q", got)
	}
	note := projectReposNote(p)
	for _, line := range []string{"- .motita-repos/old (", "- new (", "- stray ("} {
		if !strings.Contains(note, line) {
			t.Errorf("the note must say %q: %q", line, note)
		}
	}
}

func TestAddingARepositoryOverAFolderIsRefused(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if err := os.MkdirAll(filepath.Join(projectDir, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`","name":"taken"}`); code != http.StatusConflict {
		t.Errorf("a repository over an existing folder: %d", code)
	}
}

// bareProject is a project with no session, so nothing holds a copy of its repository.
func bareProject(t *testing.T) (*Server, string, string) {
	t.Helper()
	srv, ws := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"proj","dir":"proj"}`); code != http.StatusCreated {
		t.Fatalf("creating the project: %d", code)
	}
	dir := filepath.Join(ws, "proj")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	return srv, dir, onlyProjectID(t, srv)
}

func detachMain(t *testing.T, srv *Server, id string) (int, Project) {
	t.Helper()
	w := send(t, srv, http.MethodDelete, "/v1/projects/"+id+"/main", testToken, "")
	var p Project
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return w.Code, p
}

func TestTheMainRepositoryMovesToAFolderOfItsOwn(t *testing.T) {
	srv, projectDir, id := bareProject(t)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	// Uncommitted work travels with the repository.
	if err := os.WriteFile(filepath.Join(projectDir, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, p := detachMain(t, srv, id)
	if code != http.StatusOK || p.GitURL != "" || len(p.Repos) != 2 || p.Repos[0].Name != "proj" ||
		p.Repos[1].Name != "web-client" {
		t.Fatalf("moving the main repository: %d %+v", code, p)
	}
	moved := filepath.Join(projectDir, "proj")
	if p.Repos[0].Dir != moved || gitx.Repo(context.Background(), moved) != nil {
		t.Errorf("the repository moved to %q", p.Repos[0].Dir)
	}
	if _, err := os.Stat(filepath.Join(moved, "wip.txt")); err != nil {
		t.Errorf("uncommitted work must move with it: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "README.md")); !os.IsNotExist(err) {
		t.Errorf("the project's folder holds repositories now, not their files: %v", err)
	}
	// The other repository stayed put, and the folder is a repository that ignores them both.
	if _, err := os.Stat(filepath.Join(projectDir, "web-client", "README.md")); err != nil {
		t.Errorf("the other repository stays where it was: %v", err)
	}
	if n, _ := gitx.WorkingTreeChanges(context.Background(), projectDir); n != 0 {
		t.Errorf("the folder must ignore the repositories inside it, it has %d changes", n)
	}
	// Saved.
	if saved := srv.projectOf(id); saved == nil || saved.GitURL != "" || len(saved.Repos) != 2 {
		t.Errorf("the change is saved: %+v", saved)
	}
}

func TestTheMainRepositoryTakesTheNameOfItsURL(t *testing.T) {
	p := &Project{Dir: "/ws/folder", GitURL: "https://x/u/the-app.git"}
	if got := p.mainRepoFolder(); got != "the-app" {
		t.Errorf("from the URL: %q", got)
	}
	p.GitURL = ""
	if got := p.mainRepoFolder(); got != "folder" {
		t.Errorf("from the folder: %q", got)
	}
	dir := t.TempDir()
	q := &Project{Dir: dir, Repos: []ProjectRepo{{Name: "b"}}}
	if err := os.Mkdir(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"": "", "free": "free", "a": "a-main", "b": "b-main"} {
		if got := q.freeRepoFolder(name); got != want {
			t.Errorf("freeRepoFolder(%q) = %q, want %q", name, got, want)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "a-main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := q.freeRepoFolder("a"); got != "" {
		t.Errorf("no free name: %q", got)
	}
}

func TestTheMainRepositoryStaysWhenItCannotMove(t *testing.T) {
	if code, _ := detachMain2(t, "nope"); code != http.StatusNotFound {
		t.Errorf("an unknown project: %d", code)
	}

	srv, projectDir, sessionID := newSessionInProject(t)
	id := onlyProjectID(t, srv)

	// A running session is in the way.
	c := convNamed(t, srv, sessionID)
	c.takeRunSlot()
	if code, _ := detachMain(t, srv, id); code != http.StatusConflict {
		t.Errorf("a running session: %d", code)
	}
	c.releaseRunSlot()

	// So is a session with a copy of its own.
	created := post(t, srv, "/v1/sessions", `{"project_id":"`+id+`"}`, testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("creating a second session: %d %s", created.Code, created.Body)
	}
	var second SessionStatus
	_ = json.Unmarshal(created.Body.Bytes(), &second)
	if gitx.SamePath(second.Workspace, projectDir) {
		t.Skip("the second session runs in the project's folder")
	}
	if code, _ := detachMain(t, srv, id); code != http.StatusConflict {
		t.Errorf("a session with its own copy: %d", code)
	}
	if saved := srv.projectOf(id); len(saved.Repos) != 0 {
		t.Errorf("nothing changed: %+v", saved)
	}
}

func detachMain2(t *testing.T, id string) (int, Project) {
	srv, _ := projectServer(t)
	return detachMain(t, srv, id)
}

func TestTheMainRepositoryNeedsARepositoryAndAFreeName(t *testing.T) {
	srv, projectDir, id := bareProject(t)
	for _, name := range []string{"proj", "proj-main"} {
		if err := os.Mkdir(filepath.Join(projectDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if code, _ := detachMain(t, srv, id); code != http.StatusConflict {
		t.Errorf("no free folder name: %d", code)
	}
	if err := os.RemoveAll(filepath.Join(projectDir, ".git")); err != nil {
		t.Fatal(err)
	}
	if code, _ := detachMain(t, srv, id); code != http.StatusConflict {
		t.Errorf("a folder that is not a repository: %d", code)
	}
}

func TestMovingTheMainRepositoryFailsCleanly(t *testing.T) {
	srv, projectDir, id := bareProject(t)

	// A move that fails puts back what it had moved.
	realRename := renamePath
	n := 0
	renamePath = func(from, to string) error {
		n++
		if n == 2 {
			return errors.New("disk says no")
		}
		return realRename(from, to)
	}
	code, _ := detachMain(t, srv, id)
	renamePath = realRename
	if code != http.StatusInternalServerError {
		t.Errorf("a move that fails: %d", code)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".git")); err != nil {
		t.Errorf("the repository must be back where it was: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "proj")); !os.IsNotExist(err) {
		t.Errorf("the half-made folder is removed: %v", err)
	}

	// The folder that cannot be made or read, asked of the move itself.
	p := &Project{Dir: filepath.Join(t.TempDir(), "missing")}
	if err := moveMainRepo(p, "x"); err == nil {
		t.Error("a project folder that is not there")
	}
	if err := moveMainRepo(&Project{Dir: projectDir}, "README.md"); err == nil {
		t.Error("a folder name that is taken")
	}

	// The new folder cannot be made a repository.
	realInit := initRepo
	initRepo = func(context.Context, string, string) error { return errors.New("no git") }
	code, _ = detachMain(t, srv, id)
	initRepo = realInit
	if code != http.StatusInternalServerError {
		t.Errorf("a folder that cannot be initialised: %d", code)
	}
}

func TestTheMainRepositoryMoveCannotBeSaved(t *testing.T) {
	srv, _, id := bareProject(t)
	if err := os.MkdirAll(filepath.Join(srv.projects.dir, id+".json.tmp", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := detachMain(t, srv, id); code != http.StatusInternalServerError {
		t.Errorf("a save that fails: %d", code)
	}
}

// A project saved without a main branch, with a session that runs in its folder: the move names
// the branch itself and tells the session about its new repositories.
func TestTheMainRepositoryMoveNamesTheBranchAndTellsTheSessions(t *testing.T) {
	srv, projectDir, id := bareProject(t)
	p := srv.projectOf(id)
	p.MainBranch = ""
	if err := srv.projects.save(*p); err != nil {
		t.Fatal(err)
	}
	created := post(t, srv, "/v1/sessions", `{"project_id":"`+id+`"}`, testToken)
	var st SessionStatus
	_ = json.Unmarshal(created.Body.Bytes(), &st)
	c := convNamed(t, srv, st.ID)
	// The session's own copy is given back, and it goes on in the project's folder.
	if st.Workspace != projectDir {
		mustGit(t, projectDir, "worktree", "remove", "--force", st.Workspace)
		c.stateMu.Lock()
		c.workspace = projectDir
		c.stateMu.Unlock()
	}
	svc := &noteService{}
	c.svc = svc
	code, moved := detachMain(t, srv, id)
	if code != http.StatusOK || moved.MainBranch != defaultMainBranch {
		t.Fatalf("moving: %d %+v", code, moved)
	}
	if !strings.Contains(svc.note, "- proj (") {
		t.Errorf("the session must be told about the repositories: %q", svc.note)
	}
}

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

// A repository the user already has on the machine is added to a project by its folder: it is used
// where it is, with worktrees of its own, and nothing the user has in it moves.

// localProject is a whole-disk gateway with one project, "proj", that has a session.
func localProject(t *testing.T) (srv *Server, projectDir, id, sessionID string) {
	t.Helper()
	srv, ws := fullDiskServer(t)
	projectDir = filepath.Join(ws, "proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, projectDir)
	if code, out := createProject(t, srv, `{"title":"proj","dir":"proj"}`); code != http.StatusCreated {
		t.Fatalf("creating the project: %d %v", code, out)
	}
	id = onlyProjectID(t, srv)
	created := post(t, srv, "/v1/sessions", `{"project_id":"`+id+`"}`, testToken)
	var st SessionStatus
	_ = json.Unmarshal(created.Body.Bytes(), &st)
	return srv, projectDir, id, st.ID
}

// usersRepo is a repository the user works in: a clone with an origin that has moved on, on a
// feature branch, with a change not yet committed.
func usersRepo(t *testing.T) (dir, upstream string) {
	t.Helper()
	root := t.TempDir()
	upstream = filepath.Join(root, "upstream")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, upstream)
	dir = filepath.Join(root, "bridges")
	mustGit(t, root, "clone", "-q", upstream, dir)
	mustGit(t, dir, "checkout", "-q", "-b", "feature/mine")
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upstream, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, upstream, "add", "-A")
	mustGit(t, upstream, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "later")
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real, upstream
}

// snapshotOf is everything the user would notice changing in their repository: where HEAD is, the
// refs of the branches they have, and their working tree.
func snapshotOf(t *testing.T, dir string) string {
	t.Helper()
	return mustGit(t, dir, "rev-parse", "HEAD") + "|" + mustGit(t, dir, "symbolic-ref", "HEAD") + "|" +
		mustGit(t, dir, "rev-parse", "main", "feature/mine") + "|" + mustGit(t, dir, "status", "--porcelain")
}

func TestALocalRepositoryIsUsedWhereItIsAndNothingOfTheUsersMoves(t *testing.T) {
	srv, projectDir, id, sessionID := localProject(t)
	local, _ := usersRepo(t)
	before := snapshotOf(t, local)

	code, p := addRepo(t, srv, id, `{"path":"`+local+`"}`)
	if code != http.StatusCreated || len(p.Repos) != 1 {
		t.Fatalf("adding a local repository: %d %+v", code, p)
	}
	repo := p.Repos[0]
	if repo.Name != "bridges" || repo.Dir != local || repo.GitURL == "" {
		t.Fatalf("the repository is registered where it is: %+v", repo)
	}
	if !repo.Local || !repo.Shared || repo.MainBranch != "main" || repo.ProjectBranch != "project/proj" {
		t.Fatalf("the repository is local, shared and on the project's branch: %+v", repo)
	}
	// No clone: the catalog has nothing, and the project's folder is a worktree of the user's repository.
	if _, err := os.Stat(filepath.Join(srv.opts.WorkspaceDir, catalogFolder)); err == nil {
		t.Error("a local repository must not be cloned into the catalog")
	}
	ctx := context.Background()
	if got := gitx.Display(ctx, filepath.Join(projectDir, "bridges")); got != "project/proj" {
		t.Errorf("the project's folder is on %q", got)
	}
	// The session gets its own worktree of it, from the user's repository.
	ws := convNamed(t, srv, sessionID).workspaceDir()
	if _, ok, _ := gitx.LiveWorktreeAt(ctx, local, filepath.Join(ws, "bridges")); !ok {
		t.Error("the session must get a worktree of the user's repository")
	}
	// Everything a background refresh and a sync would do, none of it reaches the user's checkout.
	srv.syncSharedRepos(ctx, srv.projectOf(id))
	if w := send(t, srv, http.MethodPost, "/v1/projects/"+id+"/repos/bridges/sync", testToken, ""); w.Code >= 500 {
		t.Errorf("syncing: %d %s", w.Code, w.Body)
	}
	if after := snapshotOf(t, local); after != before {
		t.Errorf("the user's repository moved:\nbefore %s\nafter  %s", before, after)
	}

	// Taking it out of the project removes motita's worktree and branch, and nothing else.
	if w := doDelete(t, srv, "/v1/projects/"+id+"/repos/bridges?force=1"); w.Code != http.StatusOK {
		t.Fatalf("removing: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("the user's repository must stay: %v", err)
	}
	if gitx.BranchExists(ctx, local, "project/proj") {
		t.Error("the project's branch goes with it")
	}
	if after := snapshotOf(t, local); after != before {
		t.Errorf("the user's repository moved when it was removed:\nbefore %s\nafter  %s", before, after)
	}
}

func TestDeletingAProjectLeavesItsLocalRepositoryAlone(t *testing.T) {
	srv, _, id, _ := localProject(t)
	local, _ := usersRepo(t)
	if code, _ := addRepo(t, srv, id, `{"path":"`+local+`","name":"api"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	before := snapshotOf(t, local)
	if w := doDelete(t, srv, "/v1/projects/"+id); w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if after := snapshotOf(t, local); after != before {
		t.Errorf("the user's repository moved:\nbefore %s\nafter  %s", before, after)
	}
	if gitx.BranchExists(context.Background(), local, "project/proj") {
		t.Error("the project's branch must go with the project")
	}
}

func TestALocalRepositoryIsValidated(t *testing.T) {
	srv, projectDir, id, _ := localProject(t)
	local, _ := usersRepo(t)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, empty, "init", "-q")
	if err := os.MkdirAll(filepath.Join(local, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	hidden := filepath.Join(t.TempDir(), ".hidden")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		body string
		want int
	}{
		"relative":       {`{"path":"some/dir"}`, http.StatusBadRequest},
		"missing":        {`{"path":"` + filepath.Join(local, "nope") + `"}`, http.StatusBadRequest},
		"inside a repo":  {`{"path":"` + filepath.Join(local, "sub") + `"}`, http.StatusBadRequest},
		"no commits":     {`{"path":"` + empty + `"}`, http.StatusBadRequest},
		"hidden name":    {`{"path":"` + hidden + `"}`, http.StatusBadRequest},
		"the project":    {`{"path":"` + projectDir + `"}`, http.StatusConflict},
		"around project": {`{"path":"` + filepath.Dir(projectDir) + `"}`, http.StatusBadRequest},
	}
	for name, c := range cases {
		if code, _ := addRepo(t, srv, id, c.body); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
	// A name that is a path is refused, and a taken folder name too.
	if code, _ := addRepo(t, srv, id, `{"path":"`+local+`","name":"../up"}`); code != http.StatusBadRequest {
		t.Errorf("a name that is a path: %d", code)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := addRepo(t, srv, id, `{"path":"`+local+`","name":"taken"}`); code != http.StatusConflict {
		t.Errorf("a folder in the way: %d", code)
	}
	if code, p := addRepo(t, srv, id, `{"path":"`+local+`"}`); code != http.StatusCreated || len(p.Repos) != 1 {
		t.Fatalf("adding: %d", code)
	}
	if code, _ := addRepo(t, srv, id, `{"path":"`+local+`","name":"other"}`); code != http.StatusConflict {
		t.Errorf("the same repository twice: %d", code)
	}
	other, _ := usersRepo(t)
	if code, _ := addRepo(t, srv, id, `{"path":"`+other+`","name":"bridges"}`); code != http.StatusConflict {
		t.Errorf("the same name twice: %d", code)
	}
}

func TestALocalRepositoryNeedsTheWholeDisk(t *testing.T) {
	srv, _, _ := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	local, _ := usersRepo(t)
	if code, _ := addRepo(t, srv, id, `{"path":"`+local+`"}`); code != http.StatusForbidden {
		t.Errorf("workspace-only access must refuse a folder outside it: %d", code)
	}
}

func TestALocalRepositoryThatCannotBeBranchedIsReported(t *testing.T) {
	srv, projectDir, id, _ := localProject(t)
	local, _ := usersRepo(t)
	// The project's folder was replaced by a file: no worktree can go inside it.
	if err := os.RemoveAll(projectDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := addRepo(t, srv, id, `{"path":"`+local+`"}`); code != http.StatusInternalServerError {
		t.Errorf("a worktree that cannot be made: %d", code)
	}
}

func TestTheAgentIsToldTheLocalRepositoryIsTheUsers(t *testing.T) {
	p := &Project{Dir: "/w/p", Repos: []ProjectRepo{{Name: "bridges", Dir: "/home/u/bridges", Shared: true, Local: true}}}
	note := projectReposNote(p)
	for _, want := range []string{"the user's own repository", "/home/u/bridges", "never check out, merge, reset"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note must say %q: %q", want, note)
		}
	}
	if !p.isLocalRepo("/home/u/bridges") || p.isLocalRepo("/home/u/other") {
		t.Error("isLocalRepo must name the local repositories only")
	}
}

func TestTheIntegrationTaskLeavesALocalRepositoryUnmerged(t *testing.T) {
	srv, _, id, sessionID := localProject(t)
	local, _ := usersRepo(t)
	if code, _ := addRepo(t, srv, id, `{"path":"`+local+`"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	ws := convNamed(t, srv, sessionID).workspaceDir()
	note := otherReposMergeNote(srv.projectOf(id), ws, sessionBranch(sessionID))
	if !strings.Contains(note, "user's own repository") || !strings.Contains(note, "do not merge it") {
		t.Errorf("the task must not merge into the user's repository: %q", note)
	}
}

func TestMissingDependenciesAreListedAndToldToTheAgent(t *testing.T) {
	srv, projectDir, id, _ := localProject(t)
	write := func(rel, body string) {
		path := filepath.Join(projectDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("composer.json", `{"require":{"laravel/framework":"^10"}}`)
	local, _ := usersRepo(t)
	write2 := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(local, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write2("package.json", `{"devDependencies":{"vite":"^5"}}`)
	if err := os.MkdirAll(filepath.Join(local, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	write2("web/package.json", `{"dependencies":{"react":"^18"}}`)
	if code, _ := addRepo(t, srv, id, `{"path":"`+local+`"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}

	w := get(t, srv, "/v1/projects", testToken)
	var list struct {
		Projects []Project `json:"projects"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Projects) != 1 || len(list.Projects[0].MissingDeps) != 3 {
		t.Fatalf("the list must say what is not installed: %s", w.Body)
	}
	got := map[string]string{}
	for _, m := range list.Projects[0].MissingDeps {
		got[m.Path] = m.Command
	}
	if got["."] != "composer install" || got["bridges"] != "npm install" || got["bridges/web"] != "npm install" {
		t.Errorf("missing: %v", got)
	}

	note := dependenciesNote(srv.projectOf(id))
	for _, want := range []string{"NOT installed", "`composer install`", "bridges: node_modules/", "php artisan test",
		"the check was NOT RUN", "Never write a stand-in"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note must say %q: %q", want, note)
		}
	}

	// Installed, nothing is said.
	write("vendor/autoload.php", "<?php")
	for _, dir := range []string{"node_modules", "web/node_modules"} {
		if err := os.MkdirAll(filepath.Join(local, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if note := dependenciesNote(srv.projectOf(id)); note != "" {
		t.Errorf("nothing is missing: %q", note)
	}
	if dependenciesNote(nil) != "" || missingDeps(&Project{}) != nil {
		t.Error("no project, nothing to say")
	}
}

func TestTheMainBranchOfALocalRepositoryIsReadNeverSwitchedTo(t *testing.T) {
	ctx := context.Background()
	local, _ := usersRepo(t)
	// main and master are recognised; any other, the branch the user is on is the answer.
	if got := localMainBranch(ctx, local); got != "main" {
		t.Errorf("main: %q", got)
	}
	mustGit(t, local, "branch", "-m", "main", "master")
	if got := localMainBranch(ctx, local); got != "master" {
		t.Errorf("master: %q", got)
	}
	mustGit(t, local, "branch", "-m", "master", "trunk")
	if got := localMainBranch(ctx, local); got != "feature/mine" {
		t.Errorf("the branch the user has out: %q", got)
	}
	if got := mustGit(t, local, "symbolic-ref", "--short", "HEAD"); got != "feature/mine" {
		t.Errorf("reading the main branch must not switch: on %q", got)
	}
}

func TestAFolderOfTheUsersOwnIsNotSwitchedOrUpdatedByItself(t *testing.T) {
	srv, _ := fullDiskServer(t)
	local, _ := usersRepo(t)
	if code, out := createProject(t, srv, `{"title":"mine","path":"`+local+`"}`); code != http.StatusCreated {
		t.Fatalf("creating: %d %v", code, out)
	}
	p := srv.projectOf(onlyProjectID(t, srv))
	ctx := context.Background()
	mainBefore := mustGit(t, local, "rev-parse", "main")
	// A clean tree, so that only the rule keeps the branch where it is.
	if err := os.Remove(filepath.Join(local, "wip.txt")); err != nil {
		t.Fatal(err)
	}

	// Back to the main branch and the background update leave the user's checkout alone...
	srv.returnToMain(p)
	if got := mustGit(t, local, "symbolic-ref", "--short", "HEAD"); got != "feature/mine" {
		t.Errorf("the user's branch was switched to %q", got)
	}
	if w := srv.refreshMain(ctx, p, false); w != "" || mustGit(t, local, "rev-parse", "main") != mainBefore {
		t.Errorf("the background update moved the user's main branch (%q)", w)
	}
	// ...and "update now" is the person asking, so it moves the branch forward.
	if w := srv.refreshMain(ctx, p, true); w != "" || mustGit(t, local, "rev-parse", "main") == mainBefore {
		t.Errorf("an explicit update must bring main up to date (%q)", w)
	}
}

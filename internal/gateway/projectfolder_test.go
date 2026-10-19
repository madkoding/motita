package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestAProjectCannotTakeTheWorkspacesOwnFolders: worktrees/ and sessions/ hold every session of
// every project; a project there would work among them, and deleting it would delete them.
func TestAProjectCannotTakeTheWorkspacesOwnFolders(t *testing.T) {
	srv, _ := projectServer(t)
	for _, dir := range []string{"worktrees", "Sessions"} {
		if code, out := createProject(t, srv, `{"title":"x","dir":"`+dir+`"}`); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", dir, code, out)
		}
	}
}

// TestTwoProjectsNeverShareAFolder: a second project on the folder of another (the same name, or
// a clone of the same repository that would reuse it) is refused, so no two projects share a
// checkout, its branches and its skills.
func TestTwoProjectsNeverShareAFolder(t *testing.T) {
	srv, _ := projectServer(t)
	if code, out := createProject(t, srv, `{"title":"Shop","dir":"shop"}`); code != http.StatusCreated {
		t.Fatalf("first: %d %v", code, out)
	}
	if code, out := createProject(t, srv, `{"title":"Again","dir":"shop"}`); code != http.StatusConflict {
		t.Errorf("the same folder twice: %d %v", code, out)
	}

	src := sourceRepo(t)
	if code, out := createProject(t, srv, `{"title":"A","dir":"web","git_url":"`+src+`"}`); code != http.StatusCreated {
		t.Fatalf("clone: %d %v", code, out)
	}
	if code, out := createProject(t, srv, `{"title":"B","dir":"web","git_url":"`+src+`"}`); code != http.StatusConflict {
		t.Errorf("a clone that would reuse another project's folder: %d %v", code, out)
	}
}

// TestProjectInDirSeesNestingBothWays: a folder inside another project, or around one, is that
// project's too.
func TestProjectInDirSeesNestingBothWays(t *testing.T) {
	srv, ws := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"Shop","dir":"shop"}`); code != http.StatusCreated {
		t.Fatal(code)
	}
	shop := filepath.Join(ws, "shop")
	for _, dir := range []string{shop, filepath.Join(shop, "sub"), ws} {
		if p := srv.projectInDir(dir, ""); p == nil || p.Title != "Shop" {
			t.Errorf("%s: %v", dir, p)
		}
	}
	if p := srv.projectInDir(filepath.Join(t.TempDir(), "other"), ""); p != nil {
		t.Errorf("an unrelated folder: %v", p)
	}
	if p := srv.projectInDir(shop, onlyProjectID(t, srv)); p != nil {
		t.Errorf("the project itself is excepted: %v", p)
	}
	if p := srv.projectInDir("", ""); p != nil {
		t.Error("no folder, no project")
	}
	srv.projects = nil
	if p := srv.projectInDir(shop, ""); p != nil {
		t.Error("no store, no project")
	}
}

// TestDeletingAProjectKeepsAFolderAnotherOneStandsOn: a record from before folders were refused
// twice can share one; deleting either keeps the folder for the other.
func TestDeletingAProjectKeepsAFolderAnotherOneStandsOn(t *testing.T) {
	srv, ws := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"Shop","dir":"shop"}`); code != http.StatusCreated {
		t.Fatal(code)
	}
	id := onlyProjectID(t, srv)
	shop := filepath.Join(ws, "shop")
	if err := srv.projects.save(Project{ID: "legacytwin", Title: "Twin", Dir: shop}); err != nil {
		t.Fatal(err)
	}
	if w := deleteReq(t, srv, "/v1/projects/"+id, testToken); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(shop); err != nil {
		t.Errorf("the folder is the other project's too and must stay: %v", err)
	}
}

// TestRestoreKeepsEverySessionInItsPlace: a record that names its project but not its folder goes
// back to the project; a free session from the days of the shared root gets a folder of its own
// instead of the root that holds every project; any other free session keeps its folder.
func TestRestoreKeepsEverySessionInItsPlace(t *testing.T) {
	srv, ws := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"Shop","dir":"shop"}`); code != http.StatusCreated {
		t.Fatal(code)
	}
	id := onlyProjectID(t, srv)
	if !srv.restoreSession(sessionRecord{ID: "legacyproj", ProjectID: id}) {
		t.Fatal("not restored")
	}
	if c := convNamed(t, srv, "legacyproj"); c.projectDir != filepath.Join(ws, "shop") {
		t.Errorf("the project's folder must be read from the project: %q", c.projectDir)
	}

	if !srv.restoreSession(sessionRecord{ID: "legacyfree", Workspace: ws}) {
		t.Fatal("not restored")
	}
	if got := convNamed(t, srv, "legacyfree").workspaceDir(); got != srv.freeSessionDir("legacyfree") {
		t.Errorf("a free session restored into the root must get its own folder: %q", got)
	}
	own := t.TempDir()
	if got := srv.restoredFreeWorkspace(sessionRecord{ID: "f", Workspace: own}); got != own {
		t.Errorf("a free session keeps its folder: %q", got)
	}

	// A root where the session's folder cannot be made (a file) keeps the record's directory.
	blocked := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.opts.WorkspaceDir = blocked
	if got := srv.restoredFreeWorkspace(sessionRecord{ID: "x", Workspace: blocked}); got != blocked {
		t.Errorf("an uncreatable folder keeps the record's: %q", got)
	}
}

// TestTheDefaultSessionWorksInAFolderOfItsOwn: the default session (and every schedule that names
// no session) is not left in the workspace root.
func TestTheDefaultSessionWorksInAFolderOfItsOwn(t *testing.T) {
	srv, _ := projectServer(t)
	def, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("no default session")
	}
	if got := def.workspaceDir(); got != srv.freeSessionDir(DefaultSession) {
		t.Errorf("default session works in %q", got)
	}
}

// TestAnIntegratedSessionRunsNothing: a schedule or a resumed run cannot write on a session whose
// work is already in the project.
func TestAnIntegratedSessionRunsNothing(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	c := newConversation("merged1", &fakeService{})
	c.setMerged("abc")
	if _, ok := srv.startDetachedRun(c, "a task", "task", "", srv.unattendedApprover("merged1")); ok {
		t.Error("an integrated session must not run")
	}
}

// TestThePullRequestGoesToTheProjectsRepository: the repository a pull request lands on is the
// one the project was cloned from, whatever the checkout's origin says; a project with no
// recorded URL asks the checkout, and a checkout with no origin has none.
func TestThePullRequestGoesToTheProjectsRepository(t *testing.T) {
	srv, ws := projectServer(t)
	if code, _ := createProject(t, srv, `{"title":"Shop","dir":"shop"}`); code != http.StatusCreated {
		t.Fatal(code)
	}
	id := onlyProjectID(t, srv)
	c := &conversation{id: "s", projectID: id, workspace: filepath.Join(ws, "shop")}
	if got := srv.repoRemote(context.Background(), c, c.workspace); got != "" {
		t.Errorf("no URL and no origin: %q", got)
	}
	mustGit(t, c.workspace, "remote", "add", "origin", "https://example.com/elsewhere.git")
	if got := srv.repoRemote(context.Background(), c, c.workspace); got != "https://example.com/elsewhere.git" {
		t.Errorf("no URL recorded: the checkout's origin, got %q", got)
	}
	p := srv.projectOf(id)
	p.GitURL = "https://example.com/shop.git"
	if err := srv.projects.save(*p); err != nil {
		t.Fatal(err)
	}
	if got := srv.repoRemote(context.Background(), c, c.workspace); got != "https://example.com/shop.git" {
		t.Errorf("the project's own repository wins: %q", got)
	}
}

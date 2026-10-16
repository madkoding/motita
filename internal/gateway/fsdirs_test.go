package gateway

import (
	"encoding/json"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// fullDiskServer is a gateway whose Security setting lets the agent reach the whole disk.
func fullDiskServer(t *testing.T) (*Server, string) {
	t.Helper()
	ws := t.TempDir()
	svc := &fakeService{}
	svc.cfg.Sandbox.DiskAccess = config.DiskFull
	srv := newTestServer(t, svc, func(o *Options) {
		o.WorkspaceDir = ws
		o.ProjectDir = filepath.Join(ws, "projects")
		o.SessionDir = filepath.Join(ws, "sessions")
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	return srv, ws
}

func listDirs(t *testing.T, srv *Server, path string) (int, dirListing, string) {
	t.Helper()
	url := "/v1/fs/dirs"
	if path != "" {
		url += "?path=" + neturl.QueryEscape(path)
	}
	w := get(t, srv, url, testToken)
	var out dirListing
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out, w.Body.String()
}

// TestTheFolderBrowserListsOnlyFoldersAndReachesTheWholeDiskWhenAllowed: files and hidden folders
// are left out, git folders are marked, and a folder anywhere on the disk can be listed.
func TestTheFolderBrowserListsOnlyFoldersAndReachesTheWholeDiskWhenAllowed(t *testing.T) {
	srv, _ := fullDiskServer(t)
	base := t.TempDir()
	for _, d := range []string{"b-folder", "A-repo/.git", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "file.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, body := listDirs(t, srv, base)
	if code != http.StatusOK || len(out.Dirs) != 2 {
		t.Fatalf("%d %s", code, body)
	}
	if got := out.Dirs[0]; got.Name != "A-repo" || !got.Git {
		t.Errorf("a repository is marked: %+v", got)
	}
	if got := out.Dirs[1]; got.Name != "b-folder" || got.Git {
		t.Errorf("a plain folder is not: %+v", got)
	}
	if out.Parent != filepath.Dir(base) || out.Root != "/" {
		t.Errorf("parent and root: %+v", out)
	}
}

// TestTheFolderBrowserStartsAtTheHomeFolderWhenThereIsOne: with no path it opens on the user's home,
// and on the top of the disk when there is none.
func TestTheFolderBrowserStartsAtTheHomeFolderWhenThereIsOne(t *testing.T) {
	srv, _ := fullDiskServer(t)
	base := t.TempDir()
	t.Setenv("HOME", base)
	if code, out, body := listDirs(t, srv, ""); code != http.StatusOK || out.Path != base || out.Home != base {
		t.Errorf("home: %d %s", code, body)
	}
	t.Setenv("HOME", "")
	code, out, _ := listDirs(t, srv, "")
	if code != http.StatusOK || out.Path != "/" {
		t.Errorf("no home starts at the root: %d %+v", code, out)
	}
	if out.Parent != "" || out.Home != "" {
		t.Errorf("the top has no parent and there is no home: %+v", out)
	}
	if code, _, _ = listDirs(t, srv, filepath.Join(base, "nope")); code != http.StatusNotFound {
		t.Errorf("a missing folder: %d", code)
	}
}

// TestTheFolderBrowserStaysInTheWorkspaceUnlessTheWholeDiskIsAllowed: the default setting keeps
// the browser inside the workspace.
func TestTheFolderBrowserStaysInTheWorkspaceUnlessTheWholeDiskIsAllowed(t *testing.T) {
	srv, ws := projectServer(t)
	if err := os.MkdirAll(filepath.Join(ws, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, body := listDirs(t, srv, "")
	if code != http.StatusOK || out.Path != ws || out.Root != ws || out.Parent != "" {
		t.Fatalf("the default is the workspace: %d %s", code, body)
	}
	if code, _, _ = listDirs(t, srv, filepath.Join(ws, "shop")); code != http.StatusOK {
		t.Errorf("a folder inside: %d", code)
	}
	for _, p := range []string{t.TempDir(), "/", ws + "/../x"} {
		if code, _, body = listDirs(t, srv, p); code != http.StatusForbidden {
			t.Errorf("%s: %d %s", p, code, body)
		}
	}
	noWS := newTestServer(t, &fakeService{}, func(o *Options) { o.WorkspaceDir = "" })
	if code, _, _ = listDirs(t, noWS, ""); code != http.StatusNotImplemented {
		t.Errorf("no workspace: %d", code)
	}
}

// TestAProjectCanLiveOutsideTheWorkspaceOnlyWithTheWholeDisk: the folder is refused under the
// default setting, and with the whole disk it is used as it is, never deleted with the project.
func TestAProjectCanLiveOutsideTheWorkspaceOnlyWithTheWholeDisk(t *testing.T) {
	outside := t.TempDir()
	srv, _ := projectServer(t)
	if code, out := createProject(t, srv, `{"title":"x","path":"`+outside+`"}`); code != http.StatusForbidden {
		t.Fatalf("workspace-only: %d %v", code, out)
	}

	srv, ws := fullDiskServer(t)
	for _, bad := range []string{`"relative/dir"`, `"` + outside + `/missing"`, `"/"`, `"` + ws + `"`,
		`"` + filepath.Join(ws, "worktrees") + `"`} {
		if err := os.MkdirAll(filepath.Join(ws, "worktrees"), 0o755); err != nil {
			t.Fatal(err)
		}
		if code, out := createProject(t, srv, `{"title":"x","path":`+bad+`}`); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", bad, code, out)
		}
	}
	code, out := createProject(t, srv, `{"title":"Out","path":"`+outside+`"}`)
	real, _ := filepath.EvalSymlinks(outside)
	if code != http.StatusCreated || out["dir"] != real {
		t.Fatalf("create: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(real, ".git")); err != nil {
		t.Errorf("an empty folder becomes a repository: %v", err)
	}
	if code, _ := createProject(t, srv, `{"title":"Again","path":"`+outside+`"}`); code != http.StatusConflict {
		t.Errorf("a second project on the same folder: %d", code)
	}
	id, _ := out["id"].(string)
	if w := doDelete(t, srv, "/v1/projects/"+id); w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(real); err != nil {
		t.Errorf("a folder outside the workspace is never deleted: %v", err)
	}
}

// TestAProjectWithoutSessionsCanMoveToAnotherFolder: editing a project points it at a folder
// outside the workspace; sessions, other repositories, taken folders and the default setting
// refuse it.
func TestAProjectWithoutSessionsCanMoveToAnotherFolder(t *testing.T) {
	srv, _ := fullDiskServer(t)
	code, out := createProject(t, srv, `{"title":"Shop","dir":"shop"}`)
	if code != http.StatusCreated {
		t.Fatal(code, out)
	}
	id := out["id"].(string)
	target := t.TempDir()
	real, _ := filepath.EvalSymlinks(target)

	patch := func(body string) int { return patchProject(t, srv, id, body).Code }
	if c := patch(`{"path":"relative"}`); c != http.StatusBadRequest {
		t.Errorf("relative: %d", c)
	}
	if c := patch(`{"path":"` + target + `"}`); c != http.StatusOK {
		t.Fatalf("move: %d", c)
	}
	if p := srv.projectOf(id); p == nil || p.Dir != real {
		t.Fatalf("project = %+v", p)
	}
	if c := patch(`{"path":"` + target + `"}`); c != http.StatusOK {
		t.Errorf("the same folder is a no-op: %d", c)
	}
	if c := patch(`{"title":"Renamed"}`); c != http.StatusOK {
		t.Errorf("a patch with no path leaves the folder: %d", c)
	}

	other := t.TempDir()
	if code, _ := createProject(t, srv, `{"title":"Other","path":"`+other+`"}`); code != http.StatusCreated {
		t.Fatal(code)
	}
	if c := patch(`{"path":"` + other + `"}`); c != http.StatusConflict {
		t.Errorf("a folder of another project: %d", c)
	}

	if _, err := srv.projects.update(id, func(p *Project) { p.Repos = []ProjectRepo{{Name: "api"}} }); err != nil {
		t.Fatal(err)
	}
	if c := patch(`{"path":"` + t.TempDir() + `"}`); c != http.StatusConflict {
		t.Errorf("a project with other repositories: %d", c)
	}
}

func TestTheFolderOfAProjectWithSessionsDoesNotMove(t *testing.T) {
	srv, _ := fullDiskServer(t)
	_, out := createProject(t, srv, `{"title":"Shop","dir":"shop"}`)
	id := out["id"].(string)
	if w := post(t, srv, "/v1/sessions", `{"project_id":"`+id+`"}`, testToken); w.Code >= 300 {
		t.Fatalf("session: %d %s", w.Code, w.Body.String())
	}
	if c := patchProject(t, srv, id, `{"path":"`+t.TempDir()+`"}`).Code; c != http.StatusConflict {
		t.Errorf("a project with sessions: %d", c)
	}
}

func TestTheWorkspaceOnlySettingRefusesToMoveAProject(t *testing.T) {
	srv, _ := projectServer(t)
	_, out := createProject(t, srv, `{"title":"Shop","dir":"shop"}`)
	if c := patchProject(t, srv, out["id"].(string), `{"path":"`+t.TempDir()+`"}`).Code; c != http.StatusForbidden {
		t.Errorf("%d", c)
	}
}

func TestTheListingMarksTheSystemsOwnFolders(t *testing.T) {
	srv, _ := fullDiskServer(t)
	if _, out, _ := listDirs(t, srv, "/etc"); !out.Sensitive {
		t.Errorf("/etc is the system's: %+v", out)
	}
	if _, out, _ := listDirs(t, srv, t.TempDir()); out.Sensitive {
		t.Errorf("a temp folder is not: %+v", out)
	}
}

func makeDir(t *testing.T, srv *Server, body string) int {
	t.Helper()
	return post(t, srv, "/v1/fs/dirs", body, testToken).Code
}

// TestAFolderCanBeCreatedFromTheBrowser: a simple name inside an existing folder, with the whole
// disk allowed; paths, repeats and the default setting are refused.
func TestAFolderCanBeCreatedFromTheBrowser(t *testing.T) {
	srv, _ := fullDiskServer(t)
	base := t.TempDir()
	if c := makeDir(t, srv, `{"path":"`+base+`","name":"fresh"}`); c != http.StatusCreated {
		t.Fatalf("create: %d", c)
	}
	if !isDir(filepath.Join(base, "fresh")) {
		t.Error("the folder exists")
	}
	if c := makeDir(t, srv, `{"path":"`+base+`","name":"fresh"}`); c != http.StatusConflict {
		t.Errorf("a repeat: %d", c)
	}
	for _, body := range []string{
		`{"path":"` + base + `","name":"a/b"}`, `{"path":"` + base + `","name":".."}`,
		`{"path":"` + base + `","name":"  "}`, `{"path":"rel","name":"x"}`,
		`{"path":"` + base + `/nope","name":"x"}`,
	} {
		if c := makeDir(t, srv, body); c != http.StatusBadRequest {
			t.Errorf("%s: %d", body, c)
		}
	}
	plain, _ := projectServer(t)
	if c := makeDir(t, plain, `{"path":"`+base+`","name":"x"}`); c != http.StatusForbidden {
		t.Errorf("workspace-only: %d", c)
	}
	if c := makeDir(t, srv, `{bad`); c != http.StatusBadRequest {
		t.Errorf("a malformed body: %d", c)
	}
}

// TestAFolderThatCannotBeOpenedIsReportedNotListedEmpty: as a user who may not read it. The root
// user reads everything, so there the test has nothing to show.
func TestAFolderThatCannotBeOpenedIsReportedNotListedEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any folder")
	}
	srv, _ := fullDiskServer(t)
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if code, _, _ := listDirs(t, srv, locked); code != http.StatusNotFound {
		t.Errorf("listing: %d", code)
	}
	if c := makeDir(t, srv, `{"path":"`+locked+`","name":"x"}`); c != http.StatusConflict {
		t.Errorf("creating inside: %d", c)
	}
}

// withDrives makes the gateway think the machine has several drives, as a Windows one does.
func withDrives(t *testing.T, roots ...string) {
	t.Helper()
	old := driveRoots
	driveRoots = func() []string { return roots }
	t.Cleanup(func() { driveRoots = old })
}

// TestAMachineWithSeveralDrivesListsThemAboveTheTopOfEach: the top of a drive has the list of
// drives above it, and the list has nothing to choose or to create in.
func TestAMachineWithSeveralDrivesListsThemAboveTheTopOfEach(t *testing.T) {
	withDrives(t, `C:\`, `D:\`)
	srv, _ := fullDiskServer(t)
	code, out, body := listDirs(t, srv, "%40drives")
	if code != http.StatusOK || out.Path != "" || len(out.Dirs) != 2 || out.Dirs[0].Name != "C:" || out.Dirs[1].Path != `D:\` {
		t.Fatalf("drives: %d %s", code, body)
	}
	if _, out, _ = listDirs(t, srv, "/"); out.Parent != drivesPath {
		t.Errorf("above the top of a drive: %+v", out)
	}
	if _, out, _ = listDirs(t, srv, "/usr"); out.Parent != "/" {
		t.Errorf("an ordinary parent: %+v", out)
	}
}

// TestRelativeFoldersAndDollarFoldersAreLeftOut: a relative path is refused, not read from wherever
// the gateway runs, and system folders that start with $ are not offered.
func TestRelativeFoldersAndDollarFoldersAreLeftOut(t *testing.T) {
	srv, _ := fullDiskServer(t)
	if code, _, _ := listDirs(t, srv, "relative/dir"); code != http.StatusBadRequest {
		t.Errorf("relative: %d", code)
	}
	if code, _, _ := listDirs(t, srv, "%40drives"); code != http.StatusBadRequest {
		t.Errorf("drives on a machine with one tree: %d", code)
	}
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "$Recycle.Bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := listDirs(t, srv, base); len(out.Dirs) != 0 {
		t.Errorf("hidden: %+v", out.Dirs)
	}
}

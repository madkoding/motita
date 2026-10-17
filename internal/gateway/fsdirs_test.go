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
	for _, bad := range []string{`"relative/dir"`, `"` + outside + `/a/b"`, `"/"`, `"` + ws + `"`,
		`"` + filepath.Join(ws, "worktrees") + `"`} {
		if err := os.MkdirAll(filepath.Join(ws, "worktrees"), 0o755); err != nil {
			t.Fatal(err)
		}
		if code, out := createProject(t, srv, `{"title":"x","path":`+bad+`}`); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", bad, code, out)
		}
	}
	fresh := filepath.Join(t.TempDir(), "fresh")
	code, out := createProject(t, srv, `{"title":"New","path":"`+fresh+`"}`)
	if code != http.StatusCreated || !isDir(fresh) {
		t.Fatalf("a folder that does not exist is created: %d %v", code, out)
	}
	code, out = createProject(t, srv, `{"title":"Out","path":"`+outside+`"}`)
	real, _ := filepath.EvalSymlinks(outside)
	if code != http.StatusCreated || out["dir"] != real {
		t.Fatalf("create: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(real, ".git")); err == nil {
		t.Errorf("an empty folder stays a plain folder")
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

// TestAWorkspaceReachedThroughALinkIsStillRefusedAsAProject: the folder is compared resolved, so
// the workspace must be too (on macOS, a workspace under /var or /tmp is reached through a link).
func TestAWorkspaceReachedThroughALinkIsStillRefusedAsAProject(t *testing.T) {
	srv, ws := fullDiskServer(t)
	link := filepath.Join(t.TempDir(), "ws-link")
	if err := os.Symlink(ws, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	srv.opts.WorkspaceDir = link
	if code, out := createProject(t, srv, `{"title":"x","path":"`+ws+`"}`); code != http.StatusBadRequest {
		t.Errorf("the workspace itself: %d %v", code, out)
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
	code, out, body := listDirs(t, srv, drivesPath)
	if code != http.StatusOK || out.Path != "" || len(out.Dirs) != 2 {
		t.Fatalf("drives: %d %s", code, body)
	}
	if out.Dirs[0].Name != "C:" || out.Dirs[1].Path != `D:\` {
		t.Errorf("the drives, in order: %+v", out.Dirs)
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
	if code, _, _ := listDirs(t, srv, drivesPath); code != http.StatusBadRequest {
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

// TestWithoutTheWholeDiskAProjectCanStillUseAFolderInTheWorkspace: an existing workspace folder is
// picked, a new one is made, and anything outside the workspace is still refused.
func TestWithoutTheWholeDiskAProjectCanStillUseAFolderInTheWorkspace(t *testing.T) {
	srv, ws := projectServer(t)
	existing := filepath.Join(ws, "mine")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := makeDir(t, srv, `{"path":"`+ws+`","name":"made"}`); code != http.StatusCreated {
		t.Errorf("a folder in the workspace: %d", code)
	}
	elsewhere := t.TempDir()
	if code := makeDir(t, srv, `{"path":"`+elsewhere+`","name":"made"}`); code != http.StatusForbidden {
		t.Errorf("a folder outside: %d", code)
	}
	code, out := createProject(t, srv, `{"title":"A","path":"`+existing+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("existing workspace folder: %d %v", code, out)
	}
	fresh := filepath.Join(ws, "fresh")
	code, out = createProject(t, srv, `{"title":"B","path":"`+fresh+`"}`)
	if code != http.StatusCreated || !isDir(fresh) {
		t.Fatalf("new workspace folder: %d %v", code, out)
	}
	code, _ = createProject(t, srv, `{"title":"C","path":"`+ws+`"}`)
	if code != http.StatusBadRequest {
		t.Errorf("the workspace itself: %d", code)
	}
}

// TestTheDeletionPreviewNamesTheFolderAndWhatRemovingItWouldLose: the preview carries the project's
// folder and the uncommitted work in it, counted only when the removal is asked for, and says when
// the folder is protected.
func TestTheDeletionPreviewNamesTheFolderAndWhatRemovingItWouldLose(t *testing.T) {
	srv, _ := fullDiskServer(t)
	outside := t.TempDir()
	mustGit(t, outside, "init", "-q")
	if err := os.WriteFile(filepath.Join(outside, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out := createProject(t, srv, `{"title":"Out","path":"`+outside+`"}`)
	preview := func(query string) map[string]any {
		rec := send(t, srv, http.MethodGet, "/v1/projects/"+out["id"].(string)+"/deletion-preview"+query, testToken, "")
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := preview("")
	if got["folder"] != out["dir"] || got["count"] != float64(0) || got["folder_protected"] != nil {
		t.Errorf("keeping the folder loses nothing: %v", got)
	}
	if changes, _ := got["folder_changes"].([]any); len(changes) != 1 {
		t.Errorf("the folder's work is listed: %v", got)
	}
	if got := preview("?folder=delete"); got["count"] != float64(1) {
		t.Errorf("removing the folder discards its work: %v", got)
	}
}

// TestAMissingFolderMustStillBeAUsableProjectFolder: the folder a project would create has a simple
// name, an existing parent and is not one of motita's reserved folders.
func TestAMissingFolderMustStillBeAUsableProjectFolder(t *testing.T) {
	srv, ws := projectServer(t)
	for _, bad := range []string{
		ws + "/bad\\name",
		filepath.Join(ws, "gone", "inside"),
		filepath.Join(ws, "worktrees"),
	} {
		body, _ := json.Marshal(map[string]string{"title": "x", "path": bad})
		if code, out := createProject(t, srv, string(body)); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", bad, code, out)
		}
	}
}

// TestTheSystemsFoldersAreGuarded: the home folder and a system tree itself are refused, a folder
// inside a system tree needs a confirmation, and the confirmed project never deletes it.
func TestTheSystemsFoldersAreGuarded(t *testing.T) {
	srv, _ := fullDiskServer(t)
	home, _ := os.UserHomeDir()
	for _, bad := range []string{home, "/usr"} {
		if !isDir(bad) {
			continue
		}
		body, _ := json.Marshal(map[string]string{"title": "x", "path": bad})
		if code, out := createProject(t, srv, string(body)); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", bad, code, out)
		}
	}
	inside := "/usr/share"
	if !isDir(inside) {
		t.Skip("no system folder to try")
	}
	body := func(extra string) string {
		return `{"title":"Sys","path":"` + inside + `"` + extra + `}`
	}
	if code, out := createProject(t, srv, body("")); code != http.StatusConflict {
		t.Fatalf("unconfirmed: %d %v", code, out)
	}
	_, second := createProject(t, srv, `{"title":"Other","dir":"other"}`)
	oid := second["id"].(string)
	if c := patchProject(t, srv, oid, `{"path":"`+inside+`"}`).Code; c != http.StatusConflict {
		t.Errorf("a move into a system folder needs a confirmation too: %d", c)
	}
	code, out := createProject(t, srv, body(`,"confirm_sensitive":true`))
	if code != http.StatusCreated {
		t.Fatalf("confirmed: %d %v", code, out)
	}
	id := out["id"].(string)
	w := doDelete(t, srv, "/v1/projects/"+id)
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if !isDir(inside) {
		t.Fatal("a system folder is never deleted with its project")
	}
}

// TestTheFolderStaysUnlessTheDeletionAsksForItsRemoval: whatever made the folder, inside the
// workspace or not, deleting a project leaves it, and `?folder=delete` removes it with its contents.
func TestTheFolderStaysUnlessTheDeletionAsksForItsRemoval(t *testing.T) {
	srv, ws := fullDiskServer(t)
	outside := filepath.Join(t.TempDir(), "mine")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, body, query string
		dir               string
		stay              bool
	}{
		{"made, kept", `{"title":"A","dir":"a"}`, "", filepath.Join(ws, "a"), true},
		{"made, removed", `{"title":"B","dir":"b"}`, "?folder=delete", filepath.Join(ws, "b"), false},
		{"outside, kept", `{"title":"C","path":"` + outside + `"}`, "", outside, true},
		{"outside, removed", `{"title":"D","path":"` + outside + `"}`, "?folder=delete", outside, false},
	} {
		_, proj := createProject(t, srv, c.body)
		w := doDelete(t, srv, "/v1/projects/"+proj["id"].(string)+c.query)
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
		if isDir(c.dir) != c.stay {
			t.Errorf("%s: %s still there = %v, want %v", c.name, c.dir, isDir(c.dir), c.stay)
		}
	}
}

// TestRemovingTheFolderNeedsAConfirmationForUncommittedWork: work in the folder stops the removal
// until `?force=1` confirms the discard, and the project is untouched meanwhile.
func TestRemovingTheFolderNeedsAConfirmationForUncommittedWork(t *testing.T) {
	srv, _ := fullDiskServer(t)
	dir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, proj := createProject(t, srv, `{"title":"W","path":"`+dir+`"}`)
	path := "/v1/projects/" + proj["id"].(string)
	if w := doDelete(t, srv, path+"?folder=delete"); w.Code != http.StatusConflict {
		t.Fatalf("unconfirmed: %d %s", w.Code, w.Body.String())
	}
	if !isDir(dir) || srv.projectOf(proj["id"].(string)) == nil {
		t.Fatal("a refused deletion changes nothing")
	}
	if w := doDelete(t, srv, path+"?folder=delete&force=1"); w.Code != http.StatusNoContent {
		t.Fatalf("confirmed: %d %s", w.Code, w.Body.String())
	}
	if isDir(dir) {
		t.Error("the confirmed removal deletes the folder")
	}
}

// TestAProtectedFolderIsNeverRemoved: the system's folders and the one another project stands on
// are refused for the removal, and the preview says so; the project itself can still be deleted
// with the folder kept.
func TestAProtectedFolderIsNeverRemoved(t *testing.T) {
	srv, _ := fullDiskServer(t)
	inside := ""
	for _, tree := range systemTrees() {
		if sub := filepath.Join(tree, "bin"); isDir(sub) {
			inside = sub
			break
		}
	}
	if inside == "" {
		t.Skip("no system folder to try")
	}
	_, proj := createProject(t, srv, `{"title":"Sys","path":"`+inside+`","confirm_sensitive":true}`)
	id := proj["id"].(string)
	rec := send(t, srv, http.MethodGet, "/v1/projects/"+id+"/deletion-preview", testToken, "")
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["folder_protected"] == nil {
		t.Errorf("the preview says the folder is protected: %v", got)
	}
	if w := doDelete(t, srv, "/v1/projects/"+id+"?folder=delete&force=1"); w.Code != http.StatusBadRequest {
		t.Fatalf("the removal of a system folder: %d %s", w.Code, w.Body.String())
	}
	if w := doDelete(t, srv, "/v1/projects/"+id); w.Code != http.StatusNoContent || !isDir(inside) {
		t.Fatalf("keeping the folder still deletes the project: %d", w.Code)
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// artifactServer is a gateway that keeps artifacts, with one session and a workspace in which
// the agent has already written the given files.
func artifactServer(t *testing.T, files map[string]string) (*Server, *conversation, string, string) {
	t.Helper()
	root := t.TempDir()
	svc := &fakeService{}
	srv := newTestServer(t, svc, func(o *Options) {
		o.ArtifactDir = root
		o.ArtifactDays = 30
		o.NewService = func() (Service, error) { return svc, nil }
	})
	c, err := srv.createSession()
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	dir := filepath.Join(ws, filepath.FromSlash(ArtifactDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return srv, c, ws, root
}

func artifactCall(srv *Server, method, path string) *httptest.ResponseRecorder {
	req, _ := http.NewRequest(method, srv.BaseURL()+path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestArtifactsAreCollectedListedServedAndDeleted(t *testing.T) {
	srv, c, ws, _ := artifactServer(t, map[string]string{"report.html": "<h1>hi</h1>", "data.bin": "x"})
	c.workspace = ws
	srv.collectArtifacts(c)
	base := "/v1/sessions/" + c.id + "/artifacts"

	w := artifactCall(srv, http.MethodGet, base)
	var out struct{ Artifacts []Artifact }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Artifacts) != 2 {
		t.Fatalf("list = %s", w.Body)
	}

	w = artifactCall(srv, http.MethodGet, base+"/report.html")
	if w.Body.String() != "<h1>hi</h1>" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("serve = %d %q %v", w.Code, w.Body, w.Header())
	}
	if w = artifactCall(srv, http.MethodGet, base+"/report.html?download=1"); !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download header = %q", w.Header().Get("Content-Disposition"))
	}
	if w = artifactCall(srv, http.MethodGet, base+"/data.bin"); w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("unknown type = %q", w.Header().Get("Content-Type"))
	}

	if w = artifactCall(srv, http.MethodDelete, base+"/data.bin"); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	if w = artifactCall(srv, http.MethodGet, base+"/data.bin"); w.Code != http.StatusNotFound {
		t.Fatalf("deleted file = %d", w.Code)
	}
	srv.removeArtifacts(c.id)
	if w = artifactCall(srv, http.MethodGet, base); strings.Contains(w.Body.String(), "report.html") {
		t.Fatalf("session's files survived its removal: %s", w.Body)
	}
}

func TestArtifactNamesCannotEscape(t *testing.T) {
	srv, c, _, _ := artifactServer(t, nil)
	for _, name := range []string{".hidden", "a%2Fb", "a%5Cb", strings.Repeat("a", 201)} {
		if w := artifactCall(srv, http.MethodGet, "/v1/sessions/"+c.id+"/artifacts/"+name); w.Code != http.StatusNotFound {
			t.Errorf("%q: %d", name, w.Code)
		}
	}
	if validArtifactName("") || validArtifactName("a/b") || !validArtifactName("ok.md") {
		t.Fatal("validArtifactName")
	}
}

func TestCollectArtifactsSkipsWhatItShould(t *testing.T) {
	srv, c, ws, root := artifactServer(t, map[string]string{"ok.txt": "1", ".dot": "2", "big.txt": strings.Repeat("x", maxArtifactBytes+1)})
	dir := filepath.Join(ws, filepath.FromSlash(ArtifactDir))
	outside := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(outside, []byte("s"), 0o644)
	_ = os.Symlink(outside, filepath.Join(dir, "link.txt"))
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	c.workspace = ws
	srv.collectArtifacts(c)
	got := listArtifacts(filepath.Join(root, c.id))
	if len(got) != 1 || got[0].Name != "ok.txt" {
		t.Fatalf("collected %+v, want only ok.txt", got)
	}
	// Nothing to do without a workspace, a folder to read, or a root to write to.
	c.workspace = ""
	srv.collectArtifacts(c)
	c.workspace = t.TempDir()
	srv.collectArtifacts(c)
	c.workspace = ws
	(&Server{}).collectArtifacts(c)
	if (&Server{}).artifactDirFor(c.id) != "" || srv.artifactDirFor("../x") != "" {
		t.Fatal("artifactDirFor must refuse without a root or with a bad id")
	}
	(&Server{}).removeArtifacts(c.id)
}

func TestCollectArtifactsCapsTheCount(t *testing.T) {
	srv, c, ws, root := artifactServer(t, nil)
	dir := filepath.Join(ws, filepath.FromSlash(ArtifactDir))
	for i := 0; i < maxArtifacts+3; i++ {
		_ = os.WriteFile(filepath.Join(dir, strings.Repeat("a", 1)+string(rune('A'+i%26))+string(rune('a'+i/26))+".txt"), []byte("x"), 0o644)
	}
	c.workspace = ws
	srv.collectArtifacts(c)
	if n := len(listArtifacts(filepath.Join(root, c.id))); n != maxArtifacts {
		t.Fatalf("kept %d, want %d", n, maxArtifacts)
	}
	// A name already kept is replaced, not counted again.
	_ = os.WriteFile(filepath.Join(dir, "aAa.txt"), []byte("new"), 0o644)
	c.workspace = ws
	srv.collectArtifacts(c)
	if b, _ := os.ReadFile(filepath.Join(root, c.id, "aAa.txt")); string(b) != "new" {
		t.Fatal("an existing artifact must be updated")
	}
}

func TestListArtifactsWithoutAFolder(t *testing.T) {
	if got := listArtifacts(filepath.Join(t.TempDir(), "none")); got == nil || len(got) != 0 {
		t.Fatalf("got %v, want an empty list", got)
	}
}

func TestDeleteArtifactFailureAndOpenFailure(t *testing.T) {
	srv, c, ws, root := artifactServer(t, map[string]string{"a.txt": "1"})
	c.workspace = ws
	srv.collectArtifacts(c)
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := filepath.Join(root, c.id)
	base := "/v1/sessions/" + c.id + "/artifacts/a.txt"
	_ = os.Chmod(filepath.Join(dir, "a.txt"), 0o000)
	if w := artifactCall(srv, http.MethodGet, base); w.Code != http.StatusNotFound {
		t.Fatalf("unreadable = %d", w.Code)
	}
	_ = os.Chmod(filepath.Join(dir, "a.txt"), 0o644)
	_ = os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o755)
	if w := artifactCall(srv, http.MethodDelete, base); w.Code != http.StatusInternalServerError {
		t.Fatalf("undeletable = %d", w.Code)
	}
}

func TestArtifactFolderIsNotAChange(t *testing.T) {
	// The files the run leaves for the person are not a change to the project.
	ws := t.TempDir()
	gitIn(t, ws, "init", "-q", "-b", "main")
	gitIn(t, ws, "config", "user.email", "t@e.x")
	gitIn(t, ws, "config", "user.name", "T")
	put(t, filepath.Join(ws, "a.txt"), "a\n")
	gitIn(t, ws, "add", ".")
	gitIn(t, ws, "commit", "-qm", "init")
	rev := revOf(t, ws)
	put(t, filepath.Join(ws, ArtifactDir, "report.md"), "# r")
	put(t, filepath.Join(ws, "a.txt"), "a\nb\n")
	rep := buildChangeReport(context.Background(), ws, rev)
	if len(rep.Files) != 1 || rep.Files[0].Path != "a.txt" {
		t.Fatalf("files = %+v, want only a.txt", rep.Files)
	}
}

func artifactReq(srv *Server, method, path, body string) *httptest.ResponseRecorder {
	req, _ := http.NewRequest(method, srv.BaseURL()+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestOnlyNewOrChangedArtifactsAreReported(t *testing.T) {
	srv, c, ws, _ := artifactServer(t, map[string]string{"a.md": "one"})
	c.workspace = ws
	if got := srv.collectArtifacts(c); len(got) != 1 || got[0].Name != "a.md" || got[0].Type != "text/markdown; charset=utf-8" {
		t.Fatalf("first run = %+v", got)
	}
	if got := srv.collectArtifacts(c); len(got) != 0 {
		t.Fatalf("an identical file is not a change: %+v", got)
	}
	_ = os.WriteFile(filepath.Join(ws, filepath.FromSlash(ArtifactDir), "a.md"), []byte("two"), 0o644)
	if got := srv.collectArtifacts(c); len(got) != 1 {
		t.Fatalf("a changed file must be reported: %+v", got)
	}
}

func TestProjectArtifactsAreSharedAndScoped(t *testing.T) {
	srv, c, ws, root := artifactServer(t, map[string]string{"plan.md": "# p"})
	c.workspace = ws
	c.setProjectID("p1", ws, ws)
	srv.collectArtifacts(c)
	shared := projectArtifactName(c.id, "plan.md")
	if _, err := os.Stat(filepath.Join(root, "project-p1", shared)); err != nil {
		t.Fatalf("the project folder must get a copy: %v", err)
	}
	// Another session saving the same name must not replace it.
	other := projectArtifactName("another-session-id", "plan.md")
	if other == shared || projectArtifactName("short", "a") != "short-a" {
		t.Fatalf("names must differ per session: %q %q", shared, other)
	}
	base := "/v1/sessions/" + c.id + "/artifacts"
	if w := artifactCall(srv, http.MethodGet, base+"?scope=project"); !strings.Contains(w.Body.String(), shared) {
		t.Fatalf("project list = %s", w.Body)
	}
	if w := artifactCall(srv, http.MethodGet, base+"/"+shared+"?scope=project"); w.Body.String() != "# p" {
		t.Fatalf("project file = %q", w.Body)
	}
	srv.removeProjectArtifacts("p1")
	if _, err := os.Stat(filepath.Join(root, "project-p1")); err == nil {
		t.Fatal("the project's folder must go with the project")
	}
	// A session with no project has no project scope.
	c.setProjectID("", ws, "")
	if w := artifactCall(srv, http.MethodGet, base+"/"+shared+"?scope=project"); w.Code != http.StatusNotFound {
		t.Fatalf("no project: %d", w.Code)
	}
	if srv.projectArtifactDirFor("") != "" {
		t.Fatal("no project, no folder")
	}
	srv.removeProjectArtifacts("")
}

func TestUploadArtifact(t *testing.T) {
	srv, c, _, root := artifactServer(t, nil)
	base := "/v1/sessions/" + c.id + "/artifacts/"
	if w := artifactReq(srv, http.MethodPut, base+"notes.txt", "hello"); w.Code != http.StatusNoContent {
		t.Fatalf("upload = %d %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(root, c.id, "notes.txt")); string(b) != "hello" {
		t.Fatalf("stored %q", b)
	}
	if w := artifactReq(srv, http.MethodPut, base+".hidden", "x"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad name = %d", w.Code)
	}
	if w := artifactReq(srv, http.MethodPut, base+"big.bin", strings.Repeat("x", maxArtifactBytes+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big = %d", w.Code)
	}
	nokeep := newTestServer(t, &fakeService{}, func(o *Options) {
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	nc, _ := nokeep.createSession()
	if w := artifactReq(nokeep, http.MethodPut, "/v1/sessions/"+nc.id+"/artifacts/a.txt", "x"); w.Code != http.StatusNotImplemented {
		t.Fatalf("not kept = %d", w.Code)
	}
}

func TestStoreArtifactLimits(t *testing.T) {
	srv, c, _, root := artifactServer(t, nil)
	dir := filepath.Join(root, "s")
	if _, err := srv.storeArtifact("", "a.txt", nil); err == nil {
		t.Fatal("no folder must be refused")
	}
	// The gateway-wide total: a store that is already full refuses a new file. The big file lives
	// in a real session's folder, which the pruner that starts with the gateway leaves alone.
	big := filepath.Join(root, c.id, "huge")
	put(t, big, "")
	fh, _ := os.OpenFile(big, os.O_WRONLY, 0o644)
	_ = fh.Truncate(maxArtifactTotalBytes)
	fh.Close()
	if _, err := srv.storeArtifact(dir, "a.txt", []byte("x")); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("full store: %v", err)
	}
	_ = os.Remove(big)
	// An unwritable root is a plain error, not a refusal.
	if os.Geteuid() != 0 {
		_ = os.Chmod(root, 0o500)
		defer os.Chmod(root, 0o755)
		if _, err := srv.storeArtifact(filepath.Join(root, "new"), "a.txt", []byte("x")); err == nil {
			t.Fatal("an unwritable root must fail")
		}
	}
}

func TestPutArtifactReportsAnInternalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	srv, c, _, root := artifactServer(t, nil)
	_ = os.Chmod(root, 0o500)
	defer os.Chmod(root, 0o755)
	if w := artifactReq(srv, http.MethodPut, "/v1/sessions/"+c.id+"/artifacts/a.txt", "x"); w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", w.Code)
	}
}

func TestArtifactBytesUnderAMissingRoot(t *testing.T) {
	if artifactBytesUnder(filepath.Join(t.TempDir(), "none")) != 0 {
		t.Fatal("a missing root holds nothing")
	}
}

func TestTheDoneEventCarriesTheRunsArtifacts(t *testing.T) {
	ws := t.TempDir()
	svc := &fakeService{task: func(_ context.Context, _ string, _ func(string, ...any)) (string, error) {
		put(t, filepath.Join(ws, filepath.FromSlash(ArtifactDir), "report.md"), "# done")
		return "finished", nil
	}}
	srv := newTestServer(t, svc, func(o *Options) { o.ArtifactDir = t.TempDir() })
	srv.sessions[DefaultSession].workspace = ws
	events := collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"write it"}`)
	last := events[len(events)-1]
	if last.Event != EventDone || !strings.Contains(last.Data, `"artifacts"`) || !strings.Contains(last.Data, "report.md") {
		t.Fatalf("done = %s %s", last.Event, last.Data)
	}
}

func TestStoreArtifactRefusesWhatIsTooLargeOrBadlyNamed(t *testing.T) {
	srv, _, _, root := artifactServer(t, nil)
	if _, err := srv.storeArtifact(root, ".x", []byte("x")); err == nil {
		t.Fatal("a hidden name must be refused")
	}
	if _, err := srv.storeArtifact(root, "big", make([]byte, maxArtifactBytes+1)); err == nil {
		t.Fatal("an oversized file must be refused")
	}
}

func TestCollectArtifactsSkipsAFileItCannotRead(t *testing.T) {
	srv, c, ws, root := artifactServer(t, map[string]string{"a.txt": "1"})
	c.workspace = ws
	old := readArtifactFile
	readArtifactFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	defer func() { readArtifactFile = old }()
	if got := srv.collectArtifacts(c); len(got) != 0 {
		t.Fatalf("collected %+v", got)
	}
	if len(listArtifacts(filepath.Join(root, c.id))) != 0 {
		t.Fatal("nothing must be kept")
	}
}

func TestListArtifactsIgnoresFoldersAndTypesFallBack(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a.zzzunknown"), []byte("1"), 0o644)
	got := listArtifacts(dir)
	if len(got) != 1 || got[0].Type != "application/octet-stream" {
		t.Fatalf("got %+v", got)
	}
}

func TestDeleteAnUnknownArtifact(t *testing.T) {
	srv, c, _, _ := artifactServer(t, nil)
	if w := artifactCall(srv, http.MethodDelete, "/v1/sessions/"+c.id+"/artifacts/nope.txt"); w.Code != http.StatusNotFound {
		t.Fatalf("got %d", w.Code)
	}
}

func aged(t *testing.T, path string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestPruneDeletesOrphansAndOldFiles(t *testing.T) {
	root := t.TempDir()
	svc := &fakeService{}
	srv := newTestServer(t, svc, func(o *Options) {
		o.ArtifactDir = root
		o.ArtifactDays = 30
		o.SessionDir = t.TempDir()
		o.ProjectDir = t.TempDir()
		o.WorkspaceDir = t.TempDir()
		o.NewService = func() (Service, error) { return svc, nil }
	})
	live, err := srv.createSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.projects.save(Project{ID: "p1", Title: "t", Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	// A session that is only on disk is still a session.
	onDisk := filepath.Join(srv.store.dir, "disk-session.json")
	_ = os.WriteFile(onDisk, []byte("{}"), 0o644)

	for _, d := range []string{live.id, "disk-session", "gone-session", "project-p1", "project-gone"} {
		put(t, filepath.Join(root, d, "old.txt"), "o")
		put(t, filepath.Join(root, d, "new.txt"), "n")
		aged(t, filepath.Join(root, d, "old.txt"), 40*24*time.Hour)
	}
	put(t, filepath.Join(root, "stray-file"), "not a folder")

	srv.pruneArtifacts(time.Now())

	for _, gone := range []string{"gone-session", "project-gone"} {
		if _, err := os.Stat(filepath.Join(root, gone)); err == nil {
			t.Errorf("%s: an orphan folder must be removed", gone)
		}
	}
	for _, kept := range []string{live.id, "disk-session", "project-p1"} {
		if _, err := os.Stat(filepath.Join(root, kept, "new.txt")); err != nil {
			t.Errorf("%s: a recent file must stay: %v", kept, err)
		}
		if _, err := os.Stat(filepath.Join(root, kept, "old.txt")); err == nil {
			t.Errorf("%s: a file past the retention must go", kept)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "stray-file")); err != nil {
		t.Error("only folders are the pruner's business")
	}
}

func TestPruneWithZeroDaysKeepsEverythingReachable(t *testing.T) {
	root := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ArtifactDir = root
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	c, _ := srv.createSession()
	put(t, filepath.Join(root, c.id, "old.txt"), "o")
	aged(t, filepath.Join(root, c.id, "old.txt"), 400*24*time.Hour)
	// Without a project store a project folder cannot be judged an orphan.
	put(t, filepath.Join(root, "project-x", "a.txt"), "a")
	srv.pruneArtifacts(time.Now())
	if _, err := os.Stat(filepath.Join(root, c.id, "old.txt")); err != nil {
		t.Fatal("0 days keeps files for ever")
	}
	if _, err := os.Stat(filepath.Join(root, "project-x", "a.txt")); err != nil {
		t.Fatal("no project store, no verdict on a project folder")
	}
}

func TestPruneWithoutARootIsANoOp(t *testing.T) {
	(&Server{}).pruneArtifacts(time.Now())
	(&Server{opts: Options{ArtifactDir: filepath.Join(t.TempDir(), "missing")}}).pruneArtifacts(time.Now())
	(&Server{}).startArtifactPruner(time.Hour)
}

func TestThePrunerRunsOnAScheduleUntilTheGatewayCloses(t *testing.T) {
	root := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ArtifactDir = root })
	put(t, filepath.Join(root, "gone-session", "a.txt"), "a")
	srv.startArtifactPruner(5 * time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "gone-session")); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the pruner never removed the orphan")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSessionIsKnownWithoutAStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if srv.sessionIsKnown("nobody") {
		t.Fatal("an unknown session with no store is unknown")
	}
}

func TestTheClientListsAndReadsArtifacts(t *testing.T) {
	svc := &fakeService{}
	root := t.TempDir()
	srv := newTestServer(t, svc, func(o *Options) { o.ArtifactDir = root })
	put(t, filepath.Join(root, DefaultSession, "r.md"), "# report")
	cl := NewClientForSession(srv.BaseURL(), testToken, DefaultSession)
	got, err := cl.ListArtifacts(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "r.md" {
		t.Fatalf("list = %+v %v", got, err)
	}
	b, err := cl.ReadArtifact(context.Background(), "r.md")
	if err != nil || string(b) != "# report" {
		t.Fatalf("read = %q %v", b, err)
	}
	if _, err := cl.ReadArtifact(context.Background(), "nope.md"); err == nil {
		t.Fatal("a missing file must be an error")
	}
	srv.Close(context.Background())
	if _, err := cl.ListArtifacts(context.Background()); err == nil {
		t.Fatal("a gateway that is gone must be an error")
	}
	if _, err := cl.ReadArtifact(context.Background(), "r.md"); err == nil {
		t.Fatal("a gateway that is gone must be an error")
	}
	bad := NewClientForSession("http://[::1", testToken, DefaultSession)
	if _, err := bad.ReadArtifact(context.Background(), "r.md"); err == nil {
		t.Fatal("a malformed address must be an error")
	}
}

func TestPinAndExpiry(t *testing.T) {
	srv, c, ws, root := artifactServer(t, map[string]string{"keep.md": "k", "lose.md": "l"})
	c.workspace = ws
	srv.collectArtifacts(c)
	base := "/v1/sessions/" + c.id + "/artifacts"
	list := func() map[string]Artifact {
		var out struct{ Artifacts []Artifact }
		_ = json.Unmarshal(artifactCall(srv, http.MethodGet, base).Body.Bytes(), &out)
		m := map[string]Artifact{}
		for _, a := range out.Artifacts {
			m[a.Name] = a
		}
		return m
	}
	if a := list()["keep.md"]; a.Pinned || a.ExpiresAt == nil || time.Until(*a.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("an unpinned file must say when it expires: %+v", a)
	}
	if w := artifactReq(srv, http.MethodPost, base+"/keep.md/pin", `{"pinned":true}`); w.Code != http.StatusNoContent {
		t.Fatalf("pin = %d %s", w.Code, w.Body)
	}
	if a := list()["keep.md"]; !a.Pinned || a.ExpiresAt != nil {
		t.Fatalf("a pinned file never expires: %+v", a)
	}
	if _, listed := list()[pinFile]; listed {
		t.Fatal("the pin file is not an artifact")
	}
	// Pruning keeps the pinned file and removes the other one past the retention.
	for _, n := range []string{"keep.md", "lose.md"} {
		aged(t, filepath.Join(root, c.id, n), 40*24*time.Hour)
	}
	srv.pruneArtifacts(time.Now())
	if _, err := os.Stat(filepath.Join(root, c.id, "keep.md")); err != nil {
		t.Fatal("a pinned file survives the prune")
	}
	if _, err := os.Stat(filepath.Join(root, c.id, "lose.md")); err == nil {
		t.Fatal("an unpinned old file goes")
	}
	// Unpinning, deleting and the error paths.
	if w := artifactReq(srv, http.MethodPost, base+"/keep.md/pin", `{"pinned":false}`); w.Code != http.StatusNoContent {
		t.Fatalf("unpin = %d", w.Code)
	}
	if w := artifactReq(srv, http.MethodPost, base+"/keep.md/pin", `{`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad body = %d", w.Code)
	}
	if w := artifactReq(srv, http.MethodPost, base+"/nope.md/pin", `{"pinned":true}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown file = %d", w.Code)
	}
	_ = artifactReq(srv, http.MethodPost, base+"/keep.md/pin", `{"pinned":true}`)
	artifactCall(srv, http.MethodDelete, base+"/keep.md")
	if readPinned(filepath.Join(root, c.id))["keep.md"] {
		t.Fatal("deleting a file forgets its pin")
	}
}

func TestPinFileEdgeCases(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, pinFile), []byte("not json"), 0o644)
	if len(readPinned(dir)) != 0 {
		t.Fatal("a damaged pin file is no pins")
	}
	if err := setPinned(dir, "a.md", false); err != nil {
		t.Fatal("unpinning what is not pinned is a no-op")
	}
	if err := setPinned(filepath.Join(dir, "missing"), "a.md", true); err == nil {
		t.Fatal("pinning into a folder that does not exist must fail")
	}
}

func TestPinFailureIsReported(t *testing.T) {
	srv, c, ws, root := artifactServer(t, map[string]string{"a.md": "a"})
	c.workspace = ws
	srv.collectArtifacts(c)
	// A directory where the pin file goes makes the write fail for any user.
	_ = os.Mkdir(filepath.Join(root, c.id, pinFile), 0o755)
	if w := artifactReq(srv, http.MethodPost, "/v1/sessions/"+c.id+"/artifacts/a.md/pin", `{"pinned":true}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", w.Code)
	}
}

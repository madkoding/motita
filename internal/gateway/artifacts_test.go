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
)

// artifactServer is a gateway that keeps artifacts, with one session and a workspace in which
// the agent has already written the given files.
func artifactServer(t *testing.T, files map[string]string) (*Server, *conversation, string, string) {
	t.Helper()
	root := t.TempDir()
	svc := &fakeService{}
	srv := newTestServer(t, svc, func(o *Options) {
		o.ArtifactDir = root
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
	srv.collectArtifacts(c.id, ws)
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
	srv.collectArtifacts(c.id, ws)
	got := listArtifacts(filepath.Join(root, c.id))
	if len(got) != 1 || got[0].Name != "ok.txt" {
		t.Fatalf("collected %+v, want only ok.txt", got)
	}
	// Nothing to do without a workspace, a folder to read, or a root to write to.
	srv.collectArtifacts(c.id, "")
	srv.collectArtifacts(c.id, t.TempDir())
	(&Server{}).collectArtifacts(c.id, ws)
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
	srv.collectArtifacts(c.id, ws)
	if n := len(listArtifacts(filepath.Join(root, c.id))); n != maxArtifacts {
		t.Fatalf("kept %d, want %d", n, maxArtifacts)
	}
	// A name already kept is replaced, not counted again.
	_ = os.WriteFile(filepath.Join(dir, "aAa.txt"), []byte("new"), 0o644)
	srv.collectArtifacts(c.id, ws)
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
	srv.collectArtifacts(c.id, ws)
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

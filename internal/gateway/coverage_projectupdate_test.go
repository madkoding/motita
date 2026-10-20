package gateway

// Coverage tests for the gateway package: updating a project after creation.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func patchProject(t *testing.T, srv *Server, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, srv.BaseURL()+"/v1/projects/"+id, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestUpdateProjectSaveFailure(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = dir
		o.WorkspaceDir = t.TempDir()
	})
	if err := srv.projects.save(Project{ID: "p1", Title: "t", Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if w := patchProject(t, srv, "p1", `{"title":"renamed"}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("save failure: %d", w.Code)
	}
}

// TestWriteSyncedReportsAFailedWrite: /dev/full opens fine and refuses every write, which is
// the failure of the three steps a test can cause for any user, root included.
func TestWriteSyncedReportsAFailedWrite(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("this platform has no /dev/full")
	}
	if err := writeSynced("/dev/full", []byte("x")); err == nil {
		t.Fatal("a write to a full device must fail")
	}
}

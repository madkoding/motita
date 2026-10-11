package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A project id is a file name in the project store, never a path: an escaped "../" in the
// URL must not reach a file outside the store.
func TestDeleteProjectIDCannotEscapeTheStore(t *testing.T) {
	root := t.TempDir()
	pdir := filepath.Join(root, "projects")
	victim := filepath.Join(root, "victim.json")
	if err := os.WriteFile(victim, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = t.TempDir()
	})
	req, _ := http.NewRequest(http.MethodDelete, srv.BaseURL()+"/v1/projects/..%2Fvictim", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a file outside the project store was deleted (status %d): %v", w.Code, err)
	}
}

func TestProjectStoreRefusesPathIDs(t *testing.T) {
	ps, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../x", "a/b", `a\b`, "..", ""} {
		if err := ps.save(Project{ID: id}); err == nil {
			t.Errorf("save(%q) was accepted", id)
		}
		if _, err := ps.load(id); err == nil {
			t.Errorf("load(%q) was accepted", id)
		}
		if err := ps.delete(id); err == nil {
			t.Errorf("delete(%q) was accepted", id)
		}
	}
}

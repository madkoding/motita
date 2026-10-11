package gateway

// Coverage tests for the gateway package — tests that exist only to reach the
// 100% coverage gate, covering functions that the feature tests do not
// exercise: project store, session store, update endpoints, persistence,
// client no-ops, and WebSocket helpers. This file holds the client no-ops and
// the stores and handlers of projects; the rest live in coverage_runtime_test.go
// and coverage_podman_test.go.

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

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/gitx"
)

// --- Client no-ops ---

func TestClientSetWorkspaceIsNoOp(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	c := NewClient(srv.BaseURL(), srv.Token())
	c.SetWorkspace("/tmp/whatever")
}

func TestClientGenerateTitleJoinsFields(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	c := NewClient(srv.BaseURL(), srv.Token())
	got := c.GenerateTitle(context.Background(), "  hello   world  ")
	if got != "hello world" {
		t.Errorf("GenerateTitle = %q, want %q", got, "hello world")
	}
}

func TestClientRestoreTranscriptIsNoOp(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	c := NewClient(srv.BaseURL(), srv.Token())
	c.RestoreTranscript([]agent.DialogueTurn{{User: "hi"}})
}

// --- handleListCommands ---

func TestHandleListCommandsReturnsCatalogue(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, "/v1/commands", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Commands []map[string]any `json:"commands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(body.Commands) == 0 {
		t.Error("expected at least one command in the catalogue")
	}
}

// --- Project store ---

func TestProjectStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	ps, err := newProjectStore(dir)
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if got := ps.path("abc"); !strings.HasSuffix(got, "abc.json") {
		t.Errorf("path = %q, want suffix abc.json", got)
	}
	p := Project{ID: "p1", Title: "Test", Dir: "/tmp/test", Created: time.Now()}
	if err := ps.save(p); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := ps.load("p1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Title != "Test" {
		t.Errorf("loaded title = %q, want %q", got.Title, "Test")
	}
	all, err := ps.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("loadAll returned %d, want 1", len(all))
	}
	if err := ps.delete("p1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all2, _ := ps.loadAll()
	if len(all2) != 0 {
		t.Errorf("after delete, loadAll returned %d, want 0", len(all2))
	}
}

func TestNewProjectStoreEmptyDir(t *testing.T) {
	_, err := newProjectStore("")
	if err == nil {
		t.Error("expected error for empty dir")
	}
}

func TestProjectStoreLoadMissing(t *testing.T) {
	dir := t.TempDir()
	ps, _ := newProjectStore(dir)
	got, err := ps.load("nonexistent")
	if err != nil {
		t.Errorf("load of missing project should return nil, nil, got err: %v", err)
	}
	if got != nil {
		t.Errorf("load of missing project should return nil")
	}
}

func TestProjectStoreLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	ps, _ := newProjectStore(dir)
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0o600)
	_, err := ps.load("bad")
	if err == nil {
		t.Error("expected error for corrupt JSON")
	}
}

func TestProjectStoreDeleteMissing(t *testing.T) {
	dir := t.TempDir()
	ps, _ := newProjectStore(dir)
	err := ps.delete("nonexistent")
	if err != nil {
		t.Errorf("delete of missing should be nil, got: %v", err)
	}
}

func TestCloneGitRepoEmptyURL(t *testing.T) {
	_, err := cloneGitRepo("", t.TempDir(), nil)
	if err == nil {
		t.Error("expected error for empty git URL")
	}
}

func TestCloneGitRepoInvalidURL(t *testing.T) {
	_, err := cloneGitRepo("not-a-valid-url", filepath.Join(t.TempDir(), "repo"), nil)
	if err == nil {
		t.Error("expected error for invalid git URL")
	}
}

// --- Session store ---

func TestSessionStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	st, err := newSessionStore(dir)
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	if got := st.path("s1"); !strings.HasSuffix(got, "s1.json") {
		t.Errorf("path = %q, want suffix s1.json", got)
	}
	svc := &fakeService{}
	conv := newConversation("s1", svc)
	conv.setTitle("Test Session")
	if err := st.save(conv); err != nil {
		t.Fatalf("save: %v", err)
	}
	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "s1" {
		t.Errorf("loadAll = %v, want 1 session with id s1", all)
	}
	if err := st.delete("s1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all2, _ := st.loadAll()
	if len(all2) != 0 {
		t.Errorf("after delete, loadAll returned %d, want 0", len(all2))
	}
}

func TestNewSessionStoreEmptyDir(t *testing.T) {
	_, err := newSessionStore("")
	if err == nil {
		t.Error("expected error for empty dir")
	}
}

func TestSessionStoreLoadAllCorruptSkipped(t *testing.T) {
	dir := t.TempDir()
	st, _ := newSessionStore(dir)
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0o600)
	all, err := st.loadAll()
	if err != nil {
		t.Fatalf("loadAll with corrupt file: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("corrupt file should be skipped, got %d", len(all))
	}
}

func TestSessionStoreDeleteMissing(t *testing.T) {
	dir := t.TempDir()
	st, _ := newSessionStore(dir)
	err := st.delete("nonexistent")
	if err != nil {
		t.Errorf("delete of missing should be nil, got: %v", err)
	}
}

// --- handleListProjects, handleCreateProject, handleDeleteProject ---

func TestHandleListProjectsWithoutStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, "/v1/projects", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHandleListProjectsWithStore(t *testing.T) {
	pdir := t.TempDir()
	wsdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = wsdir
	})
	body := strings.NewReader(`{"title":"My Project","dir":"myproj"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/projects", body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create project: status = %d, want 201, body: %s", w.Code, w.Body.String())
	}
	w2 := get(t, srv, "/v1/projects", testToken)
	if w2.Code != http.StatusOK {
		t.Fatalf("list projects: status = %d", w2.Code)
	}
}

func TestHandleCreateProjectWithoutStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/projects", strings.NewReader(`{"title":"x","dir":"y"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
}

func TestHandleCreateProjectEmptyTitle(t *testing.T) {
	pdir := t.TempDir()
	wsdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = wsdir
	})
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/projects", strings.NewReader(`{"title":"","dir":"x"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleCreateProjectEmptyDir(t *testing.T) {
	pdir := t.TempDir()
	wsdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = wsdir
	})
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/projects", strings.NewReader(`{"title":"x","dir":""}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleCreateProjectPathInDir(t *testing.T) {
	pdir := t.TempDir()
	wsdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = wsdir
	})
	req, _ := http.NewRequest(
		http.MethodPost,
		srv.BaseURL()+"/v1/projects",
		strings.NewReader(`{"title":"x","dir":"../escape"}`),
	)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleCreateProjectNoWorkspace(t *testing.T) {
	pdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = "" // no workspace
	})
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/projects", strings.NewReader(`{"title":"x","dir":"y"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestHandleCreateProjectWithGitURL(t *testing.T) {
	pdir := t.TempDir()
	wsdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = wsdir
	})
	// Use an invalid git URL to trigger the clone error path.
	req, _ := http.NewRequest(
		http.MethodPost,
		srv.BaseURL()+"/v1/projects",
		strings.NewReader(`{"title":"x","dir":"y","git_url":"not-a-real-url"}`),
	)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

// createProjectIn posts a project with no git_url and returns the response code.
func createProjectIn(t *testing.T, wsdir, dir string) int {
	t.Helper()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = t.TempDir()
		o.WorkspaceDir = wsdir
	})
	req, _ := http.NewRequest(
		http.MethodPost,
		srv.BaseURL()+"/v1/projects",
		strings.NewReader(`{"title":"x","dir":"`+dir+`"}`),
	)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w.Code
}

func TestCreateProjectInitialisesAMissingFolder(t *testing.T) {
	ws := t.TempDir()
	if code := createProjectIn(t, ws, "fresh"); code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", code)
	}
	if _, err := os.Stat(filepath.Join(ws, "fresh", ".git")); err != nil {
		t.Errorf("a new project folder must be a git repository: %v", err)
	}
}

func TestCreateProjectInitialisesAnEmptyFolder(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := createProjectIn(t, ws, "empty"); code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", code)
	}
	if _, err := os.Stat(filepath.Join(ws, "empty", ".git")); err != nil {
		t.Errorf("an empty project folder must be a git repository: %v", err)
	}
}

func TestCreateProjectLeavesAFolderWithFilesAlone(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "full")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := createProjectIn(t, ws, "full"); code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error("a folder that already holds files must not be turned into a repository")
	}
}

func TestCreateProjectFolderIsAFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := createProjectIn(t, ws, "afile"); code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", code)
	}
}

func TestCreateProjectWithoutGitReports500(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	w, _ := postProject(
		t,
		filepath.Join(t.TempDir(), "gitconfig"),
		`{"title":"x","dir":"p","git_user_name":"Ada","git_user_email":"ada@example.com"}`,
	)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// postProject posts a JSON body to a gateway whose git has NO global identity.
func postProject(t *testing.T, cfg, body string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	ws := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = t.TempDir()
		o.WorkspaceDir = ws
	})
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/projects", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w, ws
}

func TestCreateProjectAsksForAGitIdentity(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	w, ws := postProject(t, cfg, `{"title":"x","dir":"p"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"git_identity_required"`) {
		t.Fatalf("status = %d body = %s, want 409 git_identity_required", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(ws, "p")); err == nil {
		t.Error("nothing may be created before the identity is known")
	}
}

func TestCreateProjectSavesTheGitIdentityItIsGiven(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	w, ws := postProject(t, cfg, `{"title":"x","dir":"p","git_user_name":"Ada","git_user_email":"ada@example.com"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(ws, "p", ".git")); err != nil {
		t.Errorf("not a repository: %v", err)
	}
	if n, e := gitx.GlobalIdentity(context.Background()); n != "Ada" || e != "ada@example.com" {
		t.Errorf("saved identity = %q %q", n, e)
	}
}

func TestCreateProjectRejectsABadGitIdentity(t *testing.T) {
	for _, body := range []string{
		`{"title":"x","dir":"p","git_user_name":"Ada"}`,
		`{"title":"x","dir":"p","git_user_name":"Ada","git_user_email":"nope"}`,
		`{"title":"x","dir":"p","git_user_email":"a@b.c"}`,
	} {
		if w, _ := postProject(t, filepath.Join(t.TempDir(), "gitconfig"), body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, w.Code)
		}
	}
}

func TestCreateProjectGitIdentityNotSaved(t *testing.T) {
	// A config path that is a directory makes `git config --global` fail.
	w, _ := postProject(t, t.TempDir(), `{"title":"x","dir":"p","git_user_name":"Ada","git_user_email":"ada@example.com"}`)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestCreateProjectWithFilesNeedsNoGitIdentity(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, "full"), 0o755)
	os.WriteFile(filepath.Join(ws, "full", "a"), []byte("x"), 0o644)
	if code := createProjectIn(t, ws, "full"); code != http.StatusCreated {
		t.Errorf("status = %d, want 201", code)
	}
}

func TestHandleDeleteProjectWithoutStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	req, _ := http.NewRequest(http.MethodDelete, srv.BaseURL()+"/v1/projects/p1", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
}

func TestHandleDeleteProjectWithStore(t *testing.T) {
	pdir := t.TempDir()
	wsdir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ProjectDir = pdir
		o.WorkspaceDir = wsdir
	})
	body := strings.NewReader(`{"title":"To Delete","dir":"delme"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/projects", body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	id := resp["id"].(string)
	req2, _ := http.NewRequest(http.MethodDelete, srv.BaseURL()+"/v1/projects/"+id, nil)
	req2.Header.Set("Authorization", "Bearer "+testToken)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusNoContent {
		t.Errorf("delete: status = %d, want 204", w2.Code)
	}
}

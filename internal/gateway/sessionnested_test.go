package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// A session of a plain folder holding two repositories gets a worktree of each, on its branch, and
// deleting it removes both and its folder. Uncommitted work in one of them still refuses the deletion.
func TestASessionOfAPlainFolderGetsAWorktreeOfEachRepository(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "proj")
	repos := []string{"api", "group/web"}
	for _, rel := range repos {
		dir := filepath.Join(projectDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, dir)
	}
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: workspace,
		ProjectDir:   filepath.Join(workspace, "projects"),
		SessionDir:   filepath.Join(workspace, "sessions"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})
	rec := post(t, srv, "/v1/projects", `{"title":"proj","dir":"proj"}`, testToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating the project answered %d: %s", rec.Code, rec.Body.String())
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &proj); err != nil {
		t.Fatal(err)
	}
	created := post(t, srv, "/v1/sessions", `{"project_id":"`+proj.ID+`","isolate":true}`, testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("creating the session answered %d: %s", created.Code, created.Body.String())
	}
	var conv SessionStatus
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(workspace, "worktrees", conv.ID)
	for _, rel := range repos {
		repo := filepath.Join(projectDir, filepath.FromSlash(rel))
		dest := filepath.Join(wt, filepath.FromSlash(rel))
		if !worktreeRegistered(t, repo, dest) {
			t.Fatalf("%s must have a worktree at %s", rel, dest)
		}
		if got := gitOut(t, dest, "branch", "--show-current"); got != sessionBranch(conv.ID) {
			t.Errorf("%s worktree is on %q, want the session's branch", rel, got)
		}
	}

	dirty := filepath.Join(wt, "api", "wip.txt")
	if err := os.WriteFile(dirty, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := doDelete(t, srv, "/v1/sessions/"+conv.ID); rec.Code != http.StatusConflict {
		t.Fatalf("uncommitted work must refuse the deletion, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}

	if rec := doDelete(t, srv, "/v1/sessions/"+conv.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("deleting a clean session answered %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the session's folder must be gone: %v", err)
	}
	for _, rel := range repos {
		repo := filepath.Join(projectDir, filepath.FromSlash(rel))
		if worktreeRegistered(t, repo, filepath.Join(wt, filepath.FromSlash(rel))) {
			t.Errorf("%s still registers the session's worktree", rel)
		}
	}
}

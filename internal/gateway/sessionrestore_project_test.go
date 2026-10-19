package gateway

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/gitx"
)

// A session restored after a restart finds its project: what its agent is told, and its worktrees
// of the project's other repositories.
func TestARestoredSessionKeepsItsProject(t *testing.T) {
	srv, _, sessionID := newSessionInProject(t)
	id := onlyProjectID(t, srv)
	if code, _ := addRepo(t, srv, id, `{"git_url":"`+sourceRepo(t)+`","name":"lib"}`); code != http.StatusCreated {
		t.Fatalf("adding: %d", code)
	}
	ws := convNamed(t, srv, sessionID).workspaceDir()
	srv.mainRefresh.wait()
	clone := srv.projectOf(id).Repos[0].Dir
	// Lose the repo worktree, like a container that did not keep it.
	_ = gitx.RemoveWorktree(context.Background(), clone, filepath.Join(ws, "lib"), true)
	opts := srv.opts
	_ = srv.Close(context.Background())
	svc := &noteService{}
	opts.NewService = func() (Service, error) { return svc, nil }
	startServer(t, opts)
	if !strings.Contains(svc.note, "belongs to the project") {
		t.Errorf("restored session lost its project note: %q", svc.note)
	}
	if _, ok, _ := gitx.LiveWorktreeAt(context.Background(), clone, filepath.Join(ws, "lib")); !ok {
		t.Error("restored session lost its worktree of the other repository")
	}
}

package gateway

// A confirmed discard DELETES.
//
// Reported from real use: "deleting a session with changes, after I accept, throws a git error
// and the session stays". The user's answer was overruled by a tool: git refuses some checkouts
// even with --force (a locked one needs it twice, one holding a submodule refuses outright), and
// a checkout whose change list could not be read was refused even after the user said to
// discard it. Once the user has confirmed, the only thing left to decide is HOW to remove it.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/logx"
)

// quietLog is a real logger writing to a file of the test's own, so the log branches run.
func quietLog(t *testing.T) *logx.Logger {
	t.Helper()
	l, err := logx.New(logx.Options{Path: filepath.Join(t.TempDir(), "gw.log"), Level: logx.Info})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestAConfirmedDiscardDeletesWhenGitRefuses: git says no to the forced removal, and the
// checkout is removed as a directory - its dependency LINK with it, never what it points at.
func TestAConfirmedDiscardDeletesWhenGitRefuses(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	srv.opts.Log = quietLog(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	deps := filepath.Join(projectDir, "node_modules")
	if err := os.MkdirAll(deps, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(deps, "keep.js"), []byte("x"), 0o644)
	if err := os.Symlink(deps, filepath.Join(wt, "node_modules")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt, "work.txt"), []byte("uncommitted"), 0o644)

	restore := worktreeRemove
	worktreeRemove = func(context.Context, string, string, bool) error {
		return errors.New("fatal: working trees containing submodules cannot be moved or removed")
	}
	t.Cleanup(func() { worktreeRemove = restore })

	rec := doDelete(t, srv, "/v1/sessions/"+id+"?force=1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a confirmed discard must delete, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := srv.lookup(id); ok {
		t.Error("the session must be gone")
	}
	if _, err := os.Lstat(wt); err == nil {
		t.Error("the checkout must be gone")
	}
	if _, err := os.Stat(filepath.Join(deps, "keep.js")); err != nil {
		t.Errorf("the project's own dependencies must survive the removal of the link: %v", err)
	}
}

// TestAConfirmedDiscardDeletesALockedCheckout: the real git, a real lock.
func TestAConfirmedDiscardDeletesALockedCheckout(t *testing.T) {
	srv, projectDir, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	os.WriteFile(filepath.Join(wt, "work.txt"), []byte("uncommitted"), 0o644)
	if out, err := runGit(projectDir, "worktree", "lock", wt); err != nil {
		t.Fatalf("lock: %v %s", err, out)
	}
	rec := doDelete(t, srv, "/v1/sessions/"+id+"?force=1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a confirmed discard of a locked checkout must delete, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAConfirmedDiscardDoesNotNeedTheChangeList: the list could not be read, and the user
// already said to discard whatever is there - an answer that does not depend on the count.
func TestAConfirmedDiscardDoesNotNeedTheChangeList(t *testing.T) {
	srv, _, id := newSessionInProject(t)
	restore := worktreeInspectList
	worktreeInspectList = func(context.Context, string) ([]gitx.Change, error) {
		return nil, errors.New("the checkout could not be read")
	}
	t.Cleanup(func() { worktreeInspectList = restore })

	rec := doDelete(t, srv, "/v1/sessions/"+id+"?force=1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTheDirectoryFallbackStaysInsideTheWorktrees: nothing outside this gateway's own
// worktrees directory is ever removed as a directory, whatever a session claims.
func TestTheDirectoryFallbackStaysInsideTheWorktrees(t *testing.T) {
	srv, projectDir, _ := newSessionInProject(t)
	for _, outside := range []string{
		projectDir,
		filepath.Join(srv.opts.WorkspaceDir, "worktrees"),
		filepath.Join(srv.opts.WorkspaceDir, "worktrees", "..", "proj"),
	} {
		if err := srv.forceRemoveCheckout(projectDir, outside); err == nil {
			t.Errorf("%s must be refused", outside)
		}
	}
	if _, err := os.Stat(projectDir); err != nil {
		t.Fatalf("the project must survive: %v", err)
	}
	empty := &Server{opts: Options{}}
	if err := empty.forceRemoveCheckout(projectDir, "/x/worktrees/y"); err == nil {
		t.Error("with no workspace root nothing is removed")
	}
}

// TestWhenEvenTheDirectoryCannotBeRemoved: both failures are reported, and the session stays.
func TestWhenEvenTheDirectoryCannotBeRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	srv, _, id := newSessionInProject(t)
	wt := filepath.Join(srv.opts.WorkspaceDir, "worktrees", id)
	sealed := filepath.Join(wt, "sealed")
	os.MkdirAll(sealed, 0o755)
	os.WriteFile(filepath.Join(sealed, "f"), []byte("x"), 0o644)
	os.Chmod(sealed, 0o500)
	t.Cleanup(func() { os.Chmod(sealed, 0o755) })

	restore := worktreeRemove
	worktreeRemove = func(context.Context, string, string, bool) error { return errors.New("git said no") }
	t.Cleanup(func() { worktreeRemove = restore })

	rec := doDelete(t, srv, "/v1/sessions/"+id+"?force=1")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "removing it as a directory failed too") {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := srv.lookup(id); !ok {
		t.Error("a session whose checkout survived must survive too")
	}
}

// TestAFailedPruneIsNotAFailedDeletion: the directory is gone; a stale registration is
// harmless, so it is logged and the deletion stands.
func TestAFailedPruneIsNotAFailedDeletion(t *testing.T) {
	srv, _, id := newSessionInProject(t)
	srv.opts.Log = quietLog(t)
	restoreRemove, restorePrune := worktreeRemove, worktreePrune
	worktreeRemove = func(context.Context, string, string, bool) error { return errors.New("git said no") }
	worktreePrune = func(context.Context, string) error { return errors.New("prune failed") }
	t.Cleanup(func() { worktreeRemove, worktreePrune = restoreRemove, restorePrune })

	rec := doDelete(t, srv, "/v1/sessions/"+id+"?force=1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

// runGit runs git in dir and returns its combined output.
func runGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}

package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The project's main session works in the project's folder, with no worktree of its own, and its
// work must still be proposable as a pull request.
func TestPRReadyCountsWorkOfASessionWithoutAWorktree(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	gitInit(t, repo)
	c := &conversation{workspace: repo}
	if c.hasProposableWork(ctx) {
		t.Fatal("a clean repository on its default branch has nothing to propose")
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !c.hasProposableWork(ctx) {
		t.Fatal("uncommitted changes are work to propose")
	}
	mustRun(t, "git", "-C", repo, "checkout", "-q", "-b", "feature")
	mustRun(t, "git", "-C", repo, "add", "new.txt")
	mustRun(t, "git", "-C", repo, "commit", "-qm", "feat: new")
	if !c.hasProposableWork(ctx) {
		t.Fatal("commits on a work branch are work to propose")
	}
	mustRun(t, "git", "-C", repo, "checkout", "-q", "-b", "same", "main")
	if c.hasProposableWork(ctx) {
		t.Fatal("a branch with no commits beyond the default has nothing to propose")
	}
	empty := t.TempDir()
	mustRun(t, "git", "init", "-q", "-b", "main", empty)
	if (&conversation{workspace: empty}).hasProposableWork(ctx) {
		t.Fatal("a repository with no commits has nothing to propose")
	}
}

func TestPRReadyInTheStatusOfAPlainFolderSession(t *testing.T) {
	c := &conversation{id: "s", workspace: t.TempDir()}
	repo := filepath.Join(c.workspace, "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	var st SessionStatus
	c.workspaceStatus(&st)
	if st.PRReady || st.Mergeable {
		t.Fatalf("nothing done yet: %+v", st)
	}
	if err := os.WriteFile(filepath.Join(repo, "w.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.workspaceStatus(&st)
	if !st.PRReady || st.Mergeable {
		t.Fatalf("work in the project's own folder is proposable but not integrable: %+v", st)
	}
	c.merged = true
	st = SessionStatus{}
	c.workspaceStatus(&st)
	if st.PRReady {
		t.Fatal("a merged session offers nothing")
	}
}

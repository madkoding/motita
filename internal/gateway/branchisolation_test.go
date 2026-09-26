package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/gitx"
)

// This file covers the isolation of a session's worktree from the two things
// that can break it, both measured against git 2.47.3:
//
//  1. A session whose worktree was MOVED to another branch. The worktree is
//     still the session's, and anything that identifies it by its BRANCH
//     concludes otherwise. Measured consequence: `sessionWorktree` could not
//     re-attach it, `AddWorktree` failed with "already exists", and the session
//     silently fell back to the PROJECT'S checkout - two sessions editing one
//     directory, which is the exact collision worktrees exist to prevent.
//
//  2. The project's own checkout TAKING a session's branch. Git permits it once
//     the session's worktree is on some other branch, and the session is then
//     locked out of its own branch with "'motita/<id>' is already used by
//     worktree at ...". Nothing reports that at the moment it happens.

// TestSessionWorktreeSurvivesTheSessionMovingToAnotherBranch is case 1, and it
// is the reason the identification is by path. The session's worktree is moved
// to a feature branch - which is a thing the user can plainly do - and the
// worktree must still be recognised as that session's, at that path.
//
// Identifying it by branch instead makes this return the PROJECT's directory,
// because the path is no longer on the session's branch.
func TestSessionWorktreeSurvivesTheSessionMovingToAnotherBranch(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	ss := createSessionIn(t, srv, pid)
	project := p.Dir
	mustRun(t, "git", "-C", project, "branch", "feature")
	mustRun(t, "git", "-C", ss.Workspace, "checkout", "-q", "feature")

	// What a gateway restart does: the worktree is resolved again.
	got, err := srv.sessionWorktree(ctx, project, ss.ID)
	if err != nil {
		t.Fatalf("sessionWorktree after the session moved branch: %v", err)
	}
	if got != ss.Workspace {
		t.Fatalf("workspace = %q, want the session's own worktree %q: a session moved to a feature branch must keep its worktree, not fall back to the project's checkout", got, ss.Workspace)
	}
	if branch := gitx.Display(ctx, got); branch != "feature" {
		t.Errorf("the worktree must be left where the session put it, got %q", branch)
	}
}

// TestSessionWorktreeIsStillTheSessionsAfterTheProjectTookItsBranch is the two
// cases together, which is the state the isolation guard has to survive: the
// project's checkout took the session's branch, and the session's worktree was
// moved elsewhere. The session must still resolve to its OWN directory - the
// alternative is the agent writing into the project's checkout.
func TestSessionWorktreeIsStillTheSessionsAfterTheProjectTookItsBranch(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	ss := createSessionIn(t, srv, pid)
	mustRun(t, "git", "-C", p.Dir, "branch", "feature")
	mustRun(t, "git", "-C", ss.Workspace, "checkout", "-q", "feature")
	// The project takes the branch, which git allows now.
	mustRun(t, "git", "-C", p.Dir, "checkout", "-q", sessionBranch(ss.ID))

	got, err := srv.sessionWorktree(ctx, p.Dir, ss.ID)
	if err != nil {
		t.Fatalf("sessionWorktree: %v", err)
	}
	if got != ss.Workspace {
		t.Fatalf("workspace = %q, want the session's own worktree %q", got, ss.Workspace)
	}
}

// TestSessionWorktreeRunsInTheProjectWhenThereIsNoWorktreeRoot: the fallback is
// deliberate for a gateway started without a workspace root - there is nowhere
// to put a worktree, and the project directory is the right answer.
func TestSessionWorktreeRunsInTheProjectWhenThereIsNoWorktreeRoot(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	// What a gateway started without a workspace root looks like: nowhere to put
	// a worktree, so the project directory is the answer.
	srv.opts.WorkspaceDir = ""
	got, err := srv.sessionWorktree(context.Background(), p.Dir, "s-whatever")
	if err != nil {
		t.Fatalf("sessionWorktree without a workspace root: %v", err)
	}
	if got != p.Dir {
		t.Errorf("workspace = %q, want the project directory %q", got, p.Dir)
	}
}

// TestSessionWorktreeRecreatesAWorktreeRemovedByHand: a worktree whose directory
// is gone must be recreated, and it must come back on the session's branch with
// its commits. This is the restart path after a hand cleanup.
func TestSessionWorktreeRecreatesAWorktreeRemovedByHand(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, &fakeService{})
	withProjects(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	p := srv.projectOf(pid)

	ss := createSessionIn(t, srv, pid)
	if err := os.WriteFile(filepath.Join(ss.Workspace, "work.txt"), []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", ss.Workspace, "add", "work.txt")
	mustRun(t, "git", "-C", ss.Workspace, "commit", "-qm", "session work")

	if err := os.RemoveAll(ss.Workspace); err != nil {
		t.Fatal(err)
	}
	got, err := srv.sessionWorktree(ctx, p.Dir, ss.ID)
	if err != nil {
		t.Fatalf("sessionWorktree over a removed worktree: %v", err)
	}
	if got != ss.Workspace {
		t.Fatalf("workspace = %q, want %q", got, ss.Workspace)
	}
	if _, err := os.Stat(filepath.Join(got, "work.txt")); err != nil {
		t.Errorf("the session's committed work must come back with the worktree: %v", err)
	}
}

// TestSessionWorktreeReportsAProjectThatCannotHaveOne: a project that is not a
// repository cannot have a worktree, and the caller is told so rather than
// silently running somewhere else.
func TestSessionWorktreeReportsAProjectThatCannotHaveOne(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjects(t, srv, ws)
	plain := filepath.Join(ws, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := srv.sessionWorktree(context.Background(), plain, "s-1")
	if err == nil {
		t.Fatal("a project that is not a repository cannot have a worktree")
	}
	if got != plain {
		t.Errorf("the fallback must be the project directory, got %q", got)
	}
}

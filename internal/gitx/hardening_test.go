package gitx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRepositoryConfigRunsNoCode: the repository's own .git/config is writable by whatever
// worked in it, and every git this package starts runs as the user, outside the sandbox. A
// core.fsmonitor command and the hooks a commit, a merge and a checkout run are therefore
// commands an agent could have planted; none of them may run.
func TestRepositoryConfigRunsNoCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the planted commands are shell scripts")
	}
	ctx := context.Background()
	repo := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\necho \"$0\" >> " + marker + "\n"
	hooks := filepath.Join(repo, ".git", "hooks")
	for _, hook := range []string{"pre-commit", "commit-msg", "post-commit", "pre-merge-commit", "post-merge", "post-checkout"} {
		write(t, filepath.Join(hooks, hook), script)
		if err := os.Chmod(filepath.Join(hooks, hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	monitor := filepath.Join(t.TempDir(), "monitor")
	write(t, monitor, script)
	if err := os.Chmod(monitor, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "config", "core.fsmonitor", monitor)

	write(t, filepath.Join(repo, "g.txt"), "dirty\n")
	if _, err := Dirty(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitAll(ctx, repo, "work"); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "branch", "side")
	if err := Checkout(ctx, repo, "side"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "h.txt"), "side\n")
	if _, err := CommitAll(ctx, repo, "side work"); err != nil {
		t.Fatal(err)
	}
	if err := Checkout(ctx, repo, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeInto(ctx, repo, "main", "side", "merge side"); err != nil {
		t.Fatal(err)
	}
	if exists(marker) {
		t.Errorf("repository config ran code: %s", read(t, marker))
	}
}

// TestBranchNamesAreNeverOptions: a branch name reaches git as an argument, and one that starts
// with "-" would be read as a flag: --output writes a file, --upload-pack runs a command.
func TestBranchNamesAreNeverOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the planted command is a shell command")
	}
	ctx := context.Background()
	repo := newRepo(t)
	pushToOrigin(t, repo)
	marker := filepath.Join(t.TempDir(), "ran")

	if _, _, err := CommitsBetween(ctx, repo, "--output="+marker, "main"); err == nil {
		t.Error("CommitsBetween with a flag for a branch reported success")
	}
	if err := PullFastForward(ctx, repo, "--upload-pack=touch "+marker+";"); err == nil {
		t.Error("PullFastForward with a flag for a branch reported success")
	}
	if exists(marker) {
		t.Error("a branch name was read as an option")
	}
}

// Hardening hands out a copy: a caller that appends to it cannot change what this package runs.
func TestHardeningIsACopy(t *testing.T) {
	h := Hardening()
	if strings.Join(h, " ") != "-c core.hooksPath=/dev/null -c core.fsmonitor=false" {
		t.Fatalf("hardening: %q", h)
	}
	h[1] = "core.hooksPath=hooks"
	if Hardening()[1] != "core.hooksPath=/dev/null" {
		t.Error("the package's hardening changed through a copy")
	}
}

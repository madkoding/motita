package gitx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

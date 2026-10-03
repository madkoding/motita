package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/execx"
)

// TestWithDirRunsInAnotherDirectoryUnderTheSameRules: a background agent's sandbox runs its
// commands in its own worktree, keeps the parent's tools directory, and leaves the parent as it was.
func TestWithDirRunsInAnotherDirectoryUnderTheSameRules(t *testing.T) {
	base, tools, other := t.TempDir(), t.TempDir(), t.TempDir()
	s := toolsSandbox(t, base, tools)
	c := s.WithDir(other)

	out, _, exit, err := c.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", `pwd; echo "$HOME"`},
	})
	if err != nil || exit != 0 {
		t.Fatalf("exit=%d err=%v out=%q", exit, err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !resolvedSame(t, lines[0], other) || lines[1] != filepath.Join(tools, "home") {
		t.Errorf("the copy must run in %s with the same HOME, got %q", other, out)
	}
	if c.Base() != other || s.Base() != base {
		t.Errorf("bases: copy %q (want %q), original %q (want %q)", c.Base(), other, s.Base(), base)
	}
	if rel := s.WithDir("sub"); rel.Base() != filepath.Join(base, "sub") {
		t.Errorf("a relative dir is taken against the base, got %q", rel.Base())
	}
}

// resolvedSame compares two directories through their symlinks (/tmp is often one).
func resolvedSame(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		t.Fatalf("EvalSymlinks: %v %v", errA, errB)
	}
	return ra == rb
}

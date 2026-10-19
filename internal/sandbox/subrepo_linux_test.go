package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/execx"
)

// A project holds repositories in its subfolders: git runs there, from the project's root with `-C`
// or after a `cd`, and with the whole disk allowed it runs in a repository anywhere.
func TestGitRunsInASubfolderRepository(t *testing.T) {
	needLandlock(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	project, elsewhere := t.TempDir(), t.TempDir()
	sub := filepath.Join(project, "sub")
	os.MkdirAll(sub, 0o755)
	for _, dir := range []string{sub, elsewhere} {
		cmd := exec.Command("git", "init", "-q", "-b", "main")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	const commit = "git -c commit.gpgsign=false -c user.name=t -c user.email=t@t commit -q --allow-empty -m "
	s := confined(t, project)
	for name, line := range map[string]string{
		"-C":       "git -C sub " + commit[4:] + "inside && git -C sub log --oneline | head -1",
		"cd":       "cd sub && " + commit + "inside && git log --oneline | head -1",
		"from sub": "cd sub && echo inside > f.txt && git add f.txt && " + commit + "inside && cat f.txt",
	} {
		out, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", line}})
		if err != nil || exit != 0 || !strings.Contains(out, "inside") {
			t.Errorf("%s: exit=%d err=%v out=%q", name, exit, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(sub, "f.txt")); err != nil {
		t.Error(err)
	}

	// Confined to the project, a repository elsewhere is not written; with the whole disk it is.
	line := "git -C " + elsewhere + " " + commit[4:] + "far && git -C " + elsewhere + " log --oneline | head -1"
	out, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", line}})
	if exit == 0 && err == nil && out != "" {
		t.Error("a repository outside the project must not be written in workspace mode")
	}
	s.SetFullDisk(true)
	out, _, exit, err = s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", line}})
	if err != nil || exit != 0 || !strings.Contains(out, "far") {
		t.Errorf("full disk: exit=%d err=%v out=%q", exit, err, out)
	}
}

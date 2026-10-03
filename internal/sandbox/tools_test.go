package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

// toolsSandbox is a sandbox whose tools directory is tools.
func toolsSandbox(t *testing.T, base, tools string) *Sandbox {
	t.Helper()
	s, err := New(Options{Dir: base, Timeout: 30 * time.Second, MaxOutputKB: 64, ToolsDir: tools})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestToolsDirKeepsHomeTmpAndToolchainsOutOfTheWorkingDirectory replays the real session: HOME
// and TMPDIR were the repository, so what the agent installed landed in it, and nothing it
// installed was on PATH in the next round. With a tools directory HOME and TMPDIR live there,
// and a toolchain unpacked into tools/<name>/bin is found by name in the NEXT run.
func TestToolsDirKeepsHomeTmpAndToolchainsOutOfTheWorkingDirectory(t *testing.T) {
	base, tools := t.TempDir(), t.TempDir()
	s := toolsSandbox(t, base, tools)

	out, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", `echo "$HOME|$TMPDIR|$PATH"`},
	})
	if err != nil || exit != 0 {
		t.Fatalf("exit=%d err=%v out=%q", exit, err, out)
	}
	parts := strings.Split(strings.TrimSpace(out), "|")
	if parts[0] != filepath.Join(tools, "home") {
		t.Errorf("HOME = %q, want the tools directory's home", parts[0])
	}
	if !strings.HasPrefix(parts[1], filepath.Join(tools, "tmp", "tmp-")) {
		t.Errorf("TMPDIR = %q, want a tmp-* under the tools directory", parts[1])
	}
	if parts[2] != systemPath {
		t.Errorf("with nothing installed PATH is the system one, got %q", parts[2])
	}
	if entries, _ := filepath.Glob(filepath.Join(base, "tmp-*")); len(entries) != 0 {
		t.Errorf("nothing may be left in the working directory: %v", entries)
	}

	// A toolchain installed in one round...
	bin := filepath.Join(tools, "tools", "fake", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "faketool"), []byte("#!/bin/sh\necho found-it\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// ...is run by name in the next, also as the command itself (the anchor's `make`).
	out, _, exit, err = s.Run(context.Background(), execx.Request{Command: "faketool"})
	if err != nil || exit != 0 || strings.TrimSpace(out) != "found-it" {
		t.Fatalf("the installed tool must be on PATH: exit=%d err=%v out=%q", exit, err, out)
	}
}

// TestToolsDirIsIgnoredUnderAChroot: a chroot cannot see a directory outside its root.
func TestToolsDirIsIgnoredUnderAChroot(t *testing.T) {
	s := &Sandbox{op: Options{UseChroot: true, ToolsDir: t.TempDir()}, base: t.TempDir()}
	if s.toolsDir() != "" || s.tempRoot() != s.base {
		t.Error("under a chroot the old layout is kept")
	}
	env := strings.Join(s.environmentWithTmp("/x"), "\n")
	if !strings.Contains(env, "HOME="+s.base) || !strings.Contains(env, "PATH="+systemPath+"\n") {
		t.Errorf("under a chroot HOME is the base and PATH the system one: %s", env)
	}
}

// TestTempRootFallsBackWhenTheToolsDirectoryCannotBePrepared: a tools directory that cannot hold
// a tmp folder must not stop the run; it goes back to the working directory.
func TestTempRootFallsBackWhenTheToolsDirectoryCannotBePrepared(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := runnableSandbox(t, t.TempDir())
	s.op.ToolsDir = file
	if got := s.tempRoot(); got != s.base {
		t.Errorf("tempRoot = %q, want the base %q", got, s.base)
	}
}

// TestToolPath: <tools>/bin first, then each tools/<name>/bin in order, then the given PATH; a
// file named bin is not a directory and is skipped; no tools directory changes nothing.
func TestToolPath(t *testing.T) {
	if got := ToolPath(" ", "/usr/bin"); got != "/usr/bin" {
		t.Errorf("no tools directory: %q", got)
	}
	tools := t.TempDir()
	if got := ToolPath(tools, ""); got != "" {
		t.Errorf("an empty tools directory and no PATH: %q", got)
	}
	for _, d := range []string{"bin", "tools/node/bin", "tools/go/bin"} {
		if err := os.MkdirAll(filepath.Join(tools, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(tools, "tools", "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "tools", "broken", "bin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		filepath.Join(tools, "bin"), filepath.Join(tools, "tools/go/bin"), filepath.Join(tools, "tools/node/bin"), "/usr/bin",
	}, string(os.PathListSeparator))
	if got := ToolPath(tools, "/usr/bin"); got != want {
		t.Errorf("ToolPath = %q, want %q", got, want)
	}
}

// TestLookTool: an installed executable is found by name; a path, a missing name, a directory and
// a file that cannot be executed are all left as given.
func TestLookTool(t *testing.T) {
	tools := t.TempDir()
	bin := filepath.Join(tools, "tools", "x", "bin")
	if err := os.MkdirAll(filepath.Join(bin, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tool"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "plain"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LookTool(tools, "tool"); got != filepath.Join(bin, "tool") {
		t.Errorf("LookTool(tool) = %q", got)
	}
	for _, name := range []string{"./tool", "missing", "adir", "plain"} {
		if got := LookTool(tools, name); got != name {
			t.Errorf("LookTool(%q) = %q, want it unchanged", name, got)
		}
	}
}

// TestToolEnvironment: the inherited environment keeps everything but PATH, which gets the tools
// in front; with no environment given, the process' own is used.
func TestToolEnvironment(t *testing.T) {
	tools := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tools, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ToolEnvironment(tools, []string{"A=1", "PATH=/usr/bin"})
	want := []string{"A=1", "PATH=" + filepath.Join(tools, "bin") + string(os.PathListSeparator) + "/usr/bin"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ToolEnvironment = %v, want %v", got, want)
	}
	t.Setenv("MOTITA_TOOLS_PROBE", "yes")
	inherited := strings.Join(ToolEnvironment(tools, nil), "\n")
	if !strings.Contains(inherited, "MOTITA_TOOLS_PROBE=yes") || !strings.Contains(inherited, "PATH="+filepath.Join(tools, "bin")) {
		t.Errorf("the process environment must be inherited with the tools on PATH: %s", inherited)
	}
}

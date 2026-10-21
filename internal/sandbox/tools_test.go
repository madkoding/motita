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
	if parts[2] != s.commandPath() || !strings.HasPrefix(parts[2], systemPath) {
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
	env := strings.Join(s.environmentWithTmp("/x", false), "\n")
	if !strings.Contains(env, "HOME="+s.base) || !strings.Contains(env, "PATH="+systemPath+"\n") {
		t.Errorf("under a chroot HOME is the base and PATH the system one: %s", env)
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
	if !strings.Contains(inherited, "MOTITA_TOOLS_PROBE=yes") ||
		!strings.Contains(inherited, "PATH="+filepath.Join(tools, "bin")) {
		t.Errorf("the process environment must be inherited with the tools on PATH: %s", inherited)
	}
}

// A file one command leaves for the next needs somewhere that survives it: $TMPDIR is deleted with
// the command and /tmp is refused. The scratch directory is in HOME, and what is old goes.
func TestTheScratchDirectorySurvivesTheCommandAndDropsOldFiles(t *testing.T) {
	tools := t.TempDir()
	s, err := New(Options{Dir: t.TempDir(), ToolsDir: tools})
	if err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(s.Home(), "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(scratch, "old.txt")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * scratchMaxAge)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	out, _, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", `echo "$MOTITA_SCRATCH" && echo kept > "$MOTITA_SCRATCH/new.txt"`}})
	if err != nil || !strings.Contains(out, scratch) {
		t.Fatalf("err=%v out=%q", err, out)
	}
	if _, err := os.Stat(filepath.Join(scratch, "new.txt")); err != nil {
		t.Error("a file in the scratch directory must outlive the command")
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("a file older than the limit must be removed")
	}
}

func TestNoScratchDirectoryWithoutToolsDirectory(t *testing.T) {
	s, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.scratchDir(); got != "" {
		t.Errorf("scratchDir = %q, want none", got)
	}
}

func TestScratchDirectoryThatCannotBeMadeIsNone(t *testing.T) {
	tools := t.TempDir()
	s, err := New(Options{Dir: t.TempDir(), ToolsDir: tools})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Home(), "scratch"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.scratchDir(); got != "" {
		t.Errorf("scratchDir = %q, want none", got)
	}
}

// A command that leaves a read-only directory in $TMPDIR (Go's module cache, `chmod -R a-w`) used
// to leave the whole temporary directory behind: os.RemoveAll cannot empty it.
func TestATemporaryDirectoryMadeReadOnlyIsStillRemoved(t *testing.T) {
	tools := t.TempDir()
	s := toolsSandbox(t, t.TempDir(), tools)
	out, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", `mkdir -p "$TMPDIR/ro/deep" && touch "$TMPDIR/ro/deep/f" && chmod -R a-w "$TMPDIR/ro"`},
	})
	if err != nil || exit != 0 {
		t.Fatalf("exit=%d err=%v out=%q", exit, err, out)
	}
	if left, _ := filepath.Glob(filepath.Join(tools, "tmp", "tmp-*")); len(left) != 0 {
		t.Errorf("the temporary directory was left behind: %v", left)
	}
}

// A temporary directory a dead gateway left is removed by the next run once it is a day old; a
// recent one may belong to a run in progress, and neither is touched when keeping them is asked.
func TestStaleTemporaryDirectoriesAreSwept(t *testing.T) {
	tools := t.TempDir()
	root := filepath.Join(tools, "tmp")
	old, recent, other := filepath.Join(root, "tmp-old"), filepath.Join(root, "tmp-recent"), filepath.Join(root, "other")
	for _, d := range []string{filepath.Join(old, "ro"), recent, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(old, "ro"), 0o555); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * staleTempAge)
	for _, d := range []string{old, other} {
		if err := os.Chtimes(d, past, past); err != nil {
			t.Fatal(err)
		}
	}

	kept := &Sandbox{op: Options{ToolsDir: tools, Keep: true}, base: t.TempDir()}
	kept.tempRoot()
	if _, err := os.Stat(old); err != nil {
		t.Error("with keep_ephemeral nothing is swept")
	}

	s := &Sandbox{op: Options{ToolsDir: tools}, base: t.TempDir()}
	if got := s.tempRoot(); got != root {
		t.Fatalf("tempRoot = %q", got)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("a day-old temporary directory must be swept")
	}
	for _, d := range []string{recent, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s must stay: %v", d, err)
		}
	}
}

// removeTree reports what it still could not remove.
func TestRemoveTreeReportsWhatItCannotRemove(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root removes it anyway")
	}
	if err := removeTree(dir); err == nil {
		t.Error("a directory in a read-only parent cannot be removed, and must say so")
	}
}

// The PATH a command gets keeps the system-wide installs of motita's own PATH (/usr/local/go/bin,
// /snap/bin, /opt/homebrew/bin): with systemPath alone `go` and `node` were "not found" on machines
// that have them. The user's home, relative entries and duplicates stay out.
func TestCommandPathKeepsTheMachinesToolchains(t *testing.T) {
	onPlatform(t, "linux", map[string]string{
		"PATH": "/opt/go/bin:/usr/bin:relative:/home/me/.cargo/bin:/snap/bin/:/work/root/bin",
	})
	s := &Sandbox{op: Options{GitHome: "/home/me", PrivateRoots: []string{"/work/root"}}}
	want := systemPath + ":/opt/go/bin:/snap/bin"
	if got := s.commandPath(); got != want {
		t.Errorf("commandPath = %q, want %q", got, want)
	}
	if !s.HasTool("sh") {
		t.Error("sh is on the system PATH")
	}

	chroot := &Sandbox{op: Options{UseChroot: true}}
	if got := chroot.commandPath(); got != systemPath {
		t.Errorf("under a chroot commandPath = %q, want the system one", got)
	}
}

// On Windows there is no POSIX system PATH: a command got "/usr/local/sbin:..." and found nothing,
// not even PowerShell's own modules. It gets motita's PATH, the profiles left out.
func TestCommandPathOnWindowsIsMotitasOwn(t *testing.T) {
	onPlatform(t, "windows", map[string]string{"PATH": "/c/Windows/System32:/c/Users/me/bin", "USERPROFILE": "/c/Users/me"})
	s := &Sandbox{}
	if got := s.commandPath(); got != "/c/Windows/System32" {
		t.Errorf("commandPath = %q", got)
	}
}

// A Windows command gets what Windows programs need to start (SystemRoot, PATHEXT...), its
// temporary directory as TEMP and TMP, and its profile folders in the sandbox's HOME.
func TestWindowsCommandsGetTheSystemVariables(t *testing.T) {
	onPlatform(t, "windows", map[string]string{"SystemRoot": `C:\Windows`, "PATHEXT": ".COM;.EXE"})
	s := &Sandbox{base: "/work"}
	env := strings.Join(s.environmentWithTmp("/tmp/run", false), "\n") + "\n"
	for _, want := range []string{"TEMP=/tmp/run\n", "TMP=/tmp/run\n", "USERPROFILE=/work\n",
		"APPDATA=" + filepath.Join("/work", "AppData", "Roaming") + "\n",
		"LOCALAPPDATA=" + filepath.Join("/work", "AppData", "Local") + "\n",
		"SystemRoot=C:\\Windows\n", "PATHEXT=.COM;.EXE\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q in\n%s", want, env)
		}
	}
	if strings.Contains(env, "ComSpec=") {
		t.Error("a variable motita does not have is not invented")
	}

	onPlatform(t, "linux", nil)
	if env := strings.Join(s.environmentWithTmp("/tmp/run", false), "\n"); strings.Contains(env, "TEMP=") {
		t.Errorf("only Windows gets TEMP: %s", env)
	}
}

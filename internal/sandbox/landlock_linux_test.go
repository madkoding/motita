//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/madkoding/motita/internal/execx"
)

func needLandlock(t *testing.T) {
	t.Helper()
	if landlockABI() == 0 {
		t.Skip("this kernel has no Landlock")
	}
}

func confined(t *testing.T, dir string) *Sandbox {
	t.Helper()
	s, err := New(Options{Dir: dir, ConfineWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A script inside the workspace that walks out of it and writes there is stopped by the kernel,
// which is the case no reading of the command line can see.
func TestAScriptCannotWriteOutsideTheWorkspace(t *testing.T) {
	needLandlock(t)
	base, other := t.TempDir(), t.TempDir()
	s := confined(t, base)
	script := "cd " + other + " && echo x > stolen.txt; echo y > inside.txt; mkdir sub; echo done"
	out, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(other, "stolen.txt")); statErr == nil {
		t.Fatalf("a write outside the workspace went through:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(other, "sub")); statErr == nil {
		t.Fatal("a directory was created outside the workspace")
	}
	if !strings.Contains(out, "done") || !strings.Contains(out, "denied") {
		t.Errorf("the command must run and report the refusal: %q", out)
	}
}

func TestTheWorkspaceItsTempAndDevStayWritable(t *testing.T) {
	needLandlock(t)
	base := t.TempDir()
	s := confined(t, base)
	script := `echo a > a.txt && mkdir d && echo b > d/b.txt && mv a.txt d/ && echo "$TMPDIR" && echo t > "$TMPDIR/t" && echo ok > /dev/null && echo fine`
	out, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", script}})
	if err != nil || exit != 0 || !strings.Contains(out, "fine") {
		t.Fatalf("exit=%d err=%v out=%q", exit, err, out)
	}
	if _, statErr := os.Stat(filepath.Join(base, "d", "a.txt")); statErr != nil {
		t.Error("a rename inside the workspace must work")
	}
	if !contains(s.Isolation(), ModeWriteConfine) {
		t.Error("the confinement must be reported as applied")
	}
}

func TestAnApprovedCommandIsNotConfined(t *testing.T) {
	needLandlock(t)
	base, other := t.TempDir(), t.TempDir()
	s := confined(t, base)
	_, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh",
		Args: []string{"-c", "echo x > " + filepath.Join(other, "ok.txt")}, Unconfined: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(other, "ok.txt")); statErr != nil {
		t.Error("what the user approved must be able to write where it says")
	}
}

func TestAWorktreeCanWriteItsGitDirectories(t *testing.T) {
	needLandlock(t)
	root := t.TempDir()
	common := filepath.Join(root, "main", ".git")
	own := filepath.Join(common, "worktrees", "w")
	work := filepath.Join(root, "work")
	for _, d := range []string{own, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(own, "commondir"), []byte("../..\n"), 0o644)
	os.WriteFile(filepath.Join(work, ".git"), []byte("gitdir: "+own+"\n"), 0o644)
	s := confined(t, work)
	_, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh",
		Args: []string{"-c", "echo x > " + filepath.Join(common, "objects-marker") + " && echo y > " + filepath.Join(own, "HEAD")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(common, "objects-marker"), filepath.Join(own, "HEAD")} {
		if _, statErr := os.Stat(f); statErr != nil {
			t.Errorf("%s: the worktree's git directories must stay writable", f)
		}
	}
}

func TestGitDirsReadsWhatAWorktreeSays(t *testing.T) {
	d := t.TempDir()
	if gitDirs(d) != nil {
		t.Error("no .git, nothing to add")
	}
	os.WriteFile(filepath.Join(d, ".git"), []byte("not a pointer"), 0o644)
	if gitDirs(d) != nil {
		t.Error("a .git that is not a pointer adds nothing")
	}
	os.WriteFile(filepath.Join(d, ".git"), []byte("gitdir: rel/dir\n"), 0o644)
	got := gitDirs(d)
	if len(got) != 1 || got[0] != filepath.Join(d, "rel/dir") {
		t.Errorf("a relative gitdir is taken against the worktree: %v", got)
	}
	os.MkdirAll(filepath.Join(d, "rel/dir"), 0o755)
	os.WriteFile(filepath.Join(d, "rel/dir/commondir"), []byte("/abs/common\n"), 0o644)
	if got := gitDirs(d); len(got) != 2 || got[1] != "/abs/common" {
		t.Errorf("an absolute commondir is kept: %v", got)
	}
}

// Every failing step is reported, and a missing root is skipped, not an error.
func TestConfineWritesReportsEachFailingStep(t *testing.T) {
	needLandlock(t)
	real := landlockSyscall
	t.Cleanup(func() { landlockSyscall = real })

	for call, name := range map[int]string{2: "create", 3: "add_rule", 5: "prctl", 6: "restrict", 99: "none"} {
		n := 0
		landlockSyscall = func(trap, a1, a2, a3 uintptr) (uintptr, uintptr, syscall.Errno) {
			n++
			if n == call {
				return 0, 0, syscall.EPERM
			}
			if trap == sysLandlockCreateRuleset && a1 == 0 {
				return 3, 0, 0
			}
			if trap == sysLandlockCreateRuleset {
				return uintptr(mustOpenDevNull(t)), 0, 0
			}
			return 0, 0, 0
		}
		err := confineWrites([]string{t.TempDir(), "/dev", "/does/not/exist"})
		if (err == nil) != (name == "none") {
			t.Errorf("%s: a failing step must fail the confinement, and only a failing step: %v", name, err)
		}
	}
	landlockSyscall = func(uintptr, uintptr, uintptr, uintptr) (uintptr, uintptr, syscall.Errno) {
		return 0, 0, syscall.ENOSYS
	}
	if landlockABI() != 0 || confineWrites(nil) == nil {
		t.Error("with no Landlock the ABI is 0 and confining fails")
	}
}

func mustOpenDevNull(t *testing.T) int {
	fd, err := syscall.Open("/dev/null", syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

func TestAWriteRightsAreMaskedByTheABI(t *testing.T) {
	if writeAccess(1)&accessRefer != 0 || writeAccess(2)&accessTruncate != 0 ||
		writeAccess(3)&accessRefer == 0 || writeAccess(3)&accessTruncate == 0 {
		t.Error("each ABI may only handle the rights it knows")
	}
}

func TestWithoutLandlockTheSandboxSaysSoAndDoesNotConfine(t *testing.T) {
	real := landlockSyscall
	t.Cleanup(func() { landlockSyscall = real })
	landlockSyscall = func(uintptr, uintptr, uintptr, uintptr) (uintptr, uintptr, syscall.Errno) {
		return 0, 0, syscall.ENOSYS
	}
	s := confined(t, t.TempDir())
	if contains(s.Isolation(), ModeWriteConfine) || len(s.NotApplied()) == 0 {
		t.Errorf("it must not claim an isolation it does not have: %v %v", s.Isolation(), s.NotApplied())
	}
}

func TestAConfinementThatFailsInTheChildStopsTheCommand(t *testing.T) {
	real := confineWritesHook
	t.Cleanup(func() { confineWritesHook = real })
	confineWritesHook = func([]string) error { return syscall.EPERM }
	err := RunAsChild([]string{ChildMarker, `{"command":"/bin/true","write_roots":["/tmp"]}`})
	if err == nil || !strings.Contains(err.Error(), "could not confine writes") {
		t.Fatalf("got %v", err)
	}
}

func contains(modes []Mode, m Mode) bool {
	for _, x := range modes {
		if x == m {
			return true
		}
	}
	return false
}

func TestARootThatCannotBeOpenedIsSkipped(t *testing.T) {
	real := openPath
	t.Cleanup(func() { openPath = real })
	openPath = func(string, int, uint32) (int, error) { return -1, syscall.EACCES }
	if err := allowWrites(0, t.TempDir(), 0); err != nil {
		t.Errorf("there is nothing to grant on a path that cannot be named: %v", err)
	}
}

func TestTheRootsIncludeTheToolsAndAnotherWorkingDirectory(t *testing.T) {
	base, tools, other := t.TempDir(), t.TempDir(), t.TempDir()
	s, err := New(Options{Dir: base, ToolsDir: tools, ConfineWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(base, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/b\n"), 0o644)
	roots := strings.Join(s.writeRoots(other, "/tmp/x"), " ")
	for _, want := range []string{tools, other, base, "/tmp/x", "/dev", "/elsewhere/.git/worktrees/b"} {
		if !strings.Contains(roots, want) {
			t.Errorf("%s is missing from %s", want, roots)
		}
	}
}

// The case that motivated the git directories: a commit made from a session worktree writes into
// the main repository's .git, outside the working directory.
func TestAGitCommitInAConfinedWorktreeWorks(t *testing.T) {
	needLandlock(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	main, work := filepath.Join(root, "main"), filepath.Join(root, "work")
	git := func(dir string, args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(main, 0o755)
	git(main, "init", "-q", "-b", "main")
	git(main, "commit", "-q", "--allow-empty", "-m", "init")
	git(main, "worktree", "add", "-q", work, "-b", "feature")
	s := confined(t, work)
	out, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c",
		"echo x > f.txt && git add f.txt && git -c commit.gpgsign=false -c user.name=t -c user.email=t@t commit -q -m work && git log --oneline | head -1"},
		Environment: nil})
	if err != nil || exit != 0 || !strings.Contains(out, "work") {
		t.Fatalf("exit=%d err=%v out=%q", exit, err, out)
	}
}

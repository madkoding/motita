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

// fakeWorktree lays out what `git worktree add` leaves: the repository's .git with HEAD, objects,
// refs and logs, the worktree's registration under it naming the worktree back, and the worktree's
// own `.git` file pointing at the registration.
func fakeWorktree(t *testing.T) (common, own, work string) {
	t.Helper()
	root := t.TempDir()
	common = filepath.Join(root, "main", ".git")
	own = filepath.Join(common, "worktrees", "w")
	work = filepath.Join(root, "work")
	for _, d := range []string{own, work, filepath.Join(common, "objects"), filepath.Join(common, "refs"),
		filepath.Join(common, "logs"), filepath.Join(common, "hooks")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(common, "HEAD"):       "ref: refs/heads/main\n",
		filepath.Join(common, "config"):     "[core]\n",
		filepath.Join(own, "HEAD"):          "ref: refs/heads/w\n",
		filepath.Join(own, "commondir"):     "../..\n",
		filepath.Join(own, "gitdir"):        filepath.Join(work, ".git") + "\n",
		filepath.Join(work, ".git"):         "gitdir: " + own + "\n",
		filepath.Join(common, "hooks", "x"): "",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return common, own, work
}

func TestAWorktreeCanWriteItsGitDirectories(t *testing.T) {
	needLandlock(t)
	common, own, work := fakeWorktree(t)
	s := confined(t, work)
	_, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh",
		Args: []string{"-c", "echo x > " + filepath.Join(common, "objects", "marker") + "; echo y > " + filepath.Join(own, "HEAD") +
			"; echo z > " + filepath.Join(common, "config") + "; echo h > " + filepath.Join(common, "hooks", "pre-commit")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(common, "objects", "marker"), filepath.Join(own, "HEAD")} {
		if _, statErr := os.Stat(f); statErr != nil {
			t.Errorf("%s: the worktree's git directories must stay writable", f)
		}
	}
	// What git obeys when it runs later outside the sandbox stays out of reach.
	if data, _ := os.ReadFile(filepath.Join(common, "config")); string(data) != "[core]\n" {
		t.Errorf("the repository's config was rewritten: %q", data)
	}
	if _, statErr := os.Stat(filepath.Join(common, "hooks", "pre-commit")); statErr == nil {
		t.Error("a hook was planted in the repository")
	}
}

// The attack: the agent rewrites its worktree's `.git` to point at a directory it wants to write.
// The pointer read when the sandbox was built is the one that counts, and a pointer to something
// that is not a registered worktree of this directory grants nothing.
func TestARewrittenGitPointerGrantsNothing(t *testing.T) {
	needLandlock(t)
	_, _, work := fakeWorktree(t)
	victim := t.TempDir()
	s := confined(t, work)
	os.WriteFile(filepath.Join(work, ".git"), []byte("gitdir: "+victim+"\n"), 0o644)
	_, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh",
		Args: []string{"-c", "echo x > " + filepath.Join(victim, ".bashrc")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(victim, ".bashrc")); statErr == nil {
		t.Error("a rewritten .git made a directory outside the workspace writable")
	}
	if got := gitDirs(work); got != nil {
		t.Errorf("a pointer to a directory that is no worktree registration must add nothing: %v", got)
	}
}

func TestGitDirsChecksEveryLinkOfTheChain(t *testing.T) {
	d := t.TempDir()
	if gitDirs(d) != nil {
		t.Error("no .git, nothing to add")
	}
	os.WriteFile(filepath.Join(d, ".git"), []byte("not a pointer"), 0o644)
	if gitDirs(d) != nil {
		t.Error("a .git that is not a pointer adds nothing")
	}

	common, own, work := fakeWorktree(t)
	if got := gitDirs(work); len(got) != 4 || got[0] != own || got[1] != filepath.Join(common, "objects") {
		t.Fatalf("a real worktree grants its registration, objects, refs and logs: %v", got)
	}
	os.WriteFile(filepath.Join(work, ".git"), []byte("gitdir: ../main/.git/worktrees/w\n"), 0o644)
	if got := gitDirs(work); len(got) != 4 {
		t.Errorf("a relative gitdir is taken against the worktree: %v", got)
	}
	os.RemoveAll(filepath.Join(common, "logs"))
	if got := gitDirs(work); len(got) != 3 {
		t.Errorf("a directory that is not there is not granted: %v", got)
	}

	breaks := []struct {
		name string
		do   func()
		undo func()
	}{
		{"a registration that does not name this worktree back",
			func() { os.WriteFile(filepath.Join(own, "gitdir"), []byte("/elsewhere/.git\n"), 0o644) },
			func() { os.WriteFile(filepath.Join(own, "gitdir"), []byte(filepath.Join(work, ".git")+"\n"), 0o644) }},
		{"a registration with no back pointer",
			func() { os.Rename(filepath.Join(own, "gitdir"), filepath.Join(own, "gitdir.x")) },
			func() { os.Rename(filepath.Join(own, "gitdir.x"), filepath.Join(own, "gitdir")) }},
		{"a registration with no commondir",
			func() { os.Rename(filepath.Join(own, "commondir"), filepath.Join(own, "commondir.x")) },
			func() { os.Rename(filepath.Join(own, "commondir.x"), filepath.Join(own, "commondir")) }},
		{"a commondir naming another repository",
			func() { os.WriteFile(filepath.Join(own, "commondir"), []byte("/\n"), 0o644) },
			func() { os.WriteFile(filepath.Join(own, "commondir"), []byte("../..\n"), 0o644) }},
		{"a repository whose objects are a link",
			func() {
				os.Rename(filepath.Join(common, "objects"), filepath.Join(common, "objects.x"))
				os.Symlink(t.TempDir(), filepath.Join(common, "objects"))
			},
			func() {
				os.Remove(filepath.Join(common, "objects"))
				os.Rename(filepath.Join(common, "objects.x"), filepath.Join(common, "objects"))
			}},
		{"a registration with no HEAD",
			func() { os.Rename(filepath.Join(own, "HEAD"), filepath.Join(own, "HEAD.x")) },
			func() { os.Rename(filepath.Join(own, "HEAD.x"), filepath.Join(own, "HEAD")) }},
	}
	for _, b := range breaks {
		b.do()
		if got := gitDirs(work); got != nil {
			t.Errorf("%s must grant nothing: %v", b.name, got)
		}
		b.undo()
		if got := gitDirs(work); got == nil {
			t.Fatalf("%s: the layout was not restored", b.name)
		}
	}
	// A directory that is not under a `worktrees` directory is no registration.
	os.WriteFile(filepath.Join(work, ".git"), []byte("gitdir: "+common+"\n"), 0o644)
	if got := gitDirs(work); got != nil {
		t.Errorf("the repository itself is not a worktree registration: %v", got)
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
		err := confineWrites([]string{t.TempDir(), "/dev", "/does/not/exist"}, nil)
		if (err == nil) != (name == "none") {
			t.Errorf("%s: a failing step must fail the confinement, and only a failing step: %v", name, err)
		}
	}
	landlockSyscall = func(uintptr, uintptr, uintptr, uintptr) (uintptr, uintptr, syscall.Errno) {
		return 0, 0, syscall.ENOSYS
	}
	if landlockABI() != 0 || confineWrites(nil, nil) == nil {
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
	confineWritesHook = func([]string, []string) error { return syscall.EPERM }
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
	_, own, base := fakeWorktree(t)
	_, otherOwn, other := fakeWorktree(t)
	tools := t.TempDir()
	s, err := New(Options{Dir: base, ToolsDir: tools, ConfineWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	got := s.writeRoots(other, "/tmp/x")
	roots := strings.Join(got, " ")
	for _, want := range []string{filepath.Join(tools, "home"), other, base, "/tmp/x", "/dev", own, otherOwn} {
		if !strings.Contains(roots, want) {
			t.Errorf("%s is missing from %s", want, roots)
		}
	}
	for _, root := range got {
		if root == tools {
			t.Errorf("the whole tools directory must not be writable: %v", got)
		}
	}
}

// The tools directory's bin/ comes first on every project's PATH: a confined command cannot plant
// a program there, only in its HOME, and installing is what the user approves.
func TestAConfinedCommandCannotPlantASharedTool(t *testing.T) {
	needLandlock(t)
	tools := t.TempDir()
	os.MkdirAll(filepath.Join(tools, "bin"), 0o755)
	os.MkdirAll(filepath.Join(tools, "tools"), 0o755)
	s, err := New(Options{Dir: t.TempDir(), ToolsDir: tools, ConfineWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	script := "echo x > " + filepath.Join(tools, "bin", "git") + "; mkdir " + filepath.Join(tools, "tools", "evil") +
		"; echo h > \"$HOME/.cache-file\" && echo home-writable"
	out, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	for _, planted := range []string{filepath.Join(tools, "bin", "git"), filepath.Join(tools, "tools", "evil")} {
		if _, statErr := os.Stat(planted); statErr == nil {
			t.Errorf("%s was planted by a confined command", planted)
		}
	}
	if !strings.Contains(out, "home-writable") {
		t.Errorf("the sandbox's HOME must stay writable: %q", out)
	}
	_, _, _, err = s.Run(context.Background(), execx.Request{Command: "/bin/sh",
		Args: []string{"-c", "mkdir -p " + filepath.Join(tools, "tools", "go", "bin")}, Unconfined: true})
	if _, statErr := os.Stat(filepath.Join(tools, "tools", "go", "bin")); err != nil || statErr != nil {
		t.Errorf("an approved install must still work: %v %v", err, statErr)
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

// A mapping that cannot be made fails the step that needed it, instead of passing the kernel a bad address.
func TestConfinementFailsWhenTheKernelBufferCannotBeMapped(t *testing.T) {
	needLandlock(t)
	real := mmapAnon
	t.Cleanup(func() { mmapAnon = real })
	mmapAnon = func(int, int64, int, int, int) ([]byte, error) { return nil, syscall.ENOMEM }
	if err := confineWrites([]string{t.TempDir()}, nil); err == nil || !strings.Contains(err.Error(), "create_ruleset") {
		t.Errorf("the ruleset buffer: %v", err)
	}
	if err := allowWrites(3, t.TempDir(), accessReadFile); err == nil || !strings.Contains(err.Error(), "add_rule") {
		t.Errorf("the rule buffer: %v", err)
	}
}

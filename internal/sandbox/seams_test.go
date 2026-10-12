//go:build linux

// The sandbox's isolation is Linux-specific (ulimit, cgroups v1, chroot, dropping
// privileges). These tests exercise that code, so they only build where it exists;
// the portable behaviour is covered by portable_test.go.

package sandbox

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/madkoding/motita/internal/logx"
)

// ---------------------------------------------------------------------------
// Tests of the injectable seams and of the OS error paths that cannot be
// provoked with a plain filesystem setup (there is no root and no cgroups v1 in
// the test environments).
//
// The seams (childHooks, jsonMarshal, chrootHooks, privilegeHooks, osHooks) hold
// the real operations in production; the tests that replace them restore them
// with defer, and TestProductionSeamsAreTheRealOperations checks that the values
// production uses are still the real ones.
// ---------------------------------------------------------------------------

// TestProductionSeamsAreTheRealOperations: replacing a seam in a test must never
// be able to change what production does, so the defaults are asserted.
func TestProductionSeamsAreTheRealOperations(t *testing.T) {
	if got, want := osHooks.euid(), os.Geteuid(); got != want {
		t.Errorf("osHooks.euid() = %d, the real one is %d", got, want)
	}
	executable, err := osHooks.executable()
	if err != nil || !filepath.IsAbs(executable) {
		t.Errorf("osHooks.executable() = %q, %v (an absolute path was expected)", executable, err)
	}

	spec := Spec{Command: "/bin/true", Limits: Limits{MemoryMB: 1}}
	want, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsonMarshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("jsonMarshal is not the stdlib marshaller: %s != %s", got, want)
	}
}

// TestProductionHooksAreTheRealSyscalls: the chroot hooks must be the real
// syscalls (a stub would not fail here). The privilege hooks are checked by
// identity, comparing the function pointers, because as a non-root user only
// setgroups fails (setgid and setuid succeed for the process's own ids) and
// calling them for real would make that impossible to observe.
func TestProductionHooksAreTheRealSyscalls(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root these syscalls succeed and would alter the test process")
	}
	if err := chrootHooks.chroot("/does/not/exist/the-production-hook"); err == nil {
		t.Error("the production chroot hook must be syscall.Chroot")
	}
	if err := chrootHooks.chdir("/does/not/exist/the-production-hook"); err == nil {
		t.Error("the production chdir hook must be syscall.Chdir")
	}
	if err := privilegeHooks.setgroups([]int{os.Getgid()}); err == nil {
		t.Error("the production setgroups hook must be syscall.Setgroups")
	}
	if reflect.ValueOf(privilegeHooks.setgid).Pointer() != reflect.ValueOf(syscall.Setgid).Pointer() {
		t.Error("the production setgid hook must be syscall.Setgid")
	}
	if reflect.ValueOf(privilegeHooks.setuid).Pointer() != reflect.ValueOf(syscall.Setuid).Pointer() {
		t.Error("the production setuid hook must be syscall.Setuid")
	}
}

// TestProductionChildExitHookIsTheRealOne: as in the rest of the package, the
// value production uses is asserted so a test cannot accidentally weaken it. The
// exec hook is asserted per platform (see prodhooks_unix_test.go), because what it
// has to be depends on the system.
func TestProductionChildExitHookIsTheRealOne(t *testing.T) {
	if reflect.ValueOf(childHooks.exit).Pointer() != reflect.ValueOf(os.Exit).Pointer() {
		t.Error("the production child exit hook must be os.Exit")
	}
}

// --- chroot and privileges with injected syscalls ---------------------------

// TestEnterChrootSuccessWithInjectedSyscalls walks the success path, which needs
// root: the real chroot would leave the test process inside a new root for ever.
func TestEnterChrootSuccessWithInjectedSyscalls(t *testing.T) {
	original := chrootHooks
	defer func() { chrootHooks = original }()

	var gotChroot, gotChdir string
	chrootHooks.chroot = func(path string) error {
		gotChroot = path
		return nil
	}
	chrootHooks.chdir = func(path string) error {
		gotChdir = path
		return nil
	}

	// With a directory, that directory is the one used.
	if err := enterChroot("/fake/root", "/fake/root/work"); err != nil {
		t.Fatalf("enterChroot = %v", err)
	}
	if gotChroot != "/fake/root" || gotChdir != "/fake/root/work" {
		t.Errorf("chroot(%q) chdir(%q)", gotChroot, gotChdir)
	}

	// With no directory, the root itself is used ("/"), because after the chroot
	// the path is interpreted inside the new root.
	gotChdir = ""
	if err := enterChroot("/fake/root", ""); err != nil {
		t.Fatalf("enterChroot = %v", err)
	}
	if gotChdir != "/" {
		t.Errorf("chdir = %q, \"/\" was expected when no directory is given", gotChdir)
	}
}

// TestEnterChrootChdirFailure: if the chdir after the chroot fails, the error
// must name the operation, because a failing chdir leaves the process in a
// different directory than the caller asked for.
func TestEnterChrootChdirFailure(t *testing.T) {
	original := chrootHooks
	defer func() { chrootHooks = original }()

	chrootHooks.chroot = func(string) error { return nil }
	chrootHooks.chdir = func(string) error { return syscall.ENOTDIR }

	err := enterChroot("/fake/root", "/fake/root/work")
	if err == nil {
		t.Fatal("a failing chdir must be an error")
	}
	if !strings.Contains(err.Error(), "chdir") || !strings.Contains(err.Error(), "/fake/root/work") {
		t.Errorf("the error must name the operation and the directory: %v", err)
	}
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("the original error must be preserved: %v", err)
	}
}

// TestDropPrivilegesSuccessWithInjectedSyscalls walks the success path of the
// three calls in their order: group, then supplementary groups, then user.
func TestDropPrivilegesSuccessWithInjectedSyscalls(t *testing.T) {
	original := privilegeHooks
	defer func() { privilegeHooks = original }()

	var order []string
	var groups []int
	var uid, gid int
	privilegeHooks.setgroups = func(g []int) error {
		order = append(order, "setgroups")
		groups = g
		return nil
	}
	privilegeHooks.setgid = func(g int) error {
		order = append(order, "setgid")
		gid = g
		return nil
	}
	privilegeHooks.setuid = func(u int) error {
		order = append(order, "setuid")
		uid = u
		return nil
	}

	if err := dropPrivileges(1234, 5678); err != nil {
		t.Fatalf("dropPrivileges = %v", err)
	}
	if uid != 1234 || gid != 5678 {
		t.Errorf("uid=%d gid=%d, 1234/5678 were requested", uid, gid)
	}
	// The supplementary groups are reduced to the target group.
	if len(groups) != 1 || groups[0] != 5678 {
		t.Errorf("groups = %v, [5678] was expected", groups)
	}
	if strings.Join(order, ",") != "setgroups,setgid,setuid" {
		t.Errorf("the order must be group, supplementary groups, user: %v", order)
	}
}

// TestDropPrivilegesReportsSetgidAndSetuidFailures: each call of the sequence has
// its own error, which is what tells the operator which privilege is missing.
func TestDropPrivilegesReportsSetgidAndSetuidFailures(t *testing.T) {
	original := privilegeHooks
	defer func() { privilegeHooks = original }()

	privilegeHooks.setgroups = func([]int) error { return nil }
	privilegeHooks.setgid = func(int) error { return nil }
	privilegeHooks.setuid = func(int) error { return nil }

	privilegeHooks.setgid = func(int) error { return syscall.EPERM }
	if err := dropPrivileges(1000, 1000); err == nil {
		t.Error("a failing setgid must be an error")
	} else if !strings.Contains(err.Error(), "setgid") || !errors.Is(err, syscall.EPERM) {
		t.Errorf("the error must name setgid and keep the cause: %v", err)
	} else {
		// the expected outcome: nothing more to check
	}
	// With setgid working it is setuid's turn.
	privilegeHooks.setgid = func(int) error { return nil }
	privilegeHooks.setuid = func(int) error { return syscall.EPERM }
	if err := dropPrivileges(1000, 1000); err == nil {
		t.Error("a failing setuid must be an error")
	} else if !strings.Contains(err.Error(), "setuid") || !errors.Is(err, syscall.EPERM) {
		t.Errorf("the error must name setuid and keep the cause: %v", err)
	} else {
		// the expected outcome: nothing more to check
	}
}

// --- killGroup --------------------------------------------------------------

// TestKillGroupWithAnImpossiblePid: if the group cannot be signalled (it no
// longer exists) the answer is the "already finished" error and not a panic, so
// that the deadline handling of launch can carry on.
func TestKillGroupWithAnImpossiblePid(t *testing.T) {
	cmd := exec.Command("/bin/true")
	// A pid that cannot exist in practice, used as a process group.
	cmd.Process = &os.Process{Pid: 1 << 30}

	err := killGroup(cmd)
	if !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("killGroup = %v, the process-done error was expected", err)
	}
}

// TestKillGroupOfAStartedGroup: the real syscall. With the child in a group of
// its own (what the sandbox uses), the negative pid signals the whole group.
func TestKillGroupOfAStartedGroup(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start the helper: %v", err)
	}
	if err := killGroup(cmd); err != nil {
		t.Errorf("killGroup = %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Error("the process was signalled and must not finish cleanly")
	}
}

// --- sandbox.New error paths ------------------------------------------------

// TestNewDefaultsTheWorkingDirectoryToTheCurrentOne: with no directory, the
// current one is used, and it is resolved to an absolute path (the commands need
// to be able to chdir into it).
func TestNewDefaultsTheWorkingDirectoryToTheCurrentOne(t *testing.T) {
	dir := t.TempDir()
	chdirInto(t, dir)

	s, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.base != resolved {
		t.Errorf("base = %q, the current directory (%q) was expected", s.base, resolved)
	}
}

// TestNewReportsAnUncreatableWorkingDirectory: with chroot, a directory that cannot
// be created stops New because every later path depends on it.
func TestNewReportsAnUncreatableWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := New(Options{Dir: filepath.Join(file, "below-a-file"), UseChroot: true, Root: "."})
	if err == nil {
		s.Close()
		t.Fatal("a working directory that cannot be created must be an error")
	}
	if !strings.Contains(err.Error(), "could not create the working directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNewReportsAnUnresolvableWorkingDirectory: with the current directory
// deleted, "." cannot be resolved and New must fail with a clear message instead
// of keeping a relative path the child could not chdir into.
func TestNewReportsAnUnresolvableWorkingDirectory(t *testing.T) {
	dir, err := os.MkdirTemp("", "sandbox-deleted-*")
	if err != nil {
		t.Fatal(err)
	}
	chdirInto(t, dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}

	s, err := New(Options{})
	if err == nil {
		s.Close()
		t.Fatal("an unresolvable working directory must be an error")
	}
	if !strings.Contains(err.Error(), "could not resolve the working directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNewReportsWhenTheExecutableCannotBeLocated: the sandbox runs through this
// very binary, so if it cannot be located the isolation is impossible.
func TestNewReportsWhenTheExecutableCannotBeLocated(t *testing.T) {
	original := osHooks.executable
	defer func() { osHooks.executable = original }()
	osHooks.executable = func() (string, error) { return "", errors.New("no executable today") }

	s, err := New(Options{Dir: t.TempDir()})
	if err == nil {
		s.Close()
		t.Fatal("without its own executable the sandbox cannot work")
	}
	if !strings.Contains(err.Error(), "could not locate this very executable") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNewWithChrootAsRootAndInaccessibleRoot: when the process is root but the
// requested root is not accessible, the chroot is not applied and it is recorded
// (the sandbox does not pretend to be isolated).
func TestNewWithChrootAsRootAndInaccessibleRoot(t *testing.T) {
	original := osHooks.euid
	defer func() { osHooks.euid = original }()
	osHooks.euid = func() int { return 0 }

	s, err := New(Options{
		Dir:       t.TempDir(),
		UseChroot: true,
		Root:      "/does/not/exist/this/root",
		Log:       logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.op.UseChroot {
		t.Error("an inaccessible root must switch the chroot off")
	}
	found := false
	for _, warning := range s.NotApplied() {
		if strings.Contains(warning, "is not accessible") {
			found = true
		}
	}
	if !found {
		t.Errorf("the inaccessible root must be recorded: %v", s.NotApplied())
	}
}

// TestNewWithChrootAsRootWithAnAccessibleRoot: the opposite case, which is what
// makes the chroot be really declared and used.
func TestNewWithChrootAsRootWithAnAccessibleRoot(t *testing.T) {
	original := osHooks.euid
	defer func() { osHooks.euid = original }()
	osHooks.euid = func() int { return 0 }

	root := t.TempDir()
	s, err := New(Options{
		Dir:       t.TempDir(),
		UseChroot: true,
		Root:      root,
		Log:       logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if !s.op.UseChroot {
		t.Fatalf("with root and an accessible root the chroot must be kept: %v", s.NotApplied())
	}
	if !hasMode(s.Isolation(), ModeChroot) {
		t.Errorf("the chroot must be declared as applied: %v", s.Isolation())
	}
}

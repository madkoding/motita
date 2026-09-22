//go:build linux

// The sandbox's isolation is Linux-specific (ulimit, cgroups v1, chroot, dropping
// privileges). These tests exercise that code, so they only build where it exists;
// the portable behaviour is covered by portable_test.go.

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
)

// TestNotAppliedAndIsolationJSON: what the sandbox reports must be consistent
// with what it applied, because that is what the operator sees when diagnosing.
//
// Note: if the launcher's memory peak exceeds what was requested, the memory
// adjustment warning is legitimate (it is the protection against "fatal error:
// runtime: cannot allocate memory"), so only consistency is checked here, not
// that the list is empty.
func TestNotAppliedAndIsolationJSON(t *testing.T) {
	s := newSandbox(t, Limits{MemoryMB: 128, CPUSeconds: 5})

	for _, warning := range s.NotApplied() {
		if warning == "" {
			t.Error("an empty warning contributes nothing")
		}
	}
	if s.op.Limits.MemoryMB < 128 {
		t.Errorf("the limit cannot end up below what was requested: %d", s.op.Limits.MemoryMB)
	}

	json := s.IsolationJSON()
	for _, part := range []string{"modes", "ephemeral", "posix_limits", "directory"} {
		if !strings.Contains(json, part) {
			t.Errorf("the JSON must include %q: %s", part, json)
		}
	}
}

// TestIsolationJSONWithWarnings: the isolation that was not applied shows up.
func TestIsolationJSONWithWarnings(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Options{
		Dir:       dir,
		UseChroot: true,
		Root:      "/root/that/does/not/exist",
		Log:       logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if len(s.NotApplied()) == 0 {
		t.Error("an impossible chroot must be recorded as not applied")
	}
	if !strings.Contains(s.IsolationJSON(), "not_applied") {
		t.Errorf("JSON = %s", s.IsolationJSON())
	}
}

// TestIsolationWithAllModes: Isolation summarises what is really active.
func TestIsolationWithAllModes(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Options{
		Dir:       dir,
		Limits:    Limits{MemoryMB: 64, NoNetwork: true},
		DropPrivs: true,
		Uid:       1000,
		Gid:       1000,
		Log:       logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	modes := s.Isolation()
	expected := map[Mode]bool{ModeEphemeral: false, ModeLimits: false, ModeUnprivileged: false, ModeNoNetwork: false}
	for _, m := range modes {
		if _, ok := expected[m]; ok {
			expected[m] = true
		}
	}
	for mode, seen := range expected {
		if !seen {
			t.Errorf("mode %q is missing from %v", mode, modes)
		}
	}
}

// TestIsolationWithoutLimits: without limits posix_limits is not declared.
func TestIsolationWithoutLimits(t *testing.T) {
	s := newSandbox(t, Limits{})
	modes := s.Isolation()
	if len(modes) != 1 || modes[0] != ModeEphemeral {
		t.Errorf("modes = %v, only the ephemeral one was expected", modes)
	}
}

// TestCloseWithoutCgroups: closing a sandbox without cgroups must not fail.
func TestCloseWithoutCgroups(t *testing.T) {
	s := newSandbox(t, Limits{MemoryMB: 64})
	if err := s.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
	// Closing twice must not fail either.
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

// TestKeepEphemeral: with Keep, the temporary directory stays.
func TestKeepEphemeral(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Options{Dir: dir, Keep: true, Log: logx.Global()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	output, _, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "echo $TMPDIR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(output)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("with Keep the temporary directory must exist: %v", err)
	}
}

// TestRunWithMaxOutputFromTheSandbox: the output limit is taken from the options
// when the request does not carry one.
func TestRunWithMaxOutputFromTheSandbox(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Options{Dir: dir, MaxOutputKB: 1, Log: logx.Global()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, truncated, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "i=0; while [ $i -lt 500 ]; do echo enough-filler-here; i=$((i+1)); done"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Error("it should be truncated with a 1 KB limit")
	}
}

// TestRunWithoutTimeoutUsesTheSandboxOne.
func TestRunWithoutTimeoutUsesTheSandboxOne(t *testing.T) {
	s := newSandbox(t, Limits{})
	s.op.Timeout = 2 * time.Second

	start := time.Now()
	_, _, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "sleep 30"},
	})
	if err == nil {
		t.Fatal("a timeout was expected")
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("the sandbox timeout was not applied: %s", time.Since(start))
	}
}

// TestRunDefaultTimeout: with no timeout in the options or in the request, the
// default value (300 s) is used without hanging.
func TestRunDefaultTimeout(t *testing.T) {
	s := newSandbox(t, Limits{})
	s.op.Timeout = 0
	_, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "exit 0"},
	})
	if err != nil || exit != 0 {
		t.Errorf("exit=%d err=%v", exit, err)
	}
}

// TestRunMissingDirectory: it is created, it does not fail.
func TestRunMissingDirectory(t *testing.T) {
	base := t.TempDir()
	s := newSandbox(t, Limits{})
	newDir := filepath.Join(base, "does", "not", "exist")

	_, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: newDir,
	})
	if err != nil || exit != 0 {
		t.Fatalf("exit=%d err=%v", exit, err)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Errorf("the working directory must be created: %v", err)
	}
}

// TestRunEmptyCommandInSandbox.
func TestRunEmptyCommandInSandbox(t *testing.T) {
	s := newSandbox(t, Limits{})
	_, _, exit, err := s.Run(context.Background(), execx.Request{Command: ""})
	if err == nil {
		t.Error("an empty command must give an error")
	}
	if exit != -1 {
		t.Errorf("exit = %d", exit)
	}
}

// --- cgroups ----------------------------------------------------------------

// TestCgroupsRootMissing: it is declared unavailable and it carries on, without
// pretending.
func TestCgroupsRootMissing(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Options{
		Dir:        dir,
		UseCgroups: true,
		CgroupRoot: "/path/that/does/not/exist",
		Limits:     Limits{MemoryMB: 64},
		Log:        logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.cg != nil {
		t.Error("there should be no cgroup with a non-existent root")
	}
	if len(s.NotApplied()) == 0 {
		t.Error("it must be recorded that the cgroups were not applied")
	}
	// And it still must be able to run.
	if _, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "exit 0"},
	}); err != nil || exit != 0 {
		t.Errorf("execution must keep working: exit=%d err=%v", exit, err)
	}
}

// TestNewCgroupOwnDirectories exercises the real cgroups v1 implementation over
// a fake tree: there is no need to be root or to touch the system, because what
// is tested is the logic (creating the directory, writing the limits, adding the
// process and cleaning up).
func TestNewCgroupOwnDirectories(t *testing.T) {
	root := t.TempDir()
	// The minimal cgroups v1 tree the implementation expects is built: the pids
	// controller is recognised by the existence of pids.max.
	for _, controller := range []string{"memory", "pids"} {
		if err := os.MkdirAll(filepath.Join(root, controller), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pids", "pids.max"), []byte("max"), 0o644); err != nil {
		t.Fatal(err)
	}

	cg, err := newCgroup(root, Limits{MemoryMB: 128, Processes: 64})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer cg.remove()

	// The memory limit must be written in bytes.
	data, err := os.ReadFile(filepath.Join(root, "memory", cgroupName, "memory.limit_in_bytes"))
	if err != nil {
		t.Fatalf("the memory limit was not written: %v", err)
	}
	if string(data) != fmt.Sprint(128<<20) {
		t.Errorf("limit = %q, expected %d", data, 128<<20)
	}

	// And the PIDs one.
	data, err = os.ReadFile(filepath.Join(root, "pids", cgroupName, "pids.max"))
	if err != nil {
		t.Fatalf("the PIDs limit was not written: %v", err)
	}
	if string(data) != "64" {
		t.Errorf("pids.max = %q", data)
	}

	// Adding the process writes the PID into tasks.
	if err := cg.addProcess(1234); err != nil {
		t.Fatalf("addProcess: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(root, "memory", cgroupName, "tasks"))
	if strings.TrimSpace(string(data)) != "1234" {
		t.Errorf("tasks = %q", data)
	}

	// Removing deletes the directories.
	if err := cg.remove(); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", cgroupName)); !os.IsNotExist(err) {
		t.Error("the cgroup should have been deleted")
	}
}

// TestNewCgroupWithoutMemoryController: without the memory controller there is
// no v1.
func TestNewCgroupWithoutMemoryController(t *testing.T) {
	root := t.TempDir()
	if _, err := newCgroup(root, Limits{MemoryMB: 64}); err == nil {
		t.Error("without the memory controller it must fail")
	}
}

// TestNewCgroupWithoutPidsController: a tree without the pids controller is
// still valid.
func TestNewCgroupWithoutPidsController(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "memory"), 0o755)

	cg, err := newCgroup(root, Limits{MemoryMB: 32, Processes: 10})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer cg.remove()
	if cg.pids != "" {
		t.Error("without the pids controller it must not try to use it")
	}
	if err := cg.addProcess(os.Getpid()); err != nil {
		t.Errorf("addProcess without pids must not fail: %v", err)
	}
}

// TestWriteLimitImpossiblePath.
func TestWriteLimitImpossiblePath(t *testing.T) {
	if err := writeLimit("/path/that/does/not/exist/limit", "1"); err == nil {
		t.Error("writing to a non-existent path must fail")
	}
}

// TestRemoveCgroupAlreadyRemoved: removing twice must not break.
func TestRemoveCgroupAlreadyRemoved(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "memory"), 0o755)
	cg, err := newCgroup(root, Limits{MemoryMB: 64})
	if err != nil {
		t.Fatal(err)
	}
	if err := cg.remove(); err != nil {
		t.Fatal(err)
	}
	// The second time the directories no longer exist and it must not be an
	// error.
	if err := cg.remove(); err != nil {
		t.Errorf("removing twice must not fail: %v", err)
	}
}

// --- child mode via subprocess ----------------------------------------------

// TestRunAsChildViaSubprocess walks the child mode paths for real, but in a
// subprocess: RunAsChild ends in syscall.Exec, which would replace the test
// process (and with it, the coverage profile).
//
// The subprocess is the test binary itself, whose TestMain attends the marker.
func TestRunAsChildViaSubprocess(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}
	dir := t.TempDir()

	cases := []struct {
		name  string
		spec  Spec
		fails bool
	}{
		{
			name: "runs a command with limits",
			spec: Spec{
				Command:     "/bin/sh",
				Args:        []string{"-c", "echo child-ok > " + filepath.Join(dir, "a.txt")},
				Dir:         dir,
				Limits:      Limits{MemoryMB: 256, CPUSeconds: 5, OpenFiles: 64},
				Environment: []string{"PATH=/usr/bin:/bin"},
			},
		},
		{
			name: "command by name (resolves PATH)",
			spec: Spec{
				Command:     "echo",
				Args:        []string{"resolved"},
				Dir:         dir,
				Environment: []string{"PATH=/usr/bin:/bin"},
			},
		},
		{
			name: "non-existent command in PATH",
			spec: Spec{
				Command:     "does-not-exist-anywhere",
				Dir:         dir,
				Environment: []string{"PATH=/usr/bin:/bin"},
			},
			fails: true,
		},
		{
			name: "non-existent working directory",
			spec: Spec{
				Command:     "echo",
				Dir:         "/does/not/exist/this/directory",
				Environment: []string{"PATH=/usr/bin:/bin"},
			},
			fails: true,
		},
		{
			name: "chroot without privileges",
			spec: Spec{
				Command:     "/bin/sh",
				Args:        []string{"-c", "exit 0"},
				Chroot:      "/path/that/does/not/exist",
				ChrootDir:   "/",
				Dir:         dir,
				Environment: []string{"PATH=/usr/bin:/bin"},
			},
			fails: true,
		},
		{
			name: "dropping privileges without root",
			spec: Spec{
				Command:        "/bin/sh",
				Args:           []string{"-c", "exit 0"},
				Dir:            dir,
				DropPrivileges: true,
				Uid:            os.Getuid(),
				Gid:            os.Getgid(),
				Environment:    []string{"PATH=/usr/bin:/bin"},
			},
			fails: os.Geteuid() != 0,
		},
		{
			name:  "spec without command",
			spec:  Spec{Dir: dir},
			fails: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := tc.spec.Encode()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, ChildMarker, encoded)
			cmd.Env = append(os.Environ(), "STARLIGHT_TEST_CHILD=1")
			output, err := cmd.CombinedOutput()

			if tc.fails {
				if err == nil {
					t.Fatalf("a failure was expected; output = %q", output)
				}
				return
			}
			if err != nil {
				t.Fatalf("it should not fail: %v (%s)", err, output)
			}
		})
	}
}

// TestRunAsChildInsufficientArgs: the marker without a spec.
func TestRunAsChildInsufficientArgs(t *testing.T) {
	if err := RunAsChild([]string{ChildMarker}); err == nil {
		t.Error("without a spec it must give an error")
	}
	if err := RunAsChild(nil); err == nil {
		t.Error("without arguments it must give an error")
	}
}

// TestEncodeCorrect: the encoded spec carries the command and the limits.
func TestEncodeCorrect(t *testing.T) {
	spec := Spec{Command: "/bin/true", Args: []string{"a"}, Limits: Limits{MemoryMB: 1}}
	encoded, err := spec.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "/bin/true") || !strings.Contains(encoded, "MemoryMB") {
		t.Errorf("encoded = %s", encoded)
	}
}

// --- chroot and privileges --------------------------------------------------

// TestEnterChrootWithoutRoot: with no root it does nothing (it is the path
// without chroot).
func TestEnterChrootWithoutRoot(t *testing.T) {
	if err := enterChroot("", ""); err != nil {
		t.Errorf("with no root it must do nothing: %v", err)
	}
}

// TestEnterChrootMissing: an impossible chroot must give an explicit error (and
// not leave the process half-way).
func TestEnterChrootMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the error would be a different one")
	}
	err := enterChroot("/path/that/does/not/exist", "/")
	if err == nil {
		t.Error("a non-existent chroot must fail")
	}
	if !strings.Contains(err.Error(), "chroot") {
		t.Errorf("the error must identify the operation: %v", err)
	}
}

// TestDropPrivilegesWithoutRoot: without privileges it must give a clear error
// instead of pretending it was applied.
func TestDropPrivilegesWithoutRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the operation would work")
	}
	err := dropPrivileges(os.Getuid(), os.Getgid())
	if err == nil {
		t.Error("without root, dropping privileges must fail")
	}
}

// TestChildAttributes: the child's attributes reflect the limits and the
// privilege drop.
func TestChildAttributes(t *testing.T) {
	attr, warnings := childAttributes(Limits{MemoryMB: 64, NoNetwork: true}, true, 1000, 1001)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
	if attr == nil {
		t.Fatal("the attributes must not be nil")
	}
	if !attr.Setpgid {
		t.Error("it must use a process group of its own")
	}
	if attr.Credential == nil || attr.Credential.Uid != 1000 || attr.Credential.Gid != 1001 {
		t.Errorf("credentials = %+v", attr.Credential)
	}

	// Without network and without privileges.
	attr, _ = childAttributes(Limits{MemoryMB: 64}, false, 0, 0)
	if attr.Credential != nil {
		t.Error("without DropPrivs there must be no credentials")
	}
}

// TestHasLimits.
func TestHasLimits(t *testing.T) {
	cases := []struct {
		l      Limits
		expect bool
	}{
		{Limits{}, false},
		{Limits{MemoryMB: 1}, true},
		{Limits{CPUSeconds: 1}, true},
		{Limits{Processes: 1}, true},
		{Limits{OpenFiles: 1}, true},
		{Limits{MaxFileSizeMB: 1}, true},
		{Limits{NoNetwork: true}, false}, // NoNetwork is not a ulimit
	}
	for _, tc := range cases {
		if got := hasLimits(tc.l); got != tc.expect {
			t.Errorf("hasLimits(%+v) = %v", tc.l, got)
		}
	}
}

// TestMinimumMemoryMB: the minimum depends on the process's real peak and is
// greater than zero on Linux.
func TestMinimumMemoryMB(t *testing.T) {
	minimum := minimumMemoryMB()
	if minimum <= 0 {
		t.Skip("could not read /proc/self/status in this environment")
	}
	if peak := peakVirtualMemory(); peak > 0 && uint64(minimum)<<20 < peak {
		t.Errorf("the minimum (%d MB) cannot be smaller than the peak (%d bytes)", minimum, peak)
	}
}

// TestPeakVirtualMemory: it is read from the system and it is consistent.
func TestPeakVirtualMemory(t *testing.T) {
	peak := peakVirtualMemory()
	if peak == 0 {
		t.Skip("no /proc/self/status in this environment")
	}
	if peak < 1024*1024 {
		t.Errorf("a peak of %d bytes is implausible for a Go binary", peak)
	}
}

// TestKillGroupNoProcess: killing the group of a command that never started
// (Process == nil) must return an error, not panic.
func TestKillGroupNoProcess(t *testing.T) {
	cmd := exec.Command("/bin/true")
	if err := killGroup(cmd); err == nil {
		t.Error("with no process started it must return an error")
	}
}

// --- Child mode with injected hooks -----------------------------------------

// TestRunAsChildWithInjectedExec walks the whole child mode without replacing the
// test process: the exec and the reserved exit are hooks. It is the only way to
// assert which command and environment the child would have used.
func TestRunAsChildWithInjectedExec(t *testing.T) {
	original := childHooks
	defer func() { childHooks = original }()

	var (
		gotArgv0 string
		gotArgv  []string
		gotEnv   []string
		exits    []int
	)
	childHooks.exec = func(argv0 string, argv []string, envv []string) error {
		gotArgv0, gotArgv, gotEnv = argv0, argv, envv
		return nil
	}
	childHooks.exit = func(code int) { exits = append(exits, code) }

	dir := t.TempDir()
	spec := Spec{
		Command:     "/bin/sh",
		Args:        []string{"-c", "echo child"},
		Dir:         dir,
		Environment: []string{"PATH=/usr/bin:/bin", "CUSTOM=1"},
	}
	encoded, err := spec.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := RunAsChild([]string{ChildMarker, encoded}); err != nil {
		t.Fatalf("the child mode failed: %v", err)
	}

	// The stub exec returns instead of replacing the image, which is what the
	// non-Unix implementation does: the child then forwards the code. On Unix the
	// real exec never returns, so this line is unreachable there — what must never
	// happen on the happy path is the reserved 127.
	for _, code := range exits {
		if code == 127 {
			t.Errorf("the reserved code 127 means the command could not be executed: %v", exits)
		}
	}
	if gotArgv0 == "" || len(gotArgv) == 0 {
		t.Fatalf("the exec was not reached: argv0=%q argv=%v", gotArgv0, gotArgv)
	}
	joined := fmt.Sprint(gotEnv)
	if !strings.Contains(joined, "CUSTOM=1") {
		t.Errorf("the spec's environment must be passed through: %v", gotEnv)
	}
}

// TestRunAsChildResolvesRelativeCommands: a command written by name (as in the
// YAML) must be resolved against PATH inside the child, because syscall.Exec does
// not search it.
func TestRunAsChildResolvesRelativeCommands(t *testing.T) {
	original := childHooks
	defer func() { childHooks = original }()

	var gotArgv0 string
	childHooks.exec = func(argv0 string, argv []string, envv []string) error {
		gotArgv0 = argv0
		return nil
	}
	childHooks.exit = func(int) {}

	spec := Spec{Command: "sh", Args: []string{"-c", "true"}, Dir: t.TempDir(), Environment: []string{"PATH=/usr/bin:/bin"}}
	encoded, _ := spec.Encode()
	if err := RunAsChild([]string{ChildMarker, encoded}); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(gotArgv0) {
		t.Errorf("the command must be resolved to an absolute path: %q", gotArgv0)
	}
}

// TestRunAsChildUsesTheProcessEnvironmentWhenNoneIsGiven: with no environment in
// the spec, the child inherits its own (that is what a direct call means).
func TestRunAsChildUsesTheProcessEnvironmentWhenNoneIsGiven(t *testing.T) {
	original := childHooks
	defer func() { childHooks = original }()

	var gotEnv []string
	childHooks.exec = func(argv0 string, argv []string, envv []string) error {
		gotEnv = envv
		return nil
	}
	childHooks.exit = func(int) {}

	spec := Spec{Command: "/bin/true", Dir: t.TempDir()}
	encoded, _ := spec.Encode()
	if err := RunAsChild([]string{ChildMarker, encoded}); err != nil {
		t.Fatal(err)
	}
	if len(gotEnv) == 0 {
		t.Error("with no environment in the spec the process environment must be used")
	}
}

// TestRunAsChildReportsAnUnresolvableCommand: a command that is not in PATH must
// end with the reserved code and return an error instead of exec'ing nothing.
func TestRunAsChildReportsAnUnresolvableCommand(t *testing.T) {
	original := childHooks
	defer func() { childHooks = original }()

	var exits []int
	childHooks.exec = func(argv0 string, argv []string, envv []string) error {
		t.Error("the exec must not be reached with an unresolvable command")
		return nil
	}
	childHooks.exit = func(code int) { exits = append(exits, code) }

	spec := Spec{Command: "a-command-that-does-not-exist-anywhere", Dir: t.TempDir(), Environment: []string{"PATH=/usr/bin:/bin"}}
	encoded, _ := spec.Encode()
	err := RunAsChild([]string{ChildMarker, encoded})
	if err == nil {
		t.Error("an unresolvable command must be an error")
	}
	if len(exits) != 1 || exits[0] != 127 {
		t.Errorf("the reserved code 127 was expected, got %v", exits)
	}
}

// TestRunAsChildReportsAFailedExec: if the exec itself fails the reserved code is
// used, so the parent can tell it apart from a real failure of the command.
func TestRunAsChildReportsAFailedExec(t *testing.T) {
	original := childHooks
	defer func() { childHooks = original }()

	var exits []int
	childHooks.exec = func(argv0 string, argv []string, envv []string) error {
		return errors.New("exec refused")
	}
	childHooks.exit = func(code int) { exits = append(exits, code) }

	spec := Spec{Command: "/bin/true", Dir: t.TempDir(), Environment: []string{"PATH=/usr/bin:/bin"}}
	encoded, _ := spec.Encode()
	err := RunAsChild([]string{ChildMarker, encoded})
	if err == nil {
		t.Error("a failed exec must be an error")
	}
	if len(exits) != 1 || exits[0] != 127 {
		t.Errorf("the reserved code 127 was expected, got %v", exits)
	}
}

// TestRunAsChildRejectsMissingWorkingDirectory: a working directory that does not
// exist must be reported, not silently ignored.
func TestRunAsChildRejectsMissingWorkingDirectory(t *testing.T) {
	original := childHooks
	defer func() { childHooks = original }()
	childHooks.exec = func(string, []string, []string) error { return nil }
	childHooks.exit = func(int) {}

	spec := Spec{Command: "/bin/true", Dir: "/does/not/exist/this/directory"}
	encoded, _ := spec.Encode()
	if err := RunAsChild([]string{ChildMarker, encoded}); err == nil {
		t.Error("a missing working directory must be an error")
	}
}

// TestRunAsChildRejectsAnUnreadableSpec: a spec that is not JSON must be refused
// before anything is executed.
func TestRunAsChildRejectsAnUnreadableSpec(t *testing.T) {
	if err := RunAsChild([]string{ChildMarker, "not json at all"}); err == nil {
		t.Error("an unreadable spec must be refused")
	}
}

// TestRunAsChildRejectsASpecWithoutACommand: with nothing to run there is nothing
// to do.
func TestRunAsChildRejectsASpecWithoutACommand(t *testing.T) {
	encoded, _ := Spec{Dir: t.TempDir()}.Encode()
	if err := RunAsChild([]string{ChildMarker, encoded}); err == nil {
		t.Error("a spec with no command must be refused")
	}
}

// TestEncodeRejectsUnserialisableSpec: a spec that cannot be serialised must give
// an error instead of an empty string that the child could not read.
//
// The data this package serialises cannot make encoding/json fail, so the
// failure is injected through the jsonMarshal seam (in production it always holds
// json.Marshal).
func TestEncodeRejectsUnserialisableSpec(t *testing.T) {
	original := jsonMarshal
	defer func() { jsonMarshal = original }()
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("no serialiser today") }

	encoded, err := Spec{Command: "/bin/true"}.Encode()
	if err == nil {
		t.Fatalf("a failed marshalling must be an error (encoded = %q)", encoded)
	}
	if !strings.Contains(err.Error(), "could not serialise the sandbox spec") {
		t.Errorf("the error must name the operation: %v", err)
	}
	if encoded != "" {
		t.Errorf("encoded = %q, an empty string was expected", encoded)
	}
}

// --- Platform primitives: injectable filesystem and syscalls ---------------

// TestPeakVirtualMemoryReadsNothingWhenTheFileIsMissing: on a system without
// /proc the peak cannot be read and the answer must be zero (which the caller
// interprets as "no adjustment needed").
func TestPeakVirtualMemoryReadsNothingWhenTheFileIsMissing(t *testing.T) {
	original := procStatusPath
	defer func() { procStatusPath = original }()

	procStatusPath = "/does/not/exist/status"
	if got := peakVirtualMemory(); got != 0 {
		t.Errorf("with no /proc the peak must be 0, got %d", got)
	}
}

// TestPeakVirtualMemoryHandlesGarbage: a status file without the VmPeak line, or
// with an unreadable value, must yield 0 instead of a wrong number.
func TestPeakVirtualMemoryHandlesGarbage(t *testing.T) {
	original := procStatusPath
	defer func() { procStatusPath = original }()

	dir := t.TempDir()

	procStatusPath = filepath.Join(dir, "no-peak")
	os.WriteFile(procStatusPath, []byte("Name:\tstarlight\nVmSize:\t  1024 kB\n"), 0o644)
	if got := peakVirtualMemory(); got != 0 {
		t.Errorf("without VmPeak it must be 0, got %d", got)
	}

	procStatusPath = filepath.Join(dir, "short-line")
	os.WriteFile(procStatusPath, []byte("VmPeak:\n"), 0o644)
	if got := peakVirtualMemory(); got != 0 {
		t.Errorf("with a truncated line it must be 0, got %d", got)
	}

	procStatusPath = filepath.Join(dir, "not-a-number")
	os.WriteFile(procStatusPath, []byte("VmPeak:\t  many kB\n"), 0o644)
	if got := peakVirtualMemory(); got != 0 {
		t.Errorf("with an unreadable value it must be 0, got %d", got)
	}
}

// TestMinimumMemoryMBWithoutProc: with no way to measure the peak, the adjustment
// must be skipped (0) instead of inventing a number.
func TestMinimumMemoryMBWithoutProc(t *testing.T) {
	original := procStatusPath
	defer func() { procStatusPath = original }()

	procStatusPath = "/does/not/exist/status"
	if got := minimumMemoryMB(); got != 0 {
		t.Errorf("without a measurable peak it must be 0, got %d", got)
	}
}

// TestDropPrivilegesReportsEachFailure: when a syscall of the sequence fails the
// error must name the operation, because that is what tells the operator which
// privilege is missing.
func TestDropPrivilegesReportsEachFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the sequence succeeds")
	}
	err := dropPrivileges(os.Getuid(), os.Getgid())
	if err == nil {
		t.Fatal("without root, dropping privileges must fail")
	}
	// setgroups is the first call, so it is the one that fails here.
	if !strings.Contains(err.Error(), "setgroups") {
		t.Errorf("the error must name the failing operation: %v", err)
	}
}

// TestEnterChrootWithOnlyRoot: enterChroot with no root does nothing (the path
// used when chroot is not requested).
func TestEnterChrootWithOnlyRoot(t *testing.T) {
	if err := enterChroot("", ""); err != nil {
		t.Errorf("with no root it must be a no-op: %v", err)
	}
}

// TestEnterChrootMissingRootIsReported: a root that does not exist must fail with
// the operation named.
func TestEnterChrootMissingRootIsReported(t *testing.T) {
	err := enterChroot("/does/not/exist/root", "/")
	if err == nil {
		t.Fatal("a missing root must fail")
	}
	if !strings.Contains(err.Error(), "chroot") {
		t.Errorf("the error must name the operation: %v", err)
	}
}

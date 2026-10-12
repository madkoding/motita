//go:build linux

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/logx"
)

// --- sandbox.Run error paths ------------------------------------------------

// TestRunReportsAnUnpreparableWorkingDirectory: a working directory that cannot
// be created stops the run before anything is executed.
func TestRunReportsAnUnpreparableWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := runnableSandbox(t, dir)

	// The directory exists, but its child is a file, so it cannot be used.
	_, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/true",
		Dir:     filepath.Join(file, "below-a-file"),
	})
	if err == nil {
		t.Fatal("a working directory that cannot be prepared must be an error")
	}
	if !strings.Contains(err.Error(), "could not prepare the working directory") {
		t.Errorf("unexpected error: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected (nothing was run)", exit)
	}
}

// TestRunReportsAnUncreatableTemporaryDirectory: the ephemeral TMPDIR is part of
// the isolation; without it the run does not start.
func TestRunReportsAnUncreatableTemporaryDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The base of the sandbox is a path below a file: the requested working
	// directory is fine, so MkdirAll passes and MkdirTemp fails.
	s := runnableSandbox(t, filepath.Join(file, "below-a-file"))

	_, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/true",
		Dir:     dir,
	})
	if err == nil {
		t.Fatal("without a temporary directory the run must not start")
	}
	if !strings.Contains(err.Error(), "could not create the temporary directory") {
		t.Errorf("unexpected error: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected", exit)
	}
}

// TestRunWarnsWhenTheTemporaryDirectoryCannotBeDeleted: a failure to clean up is
// not a failure of the run (the command's result matters), but it is logged
// because it leaves garbage behind.
func TestRunWarnsWhenTheTemporaryDirectoryCannotBeDeleted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the temporary directory can always be deleted")
	}
	base := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "sandbox.log")
	logger, err := logx.New(logx.Options{Path: logFile, Level: logx.Debug, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	s := runnableSandbox(t, base)
	s.log = logger

	// The command makes its own TMPDIR undeletable: RemoveAll will fail at the
	// deferred clean-up.
	output, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", `touch "$TMPDIR/leftover"; chmod 500 "$TMPDIR"; echo done`},
	})
	if err != nil {
		t.Fatalf("the command ran: %v (output %q)", err, output)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, output = %q", exit, output)
	}

	// Let the test's own clean-up delete the directory it could not delete.
	leftovers, err := filepath.Glob(filepath.Join(base, "tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range leftovers {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if len(leftovers) == 0 {
		t.Error("the undeletable temporary directory must have been left behind")
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "could not delete the temporary directory") {
		t.Errorf("the failure to clean up must be logged: %s", data)
	}
}

// TestRunWithChrootChecksTheCommandInsideTheRoot: in the chroot branch the
// command is looked up inside the new root before anything is executed, and the
// path used is the real one inside the root.
func TestRunWithChrootChecksTheCommandInsideTheRoot(t *testing.T) {
	original := osHooks.euid
	defer func() { osHooks.euid = original }()
	osHooks.euid = func() int { return 0 }

	root := t.TempDir()
	base := t.TempDir()
	s, err := New(Options{
		Dir:       base,
		UseChroot: true,
		Root:      root,
		Log:       logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !s.op.UseChroot {
		t.Fatalf("the chroot must be active for this test: %v", s.NotApplied())
	}

	// The command does not exist inside the root: it must be refused and the
	// error must name the path inside the chroot.
	_, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh"})
	if err == nil {
		t.Fatal("a command that is not inside the chroot must be refused")
	}
	if !strings.Contains(err.Error(), "does not exist inside the chroot") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(root, "bin/sh")) {
		t.Errorf("the error must name the path inside the root: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected", exit)
	}

	// With the command present inside the root, the run goes on to the child
	// (which, without root, refuses to claim it applied the chroot: it is not
	// the sandbox's job to pretend).
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", filepath.Join(root, "bin", "sh")); err != nil {
		t.Fatal(err)
	}
	output, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", "true"}})
	if os.Geteuid() != 0 {
		// The child cannot chroot: it says so and stops.
		if err == nil && exit == 0 {
			t.Fatalf("without root the chroot cannot be applied and yet it was reported as a success: %q", output)
		}
		if !strings.Contains(output, "chroot") {
			t.Errorf("the child must explain that the chroot failed: %q", output)
		}
		return
	}
	if err != nil {
		t.Fatalf("as root the chroot must work: %v (%q)", err, output)
	}
}

// TestRunReportsASpecThatCannotBeSerialised: the spec travels to the child as
// JSON; if it cannot be serialised there is nothing to pass on.
func TestRunReportsASpecThatCannotBeSerialised(t *testing.T) {
	original := jsonMarshal
	defer func() { jsonMarshal = original }()
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("no serialiser today") }

	s := runnableSandbox(t, t.TempDir())
	_, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/true"})
	if err == nil {
		t.Fatal("a spec that cannot be serialised must stop the run")
	}
	if !strings.Contains(err.Error(), "could not serialise the sandbox spec") {
		t.Errorf("unexpected error: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected", exit)
	}
}

// TestRunReportsAnIsolationThatCannotStart: if the isolation process cannot be
// started at all (the executable is gone), the error must say that it was the
// isolation and not the command.
func TestRunReportsAnIsolationThatCannotStart(t *testing.T) {
	s := runnableSandbox(t, t.TempDir())
	s.executable = "/does/not/exist/motita"

	_, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/true"})
	if err == nil {
		t.Fatal("an isolation that cannot be started must be an error")
	}
	if !strings.Contains(err.Error(), "could not start the isolation") {
		t.Errorf("unexpected error: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected", exit)
	}
}

// TestIsolationJSONRejectsASerialisationFailure: the JSON is for the logs, so if
// it cannot be built the answer is a fixed line, never a panic nor an empty
// string.
func TestIsolationJSONRejectsASerialisationFailure(t *testing.T) {
	original := jsonMarshal
	defer func() { jsonMarshal = original }()
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("no serialiser today") }

	s := runnableSandbox(t, t.TempDir())
	got := s.IsolationJSON()
	if !strings.Contains(got, "not serialisable") {
		t.Errorf("IsolationJSON = %q", got)
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("even the fallback must be valid JSON: %q", got)
	}
}

// --- helpers ----------------------------------------------------------------

// runnableSandbox builds a sandbox without going through New (some error paths
// need a state New would refuse) but with the fields Run uses.
func runnableSandbox(t *testing.T, base string) *Sandbox {
	t.Helper()
	return &Sandbox{
		op:         Options{},
		log:        logx.Global(),
		base:       base,
		executable: os.Args[0],
	}
}

// hasMode reports whether the isolation description includes the mode.
func hasMode(modes []Mode, mode Mode) bool {
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}

// chdirInto moves the test process into dir and restores the previous directory
// when the test finishes.
//
// The restore is best effort: other tests of this package move the process into
// temporary directories (the child mode does a real chdir) and those directories
// are deleted when their test ends, so there may be no directory to go back to.
func chdirInto(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		previous = ""
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("could not change to %q: %v", dir, err)
	}
	t.Cleanup(func() {
		if previous == "" {
			return
		}
		if err := os.Chdir(previous); err != nil {
			t.Logf("could not restore the working directory %q: %v", previous, err)
		}
	})
}

// TestTheChildNeverReceivesARelativeWorkingDirectory: the child re-executes this
// binary and chdirs to the directory carried in the spec. A relative value would be
// resolved a second time, inside a process that starts somewhere else, so the
// command would run outside the directory the caller meant and the anchor could not
// see the work it has to validate.
//
// Found on a real 32-bit machine: `workspace_dir: ./workspace` made the anchor fail
// every check, while the identical configuration with an absolute path passed. The
// property is asserted by observing the spec, so the test does not have to change
// the process working directory (which would break the rest of the package).
func TestTheChildNeverReceivesARelativeWorkingDirectory(t *testing.T) {
	cases := []struct {
		name string
		dir  string
	}{
		{"a relative directory", "workspace"},
		{"a nested relative directory", filepath.Join("a", "b")},
		{"a dot", "."},
		{"a relative parent", ".."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			var spec Spec
			saved := launchCommand
			defer func() { launchCommand = saved }()
			launchCommand = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
				if len(args) >= 2 {
					_ = json.Unmarshal([]byte(args[1]), &spec)
				}
				return exec.CommandContext(ctx, "true")
			}

			// The sandbox is built with an absolute base (as it is in production),
			// and the request carries the directory the caller spelled.
			box, err := New(Options{Dir: base})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer box.Close()

			// "." and ".." must be resolved against the sandbox's base, not the
			// process, so the request carries the directory exactly as spelled.
			r := execx.Request{Command: "true", Dir: tc.dir}
			// Run may fail for a directory that cannot be created; what matters is
			// that whatever reached the child was absolute.
			_, _, _, _ = box.Run(context.Background(), r)

			if spec.Dir == "" {
				t.Skip("the sandbox did not reach the launch step")
			}
			if !filepath.IsAbs(spec.Dir) {
				t.Errorf("the child received the relative path %q: it would be resolved twice", spec.Dir)
			}
		})
	}
}

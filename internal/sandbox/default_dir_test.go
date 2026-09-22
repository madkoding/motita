package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
)

// TestNewDefaultsTheChrootDirectoryToTheWorkingDirectory: with a chroot and no
// directory configured, the sandbox uses "." and creates it. Without the default
// the chroot would have nothing to enter, and the isolation would be silently
// absent — which is the opposite of what asking for it means.
func TestNewDefaultsTheChrootDirectoryToTheWorkingDirectory(t *testing.T) {
	// The directory is created here instead of with t.TempDir(): another test in
	// this package changes the working directory, and t.TempDir removes its own
	// directory at the end, which would leave the process sitting in a path that no
	// longer exists. The same reason is why the previous directory is read through
	// a fallback rather than assumed.
	dir, err := os.MkdirTemp("", "sandbox-default-dir-")
	if err != nil {
		t.Fatalf("could not create the directory: %v", err)
	}
	defer os.RemoveAll(dir)

	previous, err := os.Getwd()
	if err != nil {
		previous = os.TempDir()
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("could not enter %s: %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("could not go back to %s: %v", previous, err)
		}
	}()

	// No Dir: the sandbox has to supply ".".
	box, err := New(Options{UseChroot: true, Root: ".", Log: logx.Global()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	// Whether the chroot stays applied depends on the process: a non-root run
	// cannot chroot, and the sandbox records that as "requested but not applied"
	// instead of pretending. What this test pins is the default directory, which is
	// resolved before that decision is made.
	if box.op.Dir != "." {
		t.Errorf("Dir = %q, want the default %q", box.op.Dir, ".")
	}
	// The directory must exist on disk, otherwise the chroot cannot enter it.
	if info, err := os.Stat(filepath.Join(dir)); err != nil || !info.IsDir() {
		t.Errorf("the default directory must exist: %v", err)
	}
}

// TestRunReportsAWorkingDirectoryThatCannotBeResolved: resolving a relative path
// needs the current directory to be readable, and it may not be — this very test
// suite deletes the directory it is standing in, which is how the branch was found.
// The failure is reported instead of running the command somewhere unintended.
func TestRunReportsAWorkingDirectoryThatCannotBeResolved(t *testing.T) {
	dir, err := os.MkdirTemp("", "sandbox-vanished-")
	if err != nil {
		t.Fatalf("could not create the directory: %v", err)
	}
	safe, err := os.Getwd()
	if err != nil {
		safe = os.TempDir()
	}

	if err := os.Chdir(dir); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("could not enter %s: %v", dir, err)
	}
	// The process is now standing in a directory that no longer exists, which is
	// what makes filepath.Abs fail for a relative path.
	if err := os.RemoveAll(dir); err != nil {
		os.Chdir(safe)
		t.Fatalf("could not remove the directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(safe); err != nil {
			t.Errorf("could not go back to %s: %v", safe, err)
		}
	}()

	box, err := New(Options{Dir: safe, Log: logx.Global()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	// "." is the shortest relative path that stands for the vanished directory.
	_, _, _, err = box.Run(context.Background(), execx.Request{Command: "echo", Args: []string{"x"}, Dir: "."})

	// Linux cannot resolve a removed working directory; darwin still resolves it
	// to its old path. Either the failure is reported with its cause, or the
	// directory is recreated by that absolute path and the command runs there.
	if _, absErr := filepath.Abs("."); absErr == nil {
		defer os.RemoveAll(dir)
		if err != nil {
			t.Fatalf("a resolvable directory must be recreated and used: %v", err)
		}
		if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
			t.Errorf("the directory must be recreated by its absolute path: %v", statErr)
		}
		return
	}
	if err == nil {
		t.Fatal("an unresolvable working directory must be reported")
	}
	if !strings.Contains(err.Error(), "could not resolve the working directory") {
		t.Errorf("the error must explain the cause, got %v", err)
	}
}

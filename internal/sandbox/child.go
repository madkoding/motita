package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// Isolation child process.
//
// Go cannot apply setrlimit to a child from SysProcAttr, and depending on the
// `prlimit` utility would tie the agent to util-linux being installed on the
// i386 machine (it often is not). The dependency-free solution is to re-execute
// this very binary with a hidden mode that:
//
//	1. applies the setrlimits,
//	2. enters the chroot if requested,
//	3. drops privileges if requested,
//	4. and syscall.Execs the real command, leaving no Go process alive in
//	   between (nothing consuming memory from the limited budget).
// ---------------------------------------------------------------------------

// ChildMarker is the first argument that enables the isolation child mode.
const ChildMarker = "__sandbox_exec"

// Spec describes what the child process must do.
type Spec struct {
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	Dir            string   `json:"dir"`
	Limits         Limits   `json:"limits"`
	Chroot         string   `json:"chroot,omitempty"`
	ChrootDir      string   `json:"chroot_dir,omitempty"`
	DropPrivileges bool     `json:"drop_privileges,omitempty"`
	Uid            int      `json:"uid,omitempty"`
	Gid            int      `json:"gid,omitempty"`
	Environment    []string `json:"environment,omitempty"`
}

// jsonMarshal is the serialiser used for the sandbox spec and for the isolation
// JSON. It is a variable only because the data this package serialises cannot
// make encoding/json fail, so a test cannot reach the error path otherwise; the
// production call is always the stdlib marshaller.
var jsonMarshal = json.Marshal

// Encode serialises the spec to pass it on the command line.
func (s Spec) Encode() (string, error) {
	data, err := jsonMarshal(s)
	if err != nil {
		return "", fmt.Errorf("could not serialise the sandbox spec: %w", err)
	}
	return string(data), nil
}

// IsChildExecution reports whether the program was invoked in child mode.
func IsChildExecution(args []string) bool {
	return len(args) > 1 && args[0] == ChildMarker
}

// childHooks are the two operations that end the process (the reserved exit and
// the exec itself). They are replaceable so every decision of the child mode can
// be tested without the test process disappearing: RunAsChild normally replaces
// the current image and never returns, which is impossible to assert from the
// inside.
var childHooks = struct {
	exec func(argv0 string, argv []string, envv []string) error
	exit func(code int)
}{
	// execCommand is per platform: on Unix it replaces the image with
	// syscall.Exec; elsewhere that call only ever returns "not supported", so the
	// command runs as a child and its exit code is forwarded (see exec_other.go).
	exec: execCommand,
	exit: os.Exit,
}

// RunAsChild is the entry point of the child mode. It never returns if all goes
// well: it replaces the current process with the requested command.
func RunAsChild(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%s requires the JSON spec as an argument", ChildMarker)
	}
	var spec Spec
	if err := json.Unmarshal([]byte(args[1]), &spec); err != nil {
		return fmt.Errorf("unreadable sandbox spec: %w", err)
	}
	if spec.Command == "" {
		return fmt.Errorf("the sandbox spec carries no command")
	}

	// IMPORTANT ORDER: all the Go runtime work (chdir, chroot, dropping
	// privileges, resolving the command's path) happens BEFORE applying the
	// setrlimits, and the exec happens immediately after. Applying RLIMIT_AS
	// earlier makes the runtime itself die with "fatal error: runtime: cannot
	// allocate memory" as soon as it needs to reserve anything, because the
	// limit also counts the virtual memory the Go runtime maps (a reservation
	// that depends on the number of cores). This really happened: it worked on
	// one machine and failed on a runner with more cores.

	// chroot before the final chdir: after the chroot the path is interpreted
	// inside the new root.
	if spec.Chroot != "" {
		// enterChroot already does the chdir inside the new root.
		if err := enterChroot(spec.Chroot, spec.ChrootDir); err != nil {
			return err
		}
	} else if spec.Dir != "" {
		if err := os.Chdir(spec.Dir); err != nil {
			return fmt.Errorf("chdir(%q): %w", spec.Dir, err)
		}
	}

	if spec.DropPrivileges {
		// Privileges are dropped last: chroot needs root.
		if err := dropPrivileges(spec.Uid, spec.Gid); err != nil {
			return err
		}
	}

	env := spec.Environment
	if env == nil {
		env = os.Environ()
	}

	// syscall.Exec does NOT search PATH: it needs an absolute path. A
	// `command: make` or `command: go` written in the YAML would fail with
	// ENOENT if it were not resolved here. It is resolved inside the child so
	// that the sandbox's restricted PATH is used (the same one the command will
	// see), not the agent's.
	path := spec.Command
	if !filepath.IsAbs(path) {
		resolved, err := exec.LookPath(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "starlight: the command %q was not found in the sandbox PATH (%s): %v\n",
				path, os.Getenv("PATH"), err)
			childHooks.exit(127)
			return err
		}
		path = resolved
	}

	// The limits are NOT applied by this process: the shell applies them, and
	// the shell is a very small C binary. See limits_linux.go for the
	// measurements that led to this decision (a Go binary cannot apply
	// RLIMIT_AS and stay alive).
	finalCommand, finalArgs := wrapWithUlimit(spec.Limits, path, spec.Args)

	// The hook never returns when the command runs: on Unix it replaces the image,
	// and where that is impossible the implementation runs the command and exits
	// with its code. Only the failure comes back.
	if err := childHooks.exec(finalCommand, finalArgs, env); err != nil {
		// The failure is reported with the reserved code 127 so that the parent
		// can tell it apart from a real failure of the command.
		fmt.Fprintf(os.Stderr, "starlight: could not execute %q: %v\n", finalCommand, err)
		childHooks.exit(127)
		return err
	}
	return nil
}

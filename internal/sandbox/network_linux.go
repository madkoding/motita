//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// capNetAdmin is CAP_NET_ADMIN, what bringing the namespace's loopback up takes.
const capNetAdmin = 12

// namespaceFailedCode is the isolation child's exit when it could not start the command inside a
// network namespace.
const namespaceFailedCode = 125

// needsNamespaceRelay says whether this isolation child has to start the rest of the work inside
// the namespaces itself: a process that is not root cannot get a network namespace from its parent.
//
// The parent cannot do it for it because motita is not dumpable (hideFromChildren): the files in
// /proc of a child it clones are root's until that child execs, so it cannot write the child's
// uid_map, and a user namespace without a map cannot create a file. The isolation child is a
// fresh exec, dumpable, and can.
func needsNamespaceRelay(spec Spec) bool {
	return spec.Limits.NoNetwork && !spec.InNamespace && osHooks.euid() != 0
}

// userNamespaceAttributes are what a process that is not root needs for a network namespace: a
// user namespace too, where the child is the same user it is outside (what the command writes
// keeps its owner), holding CAP_NET_ADMIN over its own namespace only, to bring the loopback up
// before the command runs (see enterNoNetwork).
func userNamespaceAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: os.Geteuid(), HostID: os.Geteuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: os.Getegid(), HostID: os.Getegid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
		AmbientCaps:                []uintptr{capNetAdmin},
	}
}

// relayInNamespace starts this same child again inside a user and a network namespace of its own,
// forwards its exit code and exits. It stays in the run's process group, so the parent's kill on a
// deadline reaches both.
func relayInNamespace(spec Spec) error {
	spec.InNamespace = true
	readable := workOutReadable(&spec)
	if readable != nil {
		defer readable.Close()
	}
	encoded, err := spec.Encode()
	if err != nil {
		return err
	}
	exe, err := osHooks.executable()
	if err != nil {
		return err
	}
	// nosemgrep: dangerous-exec-command -- exe is this very binary, from os.Executable, never user input.
	cmd := exec.Command(exe, ChildMarker, encoded)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = userNamespaceAttributes()
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	if readable != nil {
		cmd.ExtraFiles = []*os.File{readable} // descriptor 3 of the child: Spec.ReadableFD
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "motita: could not give the command a network of its own: %v\n", err)
		childHooks.exit(namespaceFailedCode)
		return err
	}
	_ = cmd.Wait()
	childHooks.exit(exitCodeOf(cmd.ProcessState))
	return nil
}

// readableFD is the descriptor the child in the namespaces finds the list on: the first of the
// files it inherits.
const readableFD = 3

// workOutReadable lists, here and now, what stays readable around the hidden and private paths, so
// the child in the namespaces uses the answer instead of listing directories itself: some of them
// cannot be listed from inside a user namespace on a kernel that restricts them (AppArmor on
// Ubuntu), and a command that is confined to the workspace does not run when the list is missing.
// When it cannot be worked out here either, the child tries for itself, as it would have.
//
// The list is left in a file that has no name: it is created, unlinked at once and handed to the
// child as an inherited descriptor (the file returned, to pass as its first extra file), so nothing
// in a shared directory can be swapped for it between writing and reading.
func workOutReadable(spec *Spec) *os.File {
	if len(spec.WriteRoots) == 0 {
		return nil
	}
	readable := readableAround(hiddenAndPrivate(*spec))
	if readable == nil {
		return nil
	}
	data, err := jsonMarshal(readable)
	if err != nil {
		return nil
	}
	file, err := os.CreateTemp("", "motita-readable-")
	if err != nil {
		return nil
	}
	_ = os.Remove(file.Name())
	if err := writeReadable(file, data); err != nil {
		_ = file.Close()
		return nil
	}
	spec.ReadableFD = readableFD
	return file
}

// writeReadable writes the list and rewinds the file, a variable so a test can make it fail.
var writeReadable = func(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		return err
	}
	_, err := f.Seek(0, io.SeekStart)
	return err
}

// exitCodeOf is the code a shell would report for the relayed process: its own, or 128 plus the
// signal that ended it.
func exitCodeOf(state *os.ProcessState) int {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return state.ExitCode()
}

// userNamespaceRestriction is the sysctl with which Ubuntu 24.04 and later take every capability
// away from a process in a user namespace made by an unprivileged one: the namespace is created, but
// nobody in it can bring the loopback up, so a command in it could not even reach 127.0.0.1.
const userNamespaceRestriction = "kernel.apparmor_restrict_unprivileged_userns"

// kernelRestrictsNamespaces reports whether userNamespaceRestriction is on. A kernel without it
// has no such rule.
func kernelRestrictsNamespaces() bool {
	data, err := readRestriction("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
	return err == nil && strings.TrimSpace(string(data)) == "1"
}

// restricted reports whether this process's user namespaces lose their capabilities: the
// restriction is on, and this process is not under the profile `motita sandbox apparmor install`
// loads, which the kernel exempts.
func restricted() bool { return kernelRestrictsNamespaces() && !ownProfileAllowsNamespaces() }

// readRestriction is os.ReadFile, a variable so a test can choose what the kernel says.
var readRestriction = os.ReadFile

// probeTarget is a path nothing is at: the probe's child is cloned into the namespaces and its exec
// fails, so nothing runs. "Not found" is the answer that the namespaces were made.
const probeTarget = "/nonexistent/motita-network-probe"

// probeNetworkIsolation asks the kernel for the namespaces a command without a network gets, as
// root or as the relay does it, and reports the refusal. It runs nothing: the clone either fails,
// or succeeds and the exec of probeTarget fails with "not found".
//
// The relay's namespaces are asked for from THIS process, so the answer is only right while it is
// still dumpable: New asks before hideFromChildren, and the answer is kept.
func probeNetworkIsolation() error {
	if osHooks.euid() != 0 && restricted() {
		return errors.New("the kernel gives a user namespace no capabilities " +
			"(" + userNamespaceRestriction + " is 1), so its loopback cannot be brought up")
	}
	return probeNamespaces()
}

// probeNamespaces asks for the namespaces and nothing else (see probeNetworkIsolation).
func probeNamespaces() error {
	attr := &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET}
	if osHooks.euid() != 0 {
		attr = userNamespaceAttributes()
	}
	proc, err := probeStart(probeTarget, []string{probeTarget}, &os.ProcAttr{Sys: attr})
	if err == nil {
		// Something is at the path after all: it was started in the namespaces, which is the
		// question, and is not left running.
		_ = proc.Kill()
		_, _ = proc.Wait()
		return nil
	}
	if errors.Is(err, syscall.ENOENT) {
		return nil
	}
	return err
}

// probeStart is os.StartProcess, as a variable so a test can put something at the probe's path.
var probeStart = os.StartProcess

// ifreqFlags is the kernel's struct ifreq as SIOCGIFFLAGS and SIOCSIFFLAGS read it: the name and
// the flags, padded to the size of the union.
type ifreqFlags struct {
	name  [syscall.IFNAMSIZ]byte
	flags uint16
	_     [22]byte
}

// prCapAmbient and prCapAmbientClearAll are prctl's PR_CAP_AMBIENT and PR_CAP_AMBIENT_CLEAR_ALL.
const (
	prCapAmbient         = 47
	prCapAmbientClearAll = 4
)

// networkHooks are the calls that bring the loopback up and drop the capability, as variables so
// their failures are tested rather than assumed.
var networkHooks = struct {
	socket func() (int, error)
	ioctl  func(fd int, req uintptr, ifr *ifreqFlags) error
	prctl  func(trap, a1, a2, a3 uintptr) (uintptr, uintptr, syscall.Errno)
}{
	socket: func() (int, error) {
		return syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	},
	ioctl: func(fd int, req uintptr, ifr *ifreqFlags) error {
		// nosemgrep: use-of-unsafe-block -- the ioctl takes a pointer to a struct this function owns.
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(ifr)))
		if errno != 0 {
			return errno
		}
		return nil
	},
	prctl: syscall.RawSyscall,
}

// enterNoNetwork prepares, inside the isolation child, the namespace the command runs in: the
// loopback is brought up, so a test that talks to 127.0.0.1 works, and the capability that took
// is dropped so the command does not inherit it. Neither failure stops the command: without the
// loopback it still runs, only without localhost, and the network it cannot reach stays out of
// reach either way.
func enterNoNetwork() {
	_ = bringLoopbackUp()
	_ = dropNetworkCapabilities()
}

// bringLoopbackUp sets IFF_UP on "lo". A new network namespace starts with it down.
func bringLoopbackUp() error {
	fd, err := networkHooks.socket()
	if err != nil {
		return err
	}
	defer func() { _ = syscall.Close(fd) }()
	var ifr ifreqFlags
	copy(ifr.name[:], "lo")
	if err := networkHooks.ioctl(fd, syscall.SIOCGIFFLAGS, &ifr); err != nil {
		return err
	}
	ifr.flags |= syscall.IFF_UP
	return networkHooks.ioctl(fd, syscall.SIOCSIFFLAGS, &ifr)
}

// dropNetworkCapabilities clears the ambient capabilities relayInNamespace raised, so the command
// the child execs next starts with none.
func dropNetworkCapabilities() error {
	if _, _, errno := networkHooks.prctl(syscall.SYS_PRCTL, prCapAmbient, prCapAmbientClearAll, 0); errno != 0 {
		return errno
	}
	return nil
}

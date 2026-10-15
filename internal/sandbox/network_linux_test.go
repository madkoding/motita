//go:build linux

package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/madkoding/motita/internal/execx"
)

// skipWithoutNamespaces skips where this machine cannot give a command a network of its own.
func skipWithoutNamespaces(t *testing.T) {
	t.Helper()
	if err := probeNetworkIsolation(); err != nil {
		t.Skipf("no network namespaces here: %v", err)
	}
}

// A command without a network sees only the loopback, and the loopback is up; an approved one
// keeps the machine's interfaces. As root it is the parent's namespace, otherwise the relay's.
func TestACommandWithoutNetworkHasOnlyAWorkingLoopback(t *testing.T) {
	skipWithoutNamespaces(t)
	withNetworkIsolation(t, nil)
	s, err := New(Options{Dir: t.TempDir(), Limits: Limits{NoNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	script := "grep -c : /proc/net/dev; grep -c 127.0.0.1 /proc/net/fib_trie; exit 3"
	out, _, code, err := s.Run(context.Background(), execx.Request{Command: "sh", Args: []string{"-c", script}})
	if err != nil || code != 3 {
		t.Fatalf("code %d, err %v: %s", code, err, out)
	}
	lines := strings.Fields(out)
	if len(lines) != 2 || lines[0] != "1" || lines[1] == "0" {
		t.Fatalf("want one interface with 127.0.0.1 routed, got %q", out)
	}
	out, _, _, _ = s.Run(context.Background(), execx.Request{Approved: true, Command: "sh",
		Args: []string{"-c", "grep -c : /proc/net/dev"}})
	if strings.TrimSpace(out) == "1" {
		t.Error("an approved command lost the network")
	}
}

// stubProbeStart replaces what the probe starts.
func stubProbeStart(t *testing.T, start func(string, []string, *os.ProcAttr) (*os.Process, error)) {
	t.Helper()
	prev := probeStart
	probeStart = start
	t.Cleanup(func() { probeStart = prev })
}

// The probe runs nothing: "not found" means the namespaces were made, a refusal is returned, and
// something that did start is not left running. Without root it asks for the relay's namespaces.
func TestProbeNetworkIsolationReportsTheRefusal(t *testing.T) {
	var flags uintptr
	stubProbeStart(t, func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		flags = attr.Sys.Cloneflags
		return nil, &os.PathError{Op: "fork/exec", Path: name, Err: syscall.ENOENT}
	})
	prev := osHooks.euid
	t.Cleanup(func() { osHooks.euid = prev })
	osHooks.euid = func() int { return 1000 }
	if err := probeNetworkIsolation(); err != nil || flags&syscall.CLONE_NEWUSER == 0 {
		t.Fatalf("probe = %v, flags %#x", err, flags)
	}
	osHooks.euid = func() int { return 0 }
	if err := probeNetworkIsolation(); err != nil || flags != syscall.CLONE_NEWNET {
		t.Fatalf("probe as root = %v, flags %#x", err, flags)
	}
	stubProbeStart(t, func(string, []string, *os.ProcAttr) (*os.Process, error) { return nil, syscall.EPERM })
	if err := probeNetworkIsolation(); !errors.Is(err, syscall.EPERM) {
		t.Errorf("a refusal = %v", err)
	}
	stubProbeStart(t, func(string, []string, *os.ProcAttr) (*os.Process, error) {
		return os.StartProcess("/bin/sh", []string{"sh", "-c", "sleep 30"}, &os.ProcAttr{})
	})
	if err := probeNetworkIsolation(); err != nil {
		t.Errorf("a started probe = %v", err)
	}
}

// stubChildExit records the child's exits instead of ending the test process.
func stubChildExit(t *testing.T) *[]int {
	t.Helper()
	var exits []int
	prev := childHooks.exit
	childHooks.exit = func(code int) { exits = append(exits, code) }
	t.Cleanup(func() { childHooks.exit = prev })
	return &exits
}

// The relay runs the child again inside the namespaces and forwards its code.
func TestRelayInNamespaceForwardsTheExitCode(t *testing.T) {
	skipWithoutNamespaces(t)
	exits := stubChildExit(t)
	spec := Spec{Command: "sh", Args: []string{"-c", "exit 7"}, Limits: Limits{NoNetwork: true}}
	if err := relayInNamespace(spec); err != nil {
		t.Fatal(err)
	}
	if len(*exits) != 1 || (*exits)[0] != 7 {
		t.Fatalf("exits = %v", *exits)
	}
}

// RunAsChild relays when it is not root and the run asks for no network.
func TestRunAsChildRelaysWithoutRoot(t *testing.T) {
	skipWithoutNamespaces(t)
	exits := stubChildExit(t)
	prev := osHooks.euid
	osHooks.euid = func() int { return 1000 }
	t.Cleanup(func() { osHooks.euid = prev })
	encoded, err := Spec{Command: "sh", Args: []string{"-c", "kill -9 $$"}, Limits: Limits{NoNetwork: true}}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := RunAsChild([]string{ChildMarker, encoded}); err != nil {
		t.Fatal(err)
	}
	if len(*exits) != 1 || (*exits)[0] != 128+int(syscall.SIGKILL) {
		t.Fatalf("exits = %v, want the signal's code", *exits)
	}
}

// The relay's own failures: a spec it cannot encode, no executable, and a start the kernel refuses.
func TestRelayInNamespaceFailures(t *testing.T) {
	exits := stubChildExit(t)
	original := jsonMarshal
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("no serialiser") }
	if err := relayInNamespace(Spec{Command: "true"}); err == nil {
		t.Error("an unencodable spec was relayed")
	}
	jsonMarshal = original

	prev := osHooks.executable
	t.Cleanup(func() { osHooks.executable = prev })
	osHooks.executable = func() (string, error) { return "", errors.New("no executable") }
	if err := relayInNamespace(Spec{Command: "true"}); err == nil {
		t.Error("a relay without an executable succeeded")
	}
	osHooks.executable = func() (string, error) { return filepath.Join(t.TempDir(), "missing"), nil }
	if err := relayInNamespace(Spec{Command: "true"}); err == nil {
		t.Error("a relay that could not start succeeded")
	}
	if len(*exits) != 1 || (*exits)[0] != namespaceFailedCode {
		t.Fatalf("exits = %v", *exits)
	}
}

// stubNetworkHooks replaces the loopback and capability calls for a test.
func stubNetworkHooks(t *testing.T) {
	t.Helper()
	prev := networkHooks
	t.Cleanup(func() { networkHooks = prev })
}

// Inside the namespace the child brings the loopback up and drops the capability before the exec,
// and a failure of either does not stop the command.
func TestRunAsChildPreparesTheNamespace(t *testing.T) {
	stubNetworkHooks(t)
	var calls []string
	networkHooks.socket = func() (int, error) { return syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0) }
	networkHooks.ioctl = func(_ int, req uintptr, ifr *ifreqFlags) error {
		calls = append(calls, "ioctl")
		if req == syscall.SIOCSIFFLAGS && ifr.flags&syscall.IFF_UP == 0 {
			t.Error("the loopback was not set up")
		}
		return nil
	}
	networkHooks.prctl = func(_, a1, a2, _ uintptr) (uintptr, uintptr, syscall.Errno) {
		calls = append(calls, "prctl")
		if a1 != prCapAmbient || a2 != prCapAmbientClearAll {
			t.Errorf("prctl(%d, %d)", a1, a2)
		}
		return 0, 0, syscall.EPERM
	}
	prevExec := childHooks.exec
	t.Cleanup(func() { childHooks.exec = prevExec })
	childHooks.exec = func(string, []string, []string) error {
		calls = append(calls, "exec")
		return nil
	}
	encoded, err := Spec{Command: "/bin/sh", Limits: Limits{NoNetwork: true}, InNamespace: true}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := RunAsChild([]string{ChildMarker, encoded}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "ioctl,ioctl,prctl,exec" {
		t.Fatalf("calls = %v", calls)
	}
}

// Bringing the loopback up reports a socket and an ioctl that fail.
func TestBringLoopbackUpFailures(t *testing.T) {
	stubNetworkHooks(t)
	networkHooks.socket = func() (int, error) { return -1, syscall.EMFILE }
	if err := bringLoopbackUp(); !errors.Is(err, syscall.EMFILE) {
		t.Errorf("socket failure = %v", err)
	}
	networkHooks.socket = func() (int, error) { return syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0) }
	networkHooks.ioctl = func(int, uintptr, *ifreqFlags) error { return syscall.EPERM }
	if err := bringLoopbackUp(); !errors.Is(err, syscall.EPERM) {
		t.Errorf("ioctl failure = %v", err)
	}
	if err := dropNetworkCapabilities(); err != nil {
		t.Errorf("clearing no ambient capability = %v", err)
	}
}

// The real ioctl answers for the loopback; on an unknown descriptor it fails.
func TestTheRealIoctlReadsTheLoopback(t *testing.T) {
	fd, err := networkHooks.socket()
	if err != nil {
		t.Skip(err)
	}
	defer func() { _ = syscall.Close(fd) }()
	var ifr ifreqFlags
	copy(ifr.name[:], "lo")
	if err := networkHooks.ioctl(fd, syscall.SIOCGIFFLAGS, &ifr); err != nil || ifr.flags&syscall.IFF_LOOPBACK == 0 {
		t.Errorf("SIOCGIFFLAGS(lo) = %v, flags %#x", err, ifr.flags)
	}
	if err := networkHooks.ioctl(-1, syscall.SIOCGIFFLAGS, &ifr); err == nil {
		t.Error("an ioctl on no descriptor succeeded")
	}
}

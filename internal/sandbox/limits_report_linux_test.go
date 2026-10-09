//go:build linux

package sandbox

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

// rlimitNproc is RLIMIT_NPROC, which package syscall does not name.
const rlimitNproc = 6

// A limit the shell refuses used to vanish behind `2>/dev/null`: the command ran unbounded and
// nothing said so. Now the sandbox lists it as not applied and the run's output names it.
func TestALimitTheShellRefusesIsReported(t *testing.T) {
	// Far above the kernel's nr_open: refused even to root.
	s, err := New(Options{Dir: t.TempDir(), Limits: Limits{OpenFiles: 2000000000, CPUSeconds: 60}})
	if err != nil {
		t.Fatal(err)
	}
	notApplied := strings.Join(s.NotApplied(), " | ")
	if !strings.Contains(notApplied, "open_files: the shell cannot apply") || strings.Contains(notApplied, "cpu_seconds") {
		t.Errorf("only the refused limit must be listed: %s", notApplied)
	}
	out, _, exit, err := s.Run(context.Background(), execx.Request{Command: "echo", Args: []string{"ran"}})
	if err != nil || exit != 0 || !strings.Contains(out, "ran") ||
		!strings.Contains(out, "could not apply its open_files limit") {
		t.Errorf("the command runs and says which limit it lacks: exit=%d err=%v out=%q", exit, err, out)
	}
}

// Debian's /bin/sh (dash) has no `ulimit -u`: the process limit was never applied there.
func TestTheProcessLimitIsAppliedByEveryShell(t *testing.T) {
	// RLIMIT_NPROC counts every process the user owns, not just the sandbox's, so a small value
	// stops a non-root user's shell from forking at all. Use one high enough not to bite and
	// unusual enough to recognise, within the hard limit a non-root user cannot raise.
	limit := uint64(54321)
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(rlimitNproc, &rl); err == nil && rl.Max < limit {
		limit = rl.Max
	}
	s, err := New(Options{Dir: t.TempDir(), Limits: Limits{Processes: int(limit)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.NotApplied()) != 0 {
		t.Errorf("the process limit must be applicable: %v", s.NotApplied())
	}
	out, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh",
		Args: []string{"-c", "grep 'Max processes' /proc/self/limits"}})
	if want := " " + strconv.FormatUint(limit, 10) + " "; err != nil || !strings.Contains(out, want) {
		t.Errorf("RLIMIT_NPROC must be %d: %v %q", limit, err, out)
	}
}

func TestAShellThatCannotBeAskedIsReported(t *testing.T) {
	real := probeShell
	t.Cleanup(func() { probeShell = real })
	probeShell = "/does/not/exist/sh"
	s, err := New(Options{Dir: t.TempDir(), Limits: Limits{OpenFiles: 64}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(s.NotApplied(), " "), "could not check that the shell applies them") {
		t.Errorf("an unchecked limit must be reported: %v", s.NotApplied())
	}
}

// A process the command left in the background used to outlive the run unless it timed out.
func TestARunTakesItsBackgroundProcessesWithIt(t *testing.T) {
	s, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	out, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh",
		Args: []string{"-c", "sleep 60 >/dev/null 2>&1 & echo $!"}})
	if err != nil {
		t.Fatal(err)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		t.Fatalf("no pid in %q", out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for running(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("the background process %d survived the run", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// running reports a live process: one that exists and is not a zombie waiting for a reaper.
func running(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
	return len(fields) > 0 && fields[0] != "Z"
}

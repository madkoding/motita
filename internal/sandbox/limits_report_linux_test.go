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

// RLIMIT_NPROC is applied where the shell takes `ulimit -u`, and reported as not applied where it
// does not (dash): never silently dropped, and never set through dash's -p, which would count
// every process the user owns and leave the command unable to fork.
func TestTheProcessLimitIsAppliedOrReported(t *testing.T) {
	// A value high enough not to bite a non-root user, and unusual enough to recognise, within
	// the hard limit a non-root user cannot raise.
	limit := uint64(54321)
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(rlimitNproc, &rl); err == nil && rl.Max < limit {
		limit = rl.Max
	}
	s, err := New(Options{Dir: t.TempDir(), Limits: Limits{Processes: int(limit)}})
	if err != nil {
		t.Fatal(err)
	}
	out, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh",
		Args: []string{"-c", "grep 'Max processes' /proc/self/limits"}})
	if err != nil {
		t.Fatal(err)
	}
	applied := strings.Contains(out, " "+strconv.FormatUint(limit, 10)+" ")
	reported := strings.Contains(strings.Join(s.NotApplied(), " "), "processes: the shell cannot apply")
	if applied == reported {
		t.Errorf("the process limit must be either applied or reported, not %s: applied=%v %q notApplied=%v",
			map[bool]string{true: "both", false: "neither"}[applied], applied, out, s.NotApplied())
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

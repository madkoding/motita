//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Applying limits through the shell.
//
// The limits are NOT applied by the isolation process (which is this same
// binary, written in Go): the shell applies them, and the shell is a small C
// binary. It is a deliberate decision, taken from measurements:
//
//   - RLIMIT_AS also counts the virtual memory the Go runtime maps. If the
//     process applies the limit itself, any later reservation (sysmon, GC,
//     resolving PATH) kills it with "fatal error: runtime: cannot allocate
//     memory". Measured: it only failed on a runner with more cores and with
//     the race instrumentation.
//   - RLIMIT_CPU does not cut at the exact instant it is exhausted, but at the
//     process's next scheduling; the Go runtime can reserve memory before that
//     happens. Measured on a single-core machine: with RLIMIT_CPU only (no
//     memory limit) an infinite loop was still alive at 12 s with
//     `cpu_seconds: 2`.
//
// With `sh -c 'ulimit ...; exec "$@"'` the limited process is exactly the
// user's, and the shell disappears with the exec (no intermediate process is
// left consuming the budget).
// ---------------------------------------------------------------------------

// wrapWithUlimit returns the command and the arguments that apply the limits
// and run the real command.
//
// If no limits are configured, it returns the command unwrapped: the cost of an
// unnecessary intermediate shell is not paid.
func wrapWithUlimit(l Limits, command string, args []string) (string, []string) {
	orders := ulimitCommands(l)
	if len(orders) == 0 {
		return command, append([]string{command}, args...)
	}

	// `exec "$@"` runs the command with its arguments without re-interpreting
	// anything: the command and its arguments are passed positionally after the
	// shell's name (the shell's argv[0] is "sh", argv[1] becomes $0, and the
	// command and its arguments start at $1).
	script := strings.Join(orders, "; ") + `; exec "$@"`

	finalArgs := []string{"sh", "-c", script, "motita-sandbox", command}
	finalArgs = append(finalArgs, args...)
	return "/bin/sh", finalArgs
}

// ulimitCommands builds the `ulimit` orders, sorted so that the output is
// deterministic (and the tests can compare it).
//
// `ulimit` errors do not abort the script on purpose: on some systems
// (containers, LSMs) a resource may not be modifiable, and in that case it is
// better to run the command without that limit than to prevent the work. But it
// is not silent: the shell's own message is dropped, and one line naming the
// limit takes its place, so whoever reads the output knows the command ran
// without it. The parent also lists it as not applied (see unappliedLimits).
//
// The process limit is the exception: dash, Debian's /bin/sh, never takes it, so
// the line would ride on every command there. It is reported once, by the parent.
func ulimitCommands(l Limits) []string {
	return ulimitLines(l, func(name string) string {
		if name == "processes" {
			return ":"
		}
		return fmt.Sprintf("echo 'motita: the sandbox could not apply its %s limit' >&2", name)
	})
}

// ulimitLines is one line per limit: the ways of setting it, each tried when the
// one before failed, and then onFailure's command for the limit's name.
func ulimitLines(l Limits, onFailure func(name string) string) []string {
	var lines []string
	add := func(name string, tries ...string) {
		for i, try := range tries {
			tries[i] = try + " 2>/dev/null"
		}
		lines = append(lines, strings.Join(tries, " || ")+" || "+onFailure(name))
	}

	if l.CPUSeconds > 0 {
		add("cpu_seconds", fmt.Sprintf("ulimit -t %d", l.CPUSeconds))
	}
	if l.MemoryMB > 0 {
		// -v (RLIMIT_AS) exists in dash and in bash; it is the one that matters
		// most here.
		add("memory_mb", fmt.Sprintf("ulimit -v %d", l.MemoryMB<<10))
	}
	if l.Processes > 0 {
		// RLIMIT_NPROC counts every process the USER owns, not the sandbox's, so on a
		// desktop or a CI runner a small value stops the command from forking at all.
		// dash (Debian's /bin/sh) refuses -u, and its -p is deliberately not tried:
		// there the limit is reported as not applied, and the cgroup pids controller
		// is what bounds the sandbox's own processes.
		add("processes", fmt.Sprintf("ulimit -u %d", l.Processes))
	}
	if l.OpenFiles > 0 {
		add("open_files", fmt.Sprintf("ulimit -n %d", l.OpenFiles))
	}
	if l.MaxFileSizeMB > 0 {
		// -f is in 512-byte blocks.
		add("max_file_size_mb", fmt.Sprintf("ulimit -f %d", l.MaxFileSizeMB<<11))
	}

	sort.Strings(lines)
	return lines
}

// probeShell is the shell unappliedLimits asks, a variable so a test can make it missing.
var probeShell = "/bin/sh"

// unappliedLimits are the limits the shell cannot apply on this machine, found by
// trying each one once in a throwaway shell. A run would otherwise only say so in
// its own output; this is what lets the sandbox list them as not applied.
func unappliedLimits(l Limits) ([]string, error) {
	lines := ulimitLines(l, func(name string) string { return "echo " + name })
	if len(lines) == 0 {
		return nil, nil
	}
	out, err := exec.Command(probeShell, "-c", strings.Join(lines, "; ")).Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// minimumMemoryMB returns the address space the command should be able to use at
// minimum, in MB: a multiple of the peak of the process launching the sandbox.
//
// It exists because an RLIMIT_AS below what is already mapped means the command
// cannot even start (and, if a Go binary applied it, it would kill the runtime
// itself). The parent uses it to adjust and warn instead of silently leaving the
// command unusable.
//
// On the target machines (i386 with little RAM) the value is small; on a CI
// runner with many cores it is larger, and that is why the adjustment has to be
// dynamic and not a constant.
func minimumMemoryMB() int {
	// A single thread and no GC: this reduces what the runtime reserves in the
	// period from here to the exec.
	runtime.GOMAXPROCS(1)
	debug.SetGCPercent(-1)

	peak := peakVirtualMemory()
	if peak == 0 {
		return 0
	}
	// x2 margin over the observed peak.
	return int(peak>>20) * 2
}

// procStatusPath is the file read to measure the memory peak. It is a variable so
// the unreadable cases can be tested on any system.
var procStatusPath = "/proc/self/status"

// peakVirtualMemory reads the current process's virtual memory peak, in bytes.
// It returns 0 if it cannot be determined (it is not used outside Linux).
func peakVirtualMemory() uint64 {
	data, err := os.ReadFile(procStatusPath)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmPeak:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb << 10
	}
	return 0
}

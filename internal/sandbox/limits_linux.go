//go:build linux

package sandbox

import (
	"fmt"
	"os"
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
// better to run the command without that limit — it is recorded by the parent —
// than to prevent the work. The `2>/dev/null` also stops a shell message from
// contaminating the output the model reads.
func ulimitCommands(l Limits) []string {
	var orders []string

	if l.CPUSeconds > 0 {
		orders = append(orders, fmt.Sprintf("ulimit -t %d 2>/dev/null", l.CPUSeconds))
	}
	if l.MemoryMB > 0 {
		// -v (RLIMIT_AS) exists in dash and in bash; it is the one that matters
		// most here.
		orders = append(orders, fmt.Sprintf("ulimit -v %d 2>/dev/null", l.MemoryMB<<10))
	}
	if l.Processes > 0 {
		// -u (RLIMIT_NPROC) does not exist in dash; it is attempted without
		// aborting.
		orders = append(orders, fmt.Sprintf("ulimit -u %d 2>/dev/null", l.Processes))
	}
	if l.OpenFiles > 0 {
		orders = append(orders, fmt.Sprintf("ulimit -n %d 2>/dev/null", l.OpenFiles))
	}
	if l.MaxFileSizeMB > 0 {
		// -f is in 512-byte blocks.
		blocks := l.MaxFileSizeMB << 11
		orders = append(orders, fmt.Sprintf("ulimit -f %d 2>/dev/null", blocks))
	}

	sort.Strings(orders)
	return orders
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

//go:build !linux

package sandbox

// Outside Linux there is no ulimit in the same sense: the system's shell
// decides. The command is returned unwrapped and the parent records that no
// process limits are applied (only the temporary directory is isolated).
func wrapWithUlimit(l Limits, command string, args []string) (string, []string) {
	return command, append([]string{command}, args...)
}

func minimumMemoryMB() int { return 0 }

func peakVirtualMemory() uint64 { return 0 }

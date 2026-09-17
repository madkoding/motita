//go:build !windows

package sandbox

// The portable tests need to run a command and see its output. Unix has an echo
// that is a real binary, so the command can be run directly without a shell.

import "os/exec"

func echoCommand(text string) (string, []string) { return "/bin/echo", []string{text} }

func failCommand() (string, []string) { return "/bin/false", nil }

func sleepCommand(seconds int) (string, []string) { return "/bin/sleep", []string{itoa(seconds)} }

// itoa avoids importing strconv just for this.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

var _ = exec.Command

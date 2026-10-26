package sandbox

import (
	"os"
	"strings"
)

// procStatus is where the kernel lists a process' flags; a variable so a test can point elsewhere.
var procStatus = "/proc/self/status"

// NoNewPrivs reports whether this very process has NO_NEW_PRIVS set (NoNewPrivs: 1 in
// /proc/self/status). A flag cannot be taken off, and every child inherits it: a set-user-ID
// program such as sudo then cannot gain privileges, however a command was approved. The sandbox
// only sets it inside the child of a confined run, so a gateway that has it was started that way
// (a systemd unit with NoNewPrivileges=yes, a container with no-new-privileges). False where the
// file cannot be read (Windows, macOS).
func NoNewPrivs() bool {
	data, err := os.ReadFile(procStatus)
	if err != nil {
		return false
	}
	return noNewPrivsIn(string(data))
}

// noNewPrivsIn reads the NoNewPrivs line of a /proc/<pid>/status text.
func noNewPrivsIn(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok && name == "NoNewPrivs" {
			return strings.TrimSpace(value) == "1"
		}
	}
	return false
}

// NoNewPrivsHint says what the flag costs and how to take it off, for the settings page and for
// the result of a command that needed privileges.
const NoNewPrivsHint = "Motita was started with NO_NEW_PRIVS (NoNewPrivs: 1 in /proc/self/status), which no " +
	"process can take off and every command inherits, so sudo and other set-user-ID programs cannot gain " +
	"privileges even when approved. To fix it, start Motita without the flag: remove NoNewPrivileges=yes from " +
	"its systemd unit (then systemctl daemon-reload and restart it), or run the container without " +
	"--security-opt no-new-privileges."

//go:build windows

package agent

import "os"

// defaultShell is the interpreter used to run an action written by the model when the
// agent is not in read-only mode. Windows has no sh.
func defaultShell() string {
	if s := os.Getenv("ComSpec"); s != "" {
		return s
	}
	return "cmd.exe"
}

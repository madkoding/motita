//go:build !windows

package agent

// defaultShell is the interpreter used to run an action written by the model when the
// agent is not in read-only mode.
func defaultShell() string { return "/bin/sh" }

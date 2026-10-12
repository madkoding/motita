//go:build !windows

package agent

import "github.com/madkoding/motita/internal/execx"

// defaultShell is the interpreter used to run an action written by the model when the
// agent is not in read-only mode.
func defaultShell() string { return execx.Shell }

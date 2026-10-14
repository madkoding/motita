//go:build !windows

package anchor

// declaredShell runs the lines of .motita/anchor. `sh` by name, found on PATH, so the gate
// runs the same shell a contributor runs it with.
var declaredShell = "sh"

package agent

import (
	"os"
	"testing"
)

// requireSandboxNode skips a test that runs `node --test` inside the sandbox when node is not in
// the sandbox's fixed system PATH (a machine whose node lives in a user tools directory).
func requireSandboxNode(t *testing.T) {
	t.Helper()
	for _, p := range []string{"/usr/local/bin/node", "/usr/bin/node", "/bin/node"} {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	t.Skip("node is not in the sandbox system PATH on this machine")
}

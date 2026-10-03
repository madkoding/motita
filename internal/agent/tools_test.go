package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// TestACheckGetsTheCheckTimeout replays the real session: `make check` outlived the 60 s command
// timeout. A check gets the check timeout; any other command keeps the command timeout; a check
// timeout shorter than the command one never shortens it.
func TestACheckGetsTheCheckTimeout(t *testing.T) {
	a := agentWith(t, false)
	a.cfg.Sandbox.Timeout = time.Minute
	a.cfg.Sandbox.CheckTimeout = 15 * time.Minute

	if got := a.planRequest("make check > /tmp/check.out 2>&1").Request.Timeout; got != 15*time.Minute {
		t.Errorf("a check must get the check timeout, got %s", got)
	}
	if got := a.planRequest("ls -la").Request.Timeout; got != time.Minute {
		t.Errorf("a plain command keeps the command timeout, got %s", got)
	}
	a.cfg.Sandbox.CheckTimeout = time.Second
	if got := a.planRequest("go test ./...").Request.Timeout; got != time.Minute {
		t.Errorf("a shorter check timeout must not shorten the command one, got %s", got)
	}
}

// TestDescribeTools: the model is told where HOME is and where tools persist, and nothing at all
// when there is no tools directory or a chroot cannot see it.
func TestDescribeTools(t *testing.T) {
	got := describeTools(config.Sandbox{Kind: "none", ToolsDir: "/opt/mt"})
	for _, want := range []string{"HOME is /opt/mt/home", "/opt/mt/tools/<name>/", "/opt/mt/bin", "never use sudo", "installing-a-toolchain"} {
		if !strings.Contains(got, want) {
			t.Errorf("the description must say %q:\n%s", want, got)
		}
	}
	if describeTools(config.Sandbox{ToolsDir: " "}) != "" || describeTools(config.Sandbox{Kind: "chroot", ToolsDir: "/opt/mt"}) != "" {
		t.Error("with no usable tools directory nothing is said")
	}
	a := agentWith(t, false)
	a.cfg.Sandbox.ToolsDir = "/opt/mt"
	if !strings.Contains(a.baseVariables(taskOf("x"))["tools"], "/opt/mt/home") {
		t.Error("the prompt variables must carry the tools description")
	}
}

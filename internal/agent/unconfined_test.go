package agent

import (
	"testing"

	"github.com/madkoding/motita/internal/policy"
)

// withoutConfinement stands the test on a platform where nothing confines a command (Windows).
func withoutConfinement(t *testing.T) {
	t.Helper()
	old := confinementAvailable
	confinementAvailable = func() bool { return false }
	t.Cleanup(func() { confinementAvailable = old })
}

// Where nothing confines a command, the agent asks before the project's code runs and before a
// reader reaches outside the workspace; reading the workspace stays silent.
func TestWithoutConfinementTheAgentAsks(t *testing.T) {
	withoutConfinement(t)
	a := agentWith(t, false)
	a.cfg.Agent.Policy.Enforce = true
	for line, rule := range map[string]string{
		"python t.py":          "unconfined-run",
		"cat ~/.motita/auth/x": "unconfined-read",
		"cat ../../x":          "unconfined-read",
	} {
		if p := a.planRequest(line); p.Verdict != policy.Ask || p.Rule != rule {
			t.Errorf("%q: got %s/%s, want ask/%s", line, p.Verdict, p.Rule, rule)
		}
	}
	if p := a.planRequest("cat README.md"); p.Verdict != policy.Allow {
		t.Errorf("a read inside the workspace: got %s/%s (%s)", p.Verdict, p.Rule, p.Reason)
	}
	if !a.policyMode().Unconfined {
		t.Error("the mode must say nothing confines the commands")
	}
}

// Under confinement the sandbox keeps the user's keys away, and the agent does not ask.
func TestWithConfinementTheAgentRunsProjectCode(t *testing.T) {
	old := confinementAvailable
	confinementAvailable = func() bool { return true }
	t.Cleanup(func() { confinementAvailable = old })
	a := agentWith(t, false)
	a.cfg.Agent.Policy.Enforce = true
	if p := a.planRequest("python t.py"); p.Verdict != policy.Allow {
		t.Errorf("python t.py: got %s/%s", p.Verdict, p.Rule)
	}
}

// Under cmd.exe the agent reads a line with cmd's own syntax as a shell line, approved as a whole.
func TestTheCmdShellLineIsApprovedWhole(t *testing.T) {
	a := agentWith(t, false)
	a.cfg.Agent.Policy.Enforce = true
	a.cfg.Agent.Shell = "cmd.exe"
	if p := a.planRequest(`echo x > %USERPROFILE%\evil.bat`); p.Verdict != policy.Ask || p.Rule != "shell-line" {
		t.Errorf("got %s/%s, want ask/shell-line", p.Verdict, p.Rule)
	}
	if !a.policyMode().Cmd {
		t.Error("the mode must say the line runs under cmd.exe")
	}
}

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

// sandbox.ask_unconfined_runs: false lets the project's code run without confinement and without a
// question; a read outside the workspace is still asked about.
func TestTheOperatorMayTrustUnconfinedRuns(t *testing.T) {
	withoutConfinement(t)
	a := agentWith(t, false)
	a.cfg.Agent.Policy.Enforce = true
	a.cfg.Sandbox.AskUnconfinedRuns = false
	if p := a.planRequest("python t.py"); p.Verdict != policy.Allow {
		t.Errorf("python t.py: got %s/%s, want allow", p.Verdict, p.Rule)
	}
	if p := a.planRequest("cat ../../x"); p.Verdict != policy.Ask || p.Rule != "unconfined-read" {
		t.Errorf("cat ../../x: got %s/%s, want ask/unconfined-read", p.Verdict, p.Rule)
	}
}

// Allowing every command in the settings reaches the mode, the commands keep their sandbox, and
// what the policy refuses outright stays refused.
func TestApproveAllCommandsReachesThePolicyMode(t *testing.T) {
	a := agentWith(t, false)
	a.cfg.Agent.Policy.Enforce = true
	if a.policyMode().ApproveAll {
		t.Fatal("asking is the default")
	}
	a.cfg.Agent.ApproveAllCommands = true
	if !a.policyMode().ApproveAll {
		t.Fatal("the setting must reach the mode")
	}
	p := a.planRequest("sudo ls")
	if p.Verdict != policy.Allow || p.Request.Unconfined {
		t.Errorf("sudo ls: got %s, unconfined %v; want allow, still confined", p.Verdict, p.Request.Unconfined)
	}
	if p := a.planRequest("mkfs.ext4 /dev/sda1"); p.Verdict != policy.Deny {
		t.Errorf("the mandatory floor: got %s", p.Verdict)
	}
}

// The run-every-command choice is read at each command from the source the session installs, so a
// change made in the settings while a turn runs reaches that turn's next command, and the
// background agents it starts.
func TestApproveAllSourceIsReadAtEachCommand(t *testing.T) {
	a := agentWith(t, false)
	a.cfg.Agent.Policy.Enforce = true
	on := false
	a.SetApproveAllSource(func() bool { return on })
	if p := a.planRequest("sudo ls"); p.Verdict != policy.Ask {
		t.Fatalf("before the change sudo asks: %s", p.Verdict)
	}
	on = true
	if p := a.planRequest("sudo ls"); p.Verdict != policy.Allow {
		t.Errorf("after the change the same agent runs it: %s", p.Verdict)
	}
	if p := a.planRequest("mkfs.ext4 /dev/sda1"); p.Verdict != policy.Deny {
		t.Errorf("the mandatory floor: got %s", p.Verdict)
	}
	child := a.newChild(childPlace{dir: t.TempDir()}, "explore", TierStrong)
	if !child.policyMode().ApproveAll {
		t.Error("a background agent must read the same choice")
	}
	on = false
	if child.policyMode().ApproveAll {
		t.Error("and follow it when it is taken back")
	}
	a.SetApproveAllSource(nil)
	a.cfg.Agent.ApproveAllCommands = true
	if !a.policyMode().ApproveAll {
		t.Error("with no source the configuration decides")
	}
}

package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// The model is told which shell it writes for only when that shell is not sh.
func TestDescribeShellNamesANonPOSIXShell(t *testing.T) {
	if got := describeShell("/bin/sh"); got != "" {
		t.Errorf("sh = %q, want nothing", got)
	}
	if got := describeShell(`C:\Program Files\PowerShell\7\pwsh.exe`); !strings.Contains(got, "PowerShell") ||
		!strings.Contains(got, "files-and-directories") {
		t.Errorf("pwsh = %q", got)
	}
	if got := describeShell(`C:\Windows\system32\cmd.exe`); !strings.Contains(got, "cmd.exe") {
		t.Errorf("cmd = %q", got)
	}
}

// A configured PowerShell is handed its line with -Command, and the policy is told the line
// runs under PowerShell.
func TestAConfiguredPowerShellIsSpokenToAsPowerShell(t *testing.T) {
	cfg := config.Config{}
	cfg.Agent.Shell = "pwsh"
	a := &Agent{cfg: cfg}
	if got := a.shellRequest("Get-Date"); got.Command != "pwsh" ||
		!reflect.DeepEqual(got.Args, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Get-Date"}) {
		t.Errorf("request = %+v", got)
	}
	if !a.policyMode().PowerShell {
		t.Error("the policy was not told about PowerShell")
	}
	if !strings.Contains(a.baseVariables(textTask("x"))["tools"], "PowerShell") {
		t.Error("the prompt does not name the shell")
	}

	a.cfg.Agent.Shell = ""
	if a.policyMode().PowerShell {
		t.Error("sh is not PowerShell")
	}
}

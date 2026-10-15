package app

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSandboxApparmorIsParsed(t *testing.T) {
	for _, action := range []string{"print", "install", "remove"} {
		fl, err := parse([]string{"sandbox", "apparmor", action})
		if err != nil || fl.sandboxAction != action {
			t.Errorf("%s: %+v, %v", action, fl, err)
		}
	}
	bads := [][]string{
		{"sandbox"}, {"sandbox", "apparmor"}, {"sandbox", "selinux", "print"}, {"sandbox", "apparmor", "wipe"},
	}
	for _, bad := range bads {
		if _, err := parse(bad); err == nil || !strings.Contains(err.Error(), "sandbox apparmor <print|install|remove>") {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if !strings.Contains(Usage(), "sandbox apparmor") {
		t.Error("the usage does not list the command")
	}
}

func TestSandboxApparmorCommands(t *testing.T) {
	silence(t)
	var installed string
	removed := 0
	run := func(action string, installErr, removeErr error, exe string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Run(Options{
			Args: []string{"sandbox", "apparmor", action}, Out: &out, Err: &errb, ExePath: exe,
			InstallAppArmor: func(e string) error {
				installed = e
				return installErr
			},
			RemoveAppArmor: func() error {
				removed++
				return removeErr
			},
		})
		return code, out.String(), errb.String()
	}

	if code, out, _ := run("print", nil, nil, "/usr/local/bin/motita"); code != Success ||
		!strings.Contains(out, `profile motita "/usr/local/bin/motita"`) {
		t.Errorf("print: %d %q", code, out)
	}
	if code, _, errs := run("print", nil, nil, "relative"); code != RunError || !strings.Contains(errs, "absolute") {
		t.Errorf("print of a relative path: %d %q", code, errs)
	}
	if code, out, _ := run("install", nil, nil, "/opt/motita"); code != Success || installed != "/opt/motita" ||
		!strings.Contains(out, "loaded") {
		t.Errorf("install: %d %q %q", code, out, installed)
	}
	if code, _, errs := run("install", errors.New("needs root"), nil, "/opt/motita"); code != RunError ||
		!strings.Contains(errs, "needs root") {
		t.Errorf("install failure: %d %q", code, errs)
	}
	if code, out, _ := run("remove", nil, nil, "/opt/motita"); code != Success || removed != 1 ||
		!strings.Contains(out, "removed") {
		t.Errorf("remove: %d %q", code, out)
	}
	if code, _, errs := run("remove", nil, errors.New("busy"), "/opt/motita"); code != RunError ||
		!strings.Contains(errs, "busy") {
		t.Errorf("remove failure: %d %q", code, errs)
	}
}

package app

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/tailscale"
)

func TestGatewaySetupOffersTheAppArmorProfile(t *testing.T) {
	cases := []struct {
		name    string
		hint    string
		input   string
		sudoErr error
		want    string
		wantErr string
		runs    int
	}{
		{"nothing needed", "", "y\n", nil, "", "", 0},
		{"declined", "hint", "\n", nil, "not installed; later: sudo motita sandbox apparmor install", "", 0},
		{"installed", "hint", "yes\n", nil, "profile installed", "", 1},
		{"sudo failed", "hint", "y\n", errors.New("sudo: a password is required"), "", "run by hand", 1},
	}
	for _, tc := range cases {
		op, out, errOut := netOptions("")
		op.ExePath = "/opt/motita"
		runs := 0
		op.AppArmorHint = func() string { return tc.hint }
		op.SudoAppArmor = func(exe string) error {
			runs++
			if exe != "/opt/motita" {
				t.Errorf("%s: installed for %q", tc.name, exe)
			}
			return tc.sudoErr
		}
		op.offerAppArmor(bufio.NewReader(strings.NewReader(tc.input)))
		if runs != tc.runs {
			t.Errorf("%s: sudo ran %d times, want %d", tc.name, runs, tc.runs)
		}
		if !strings.Contains(out.String(), tc.want) || !strings.Contains(errOut.String(), tc.wantErr) {
			t.Errorf("%s: output %q, errors %q", tc.name, out, errOut)
		}
		if tc.hint == "" && out.Len() != 0 {
			t.Errorf("%s: asked although nothing is needed: %q", tc.name, out)
		}
	}
	op, out, _ := netOptions("")
	op.offerAppArmor(bufio.NewReader(strings.NewReader("y\n")))
	if out.Len() != 0 {
		t.Errorf("not looked for, yet it asked: %q", out)
	}
}

func TestTheNetworkQuestionEndsWithTheAppArmorOffer(t *testing.T) {
	path := netConfig(t, "gateway:\n  enabled: true\n")
	op, out, _ := netOptions("1\nn\n")
	op.AppArmorHint = func() string { return "hint" }
	op.SudoAppArmor = func(string) error { t.Error("installed without being asked"); return nil }
	if err := op.askExposure(context.Background(), bufio.NewReader(op.Stdin), path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "SANDBOX:") {
		t.Errorf("the profile was not offered:\n%s", out)
	}
}

func TestAFailedTailnetPublishStopsBeforeTheAppArmorOffer(t *testing.T) {
	path := netConfig(t, "gateway:\n  enabled: true\n")
	op, out, _ := netOptions("1\ny\n")
	op.Tailscale = &fakeTS{
		serveErr: errors.New("Access denied"),
		status:   tailscale.Status{Installed: true, Running: true, DNSName: "b.ts.net"},
	}
	op.AppArmorHint = func() string { return "hint" }
	op.SudoAppArmor = func(string) error { return nil }
	err := op.askExposure(context.Background(), bufio.NewReader(op.Stdin), path)
	if err == nil || !strings.Contains(err.Error(), "Access denied") || strings.Contains(out.String(), "SANDBOX:") {
		t.Errorf("err = %v, output:\n%s", err, out)
	}
}

func TestSudoRunsTheInstallOfThisBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for sudo")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > " + log + "\necho from-sudo\n[ \"$FAIL\" = 1 ] && exit 3\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	op, out, _ := netOptions("")
	if err := op.sudoAppArmor("/opt/motita"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	if strings.TrimSpace(string(got)) != "/opt/motita sandbox apparmor install" ||
		!strings.Contains(out.String(), "from-sudo") {
		t.Errorf("sudo got %q, printed %q", got, out)
	}
	t.Setenv("FAIL", "1")
	if err := op.sudoAppArmor("/opt/motita"); err == nil {
		t.Error("a failing sudo reported success")
	}
}

func TestSudoMissingIsAnError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	op, _, _ := netOptions("")
	if err := op.sudoAppArmor("/opt/motita"); err == nil {
		t.Error("no sudo on the machine, yet no error")
	}
}

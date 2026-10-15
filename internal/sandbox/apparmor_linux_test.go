//go:build linux

package sandbox

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// stubAppArmor replaces the calls the install makes and records what they were asked.
type appArmorCalls struct {
	written map[string]string
	removed []string
	ran     []string
}

func stubAppArmor(t *testing.T, euid int) *appArmorCalls {
	t.Helper()
	calls := &appArmorCalls{written: map[string]string{}}
	prevHooks, prevEuid := appArmorHooks, osHooks.euid
	t.Cleanup(func() { appArmorHooks, osHooks.euid = prevHooks, prevEuid })
	osHooks.euid = func() int { return euid }
	appArmorHooks.writeFile = func(name string, data []byte, _ os.FileMode) error {
		calls.written[name] = string(data)
		return nil
	}
	appArmorHooks.remove = func(name string) error {
		calls.removed = append(calls.removed, name)
		return nil
	}
	appArmorHooks.stat = func(string) (os.FileInfo, error) { return nil, nil }
	appArmorHooks.run = func(name string, args ...string) ([]byte, error) {
		calls.ran = append(calls.ran, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	appArmorHooks.readLabel = func() ([]byte, error) { return []byte("unconfined\n"), nil }
	return calls
}

func TestInstallAppArmorWritesAndLoadsTheProfile(t *testing.T) {
	calls := stubAppArmor(t, 0)
	if err := InstallAppArmor("/usr/local/bin/motita"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls.written[AppArmorProfilePath], `"/usr/local/bin/motita"`) ||
		len(calls.ran) != 1 || calls.ran[0] != "apparmor_parser -r "+AppArmorProfilePath {
		t.Errorf("written %v, ran %v", calls.written, calls.ran)
	}
}

func TestInstallAppArmorRefusals(t *testing.T) {
	stubAppArmor(t, 1000)
	if err := InstallAppArmor("/usr/local/bin/motita"); err == nil || !strings.Contains(err.Error(), "root") {
		t.Errorf("without root: %v", err)
	}
	calls := stubAppArmor(t, 0)
	if err := InstallAppArmor("motita"); err == nil {
		t.Error("a relative path was accepted")
	}
	appArmorHooks.writeFile = func(string, []byte, os.FileMode) error { return fs.ErrPermission }
	if err := InstallAppArmor("/usr/local/bin/motita"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("write failure: %v", err)
	}
	appArmorHooks.writeFile = func(string, []byte, os.FileMode) error { return nil }
	appArmorHooks.run = func(string, ...string) ([]byte, error) { return []byte("parse error\n"), errors.New("exit 1") }
	err := InstallAppArmor("/usr/local/bin/motita")
	if err == nil || !strings.Contains(err.Error(), "parse error") || len(calls.removed) != 1 {
		t.Errorf("parser failure: %v, removed %v", err, calls.removed)
	}
}

func TestRemoveAppArmor(t *testing.T) {
	stubAppArmor(t, 1000)
	if err := RemoveAppArmor(); err == nil {
		t.Error("removed without root")
	}
	calls := stubAppArmor(t, 0)
	appArmorHooks.stat = func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist }
	if err := RemoveAppArmor(); err != nil || len(calls.ran) != 0 {
		t.Errorf("nothing installed: %v, ran %v", err, calls.ran)
	}
	appArmorHooks.stat = func(string) (os.FileInfo, error) { return nil, nil }
	if err := RemoveAppArmor(); err != nil || calls.ran[0] != "apparmor_parser -R "+AppArmorProfilePath ||
		len(calls.removed) != 1 {
		t.Errorf("installed: %v, ran %v, removed %v", err, calls.ran, calls.removed)
	}
	appArmorHooks.run = func(string, ...string) ([]byte, error) { return []byte("busy"), errors.New("exit 1") }
	if err := RemoveAppArmor(); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Errorf("unload failure: %v", err)
	}
}

// Under its own profile the program is exempt from the restriction, and the hint goes away.
func TestTheHintAppearsOnlyWhereTheProfileWouldHelp(t *testing.T) {
	stubAppArmor(t, 1000)
	stubRestriction(t, "1\n", nil)
	if h := NetworkIsolationHint(); !strings.Contains(h, "sudo motita sandbox apparmor install") || !restricted() {
		t.Errorf("restricted: hint %q, restricted %v", h, restricted())
	}
	appArmorHooks.readLabel = func() ([]byte, error) { return []byte("motita (unconfined)\n"), nil }
	if h := NetworkIsolationHint(); h != "" || restricted() {
		t.Errorf("under the profile: hint %q, restricted %v", h, restricted())
	}
	appArmorHooks.readLabel = func() ([]byte, error) { return nil, fs.ErrNotExist }
	stubRestriction(t, "0\n", nil)
	if NetworkIsolationHint() != "" {
		t.Error("a hint where the kernel does not restrict")
	}
	stubRestriction(t, "1\n", nil)
	osHooks.euid = func() int { return 0 }
	if NetworkIsolationHint() != "" {
		t.Error("a hint for root")
	}
}

// The notice that isolation is not applied carries the hint, so every place that prints it
// (doctor, -isolation, the terminal) tells the reader what to do.
func TestTheNotAppliedNoticeCarriesTheHint(t *testing.T) {
	stubAppArmor(t, 1000)
	stubRestriction(t, "1\n", nil)
	withNetworkIsolation(t, errors.New("restricted"))
	s, err := New(Options{Dir: t.TempDir(), Limits: Limits{NoNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.NotApplied(), "|"); !strings.Contains(got, "sudo motita sandbox apparmor install") {
		t.Errorf("not applied = %q", got)
	}
}

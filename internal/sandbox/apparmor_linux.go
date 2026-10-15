//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// appArmorHooks are the calls the install and removal make, as variables so their failures are
// tested rather than assumed.
var appArmorHooks = struct {
	writeFile func(string, []byte, os.FileMode) error
	remove    func(string) error
	stat      func(string) (os.FileInfo, error)
	run       func(name string, args ...string) ([]byte, error)
	readLabel func() ([]byte, error)
}{
	writeFile: os.WriteFile,
	remove:    os.Remove,
	stat:      os.Stat,
	run: func(name string, args ...string) ([]byte, error) {
		// nosemgrep: dangerous-exec-command -- the program is the fixed apparmor_parser, the arguments a fixed flag and our own profile path.
		return exec.Command(name, args...).CombinedOutput()
	},
	readLabel: func() ([]byte, error) { return os.ReadFile("/proc/self/attr/current") },
}

// InstallAppArmor writes the profile for the binary at exe and loads it, so the commands of a
// system that restricts user namespaces can run without a network. It needs root.
func InstallAppArmor(exe string) error {
	if osHooks.euid() != 0 {
		return errors.New("installing an AppArmor profile needs root: run it with sudo")
	}
	profile, err := AppArmorProfile(exe)
	if err != nil {
		return err
	}
	if err := appArmorHooks.writeFile(AppArmorProfilePath, []byte(profile), 0o644); err != nil {
		return err
	}
	if out, err := appArmorHooks.run("apparmor_parser", "-r", AppArmorProfilePath); err != nil {
		_ = appArmorHooks.remove(AppArmorProfilePath)
		return fmt.Errorf("apparmor_parser could not load the profile: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveAppArmor unloads the profile and deletes it. A profile that is not there is not an error.
func RemoveAppArmor() error {
	if osHooks.euid() != 0 {
		return errors.New("removing an AppArmor profile needs root: run it with sudo")
	}
	if _, err := appArmorHooks.stat(AppArmorProfilePath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if out, err := appArmorHooks.run("apparmor_parser", "-R", AppArmorProfilePath); err != nil {
		return fmt.Errorf("apparmor_parser could not unload the profile: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return appArmorHooks.remove(AppArmorProfilePath)
}

// ownProfileAllowsNamespaces says whether this very process runs under the profile
// InstallAppArmor loads: the kernel then lets it make user namespaces that keep their
// capabilities, whatever the system-wide restriction says.
func ownProfileAllowsNamespaces() bool {
	label, err := appArmorHooks.readLabel()
	return err == nil && strings.HasPrefix(string(label), appArmorProfileName+" (")
}

// NetworkIsolationHint says what would make network isolation available on this machine, or ""
// when the reason is not one this program can fix. It is shown beside the "not applied" notice.
func NetworkIsolationHint() string {
	if osHooks.euid() == 0 || !kernelRestrictsNamespaces() || ownProfileAllowsNamespaces() {
		return ""
	}
	return "to allow it, run once: sudo motita sandbox apparmor install " +
		"(a profile that lets this binary make user namespaces; nothing else is confined)"
}

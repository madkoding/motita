//go:build linux

package sandbox

import (
	"os"
	"testing"
)

// TestInstallTheAppArmorProfileForThisBinary is a step of the network isolation CI job on Ubuntu
// 24.04, not a test of its own: run as root, it loads the profile `motita sandbox apparmor
// install` would for this test binary, so the next step can check that the restriction no
// longer stops the isolation.
func TestInstallTheAppArmorProfileForThisBinary(t *testing.T) {
	if os.Getenv("MOTITA_NETISO_INSTALL") == "" {
		t.Skip("MOTITA_NETISO_INSTALL is not set: this is a step of the network isolation CI job")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallAppArmor(exe); err != nil {
		t.Fatal(err)
	}
}

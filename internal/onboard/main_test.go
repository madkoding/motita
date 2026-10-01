package onboard

import (
	"os"
	"testing"
)

// TestMain puts every test under a HOME of its own, so a login or a key stored on the machine
// running the tests can never change what the wizard offers (it offers to keep both).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "motita-onboard-home-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Unsetenv("MOTITA_AUTH_DIR")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

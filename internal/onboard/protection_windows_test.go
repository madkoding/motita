//go:build windows

package onboard

import (
	"strings"
	"testing"
)

// TestCredentialsProtectionIsHonestOnWindows: os.Chmod on Windows only toggles the
// read-only bit, so the wizard must not claim to have set 0600. Saying it would be
// a promise the program cannot keep.
func TestCredentialsProtectionIsHonestOnWindows(t *testing.T) {
	got := credentialsProtection()
	if strings.Contains(got, "0600") {
		t.Errorf("the message claims 0600, which Windows does not apply: %q", got)
	}
	if !strings.Contains(got, "ACL") {
		t.Errorf("the message must say where the permissions really come from: %q", got)
	}
}

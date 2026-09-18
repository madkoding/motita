//go:build !windows

package onboard

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestCredentialsAreReallyPrivateOnThisPlatform: on Unix the file mode is real, so
// it is asserted rather than merely described.
func TestCredentialsAreReallyPrivateOnThisPlatform(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"openai", "1", "2", "", "sk-a-key"}, Answers{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	info, err := os.Stat(res.CredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the credentials file is %o, want 600", perm)
	}
	if got := credentialsProtection(); !strings.Contains(got, "0600") {
		t.Errorf("the message must state the real protection: %q", got)
	}
}

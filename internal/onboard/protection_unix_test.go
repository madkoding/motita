//go:build !windows

package onboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCredentialsAreReallyPrivateOnThisPlatform: on Unix the file mode is real, so
// it is asserted rather than merely described.
func TestCredentialsAreReallyPrivateOnThisPlatform(t *testing.T) {
	dir := t.TempDir()
	_, res, err := run(context.Background(), t, dir, []string{"openai", "", "sk-a-key", "1", "3", ""}, Answers{})
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

// TestConfigDirectoryIsPrivate: the directory the wizard creates holds the
// credentials and the log, so other users cannot list or read it.
func TestConfigDirectoryIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".motita")
	if _, _, err := run(context.Background(), t, dir, []string{"openai", "", "sk-a-key", "1", "3", ""}, Answers{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("the configuration directory is %o, want 700", perm)
	}
}

package gitx

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestSetEnvReachesGit proves the environment set by SetEnv is the one git runs with: an inline
// config entry (how motita names its credential helper) is visible to the child, and nil goes back
// to inheriting the process environment.
func TestSetEnvReachesGit(t *testing.T) {
	t.Cleanup(func() { SetEnv(nil) })
	SetEnv([]string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=motita.probe", "GIT_CONFIG_VALUE_0=helper",
	})
	out, err := execCommand(context.Background(), "git", "config", "--get", "motita.probe")
	if err != nil || strings.TrimSpace(string(out)) != "helper" {
		t.Fatalf("git did not see the configured environment: %q, %v", out, err)
	}
	SetEnv(nil)
	if env := Env(); env != nil {
		t.Fatalf("SetEnv(nil) left an environment: %v", env)
	}
	if out, err := execCommand(context.Background(), "git", "config", "--get", "motita.probe"); err == nil {
		t.Fatalf("the probe survived SetEnv(nil): %q", out)
	}
}

package sandbox

import (
	"strings"
	"testing"
)

// The git the agent runs in a worktree must see the user's git configuration and the logins
// motita holds. The sandbox replaces HOME, so without these variables a worktree's git found
// neither, however the user had configured git globally.
func TestEnvironmentCarriesGitConfiguration(t *testing.T) {
	s := &Sandbox{base: t.TempDir(), executable: "/opt/motita"}
	plain := strings.Join(s.environment(), "\n")
	if strings.Contains(plain, "GIT_") || strings.Contains(plain, "MOTITA_AUTH_DIR") {
		t.Errorf("a sandbox with no git options must not add any:\n%s", plain)
	}
	s.op.GitAuthDir = "/auth"
	env := strings.Join(s.environment(), "\n")
	for _, want := range []string{"MOTITA_AUTH_DIR=/auth", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_VALUE_0=!'/opt/motita' git-credential"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q in\n%s", want, env)
		}
	}
}

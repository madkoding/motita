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
	env := strings.Join(s.environmentWithTmp("/tmp", true), "\n")
	for _, want := range []string{"MOTITA_AUTH_DIR=/auth", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_VALUE_0=!'/opt/motita' git-credential"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q in\n%s", want, env)
		}
	}
}

// The credential helper hands motita's token to whoever runs `git credential fill`, so only a
// command somebody approved gets it, never a confined one nobody looked at.
func TestAConfinedCommandGetsNoLogins(t *testing.T) {
	s := &Sandbox{base: t.TempDir(), executable: "/opt/motita", op: Options{GitAuthDir: "/auth"}}
	for _, env := range [][]string{s.environmentWithTmp("/tmp", false), s.environment()} {
		joined := strings.Join(env, "\n")
		if strings.Contains(joined, "MOTITA_AUTH_DIR") || strings.Contains(joined, "credential") {
			t.Errorf("a confined command must not reach motita's logins:\n%s", joined)
		}
		if !strings.Contains(joined, "GIT_TERMINAL_PROMPT=0") {
			t.Errorf("git must still fail instead of prompting:\n%s", joined)
		}
	}
}

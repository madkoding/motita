package sandbox

import (
	"sort"
	"strings"

	"github.com/madkoding/motita/internal/redact"
)

// minMaskedSecret is the shortest value masked in output: a one or two character value would turn
// every line of a listing into [REDACTED].
const minMaskedSecret = 4

// secrets is what the person keeps for the agent right now.
func (s *Sandbox) secrets() map[string]string {
	if s == nil || s.op.Secrets == nil {
		return nil
	}
	return s.op.Secrets()
}

// secretEnv is the NAME=value pair of each secret, in name order.
func (s *Sandbox) secretEnv() []string {
	secrets := s.secrets()
	names := s.SecretNames()
	env := make([]string, 0, len(names))
	for _, n := range names {
		env = append(env, n+"="+secrets[n])
	}
	return env
}

// SecretNames are the names of the variables every command gets, never their values, sorted: what
// the agent is told it can use.
func (s *Sandbox) SecretNames() []string {
	secrets := s.secrets()
	names := make([]string, 0, len(secrets))
	for n := range secrets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// maskSecrets replaces every secret's value in a command's output, so a command that prints its
// environment does not hand the value to the model provider or the saved transcript.
func (s *Sandbox) maskSecrets(out string) string {
	var values []string
	for _, v := range s.secrets() {
		if len(v) >= minMaskedSecret && strings.Contains(out, v) {
			values = append(values, v)
		}
	}
	return redact.Values(out, values)
}

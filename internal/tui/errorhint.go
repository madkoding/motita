package tui

import (
	"strings"

	"github.com/madkoding/motita/internal/config"
)

// errorText is how a failed turn is reported: the error as it happened, and - when it is one of the
// failures a newcomer meets first - one sentence on what to do about it.
//
// The raw error stays first and whole. It is the evidence, and a hint that replaced it would be
// the interface guessing in place of the program reporting. The hint only adds the step the error
// leaves out: "connection refused" is accurate, and it does not say that the Ollama server is not
// running or which command starts it.
func (t *TUI) errorText(err error) string {
	text := t.trf("error: %v", err)
	if hint := t.errorHint(err); hint != "" {
		// The hints are fixed sentences, translated as they are; the one built around the
		// provider's name is translated where it is built.
		text += "\n\n" + t.tr(hint)
	}
	return text
}

// errorHint matches the error against the failures that have a known fix. It returns "" for
// anything else: a hint that is wrong is worse than none.
func (t *TUI) errorHint(err error) string {
	msg := strings.ToLower(err.Error())
	llmCfg := t.Runner.Config().LLM
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(msg, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("connection refused", "no such host", "i/o timeout", "network is unreachable", "dial tcp"):
		if config.IsSelfHostedOllama(llmCfg) {
			return "hint: motita could not reach your Ollama server. Start it with `ollama serve`, " +
				"then try /models to check the connection."
		}
		return "hint: motita could not reach the provider's server. Check your connection, " +
			"try /models to test the endpoint, or /config to change it."
	case has("already in progress"):
		return "hint: the previous task is still stopping. Wait a moment and send it again."
	case has("key is missing"):
		return t.trf("hint: there is no API key for %s. Type /config to add one.", providerName(llmCfg.Provider))
	case has("authentication failed", "could not read username", "could not read password", "terminal prompts disabled",
		"permission denied (publickey)", "http basic: access denied"):
		return "hint: git could not sign in to the host. Type /git connect to connect GitHub, GitLab or Bitbucket."
	case has("401", "403", "unauthorized", "forbidden", "invalid api key", "invalid_api_key", "incorrect api key"):
		return "hint: the provider refused the key or the login. Type /config to enter a new one."
	case has("429", "rate limit", "quota", "insufficient_quota"):
		return "hint: the provider is limiting requests or the account is out of credit. " +
			"Wait a moment, or switch model with /models."
	case has("404", "model not found", "does not exist", "no such model"):
		return "hint: the model may not exist on this provider. /models lists the ones you can use."
	}
	return ""
}

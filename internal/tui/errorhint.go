package tui

import (
	"fmt"
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
	text := fmt.Sprintf("error: %v", err)
	if hint := t.errorHint(err); hint != "" {
		text += "\n\n" + hint
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
		return "hint: there is no API key for " + providerName(llmCfg.Provider) + ". Type /config to add one."
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

// Package redact masks secrets (API keys, tokens, passwords, private keys) in
// free text before it is written to a log or sent to a model provider.
//
// It is a best-effort pattern scrubber: it recognizes the well-known shapes of
// credentials, not every secret a program can print.
package redact

import "regexp"

// Mask replaces every secret that is found.
const Mask = "[REDACTED]"

// rule is one pattern and what replaces it. A replacement that starts with a
// capture group keeps that prefix (a header name, a URL scheme) readable.
type rule struct {
	re   *regexp.Regexp
	repl string
}

var rules = []rule{
	// PEM private key blocks, whole.
	{regexp.MustCompile(`-----BEGIN[A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END[A-Z0-9 ]*PRIVATE KEY-----`), Mask},
	// Authorization headers: "Authorization: Bearer x", "Authorization: Basic x", and a
	// long bare value. A value made of "$", "{" or "(" is a placeholder (${TOKEN},
	// {{.Key}}, $(cat file)), not a secret, so the value classes stop there: masking it
	// would only make the source the model reads differ from the file on disk.
	{regexp.MustCompile(`(?i)(\bauthorization["']?\s*[:=]\s*["']?(?:bearer|basic|token|bot)\s+)[^\s"'\\,;$(){}<>` + "`" + `]+`), "${1}" + Mask},
	{regexp.MustCompile(`(?i)(\bauthorization["']?\s*[:=]\s*["']?)[^\s"'\\,;$(){}<>` + "`" + `[]{16,}`), "${1}" + Mask},
	// A bearer token outside a header ("Bearer x" in a command or a message).
	{regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + Mask},
	// Anthropic, OpenAI and similar keys: sk-..., sk-ant-..., sk-proj-...
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), Mask},
	// GitHub tokens: ghp_, gho_, ghu_, ghs_, ghr_ and fine-grained github_pat_.
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`), Mask},
	// AWS access key ids.
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), Mask},
	// Google API keys.
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), Mask},
	// Slack tokens: xoxb-, xoxp-, xoxa-, xoxo-, xoxr-, xoxs-.
	{regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), Mask},
	// Credentials in a URL: scheme://user:password@host.
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s:@"'\\]+:[^/\s@"'\\]+@`), "${1}" + Mask + "@"},
	// Secret query parameters: ?token=, &access_token=, &api_key=, &secret=, &password=...
	{regexp.MustCompile(`(?i)((?:^|[?&;])(?:[a-z0-9_.-]*[_-])?(?:token|key|apikey|secret|password|passwd)=)[^&;\s"'\\#<>$(){}` + "`" + `]+`), "${1}" + Mask},
}

// String returns s with every recognized secret replaced by Mask.
func String(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

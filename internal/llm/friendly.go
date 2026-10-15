package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/madkoding/motita/internal/i18n"
)

// The sentences a provider failure is shown with. They are fixed English text so the Spanish
// catalog (internal/i18n/es_llm.go) can translate them wherever they end up: Localize swaps them
// inside any longer message. The provider's own JSON never reaches the reader; it goes to the logs
// (HTTPError.Detail).
const (
	msgBadKey       = "The provider rejected the API key or the login."
	msgNoAccess     = "The key has no permission for this model or action."
	msgQuota        = "The account is out of credit or over its quota."
	msgRateLimit    = "The provider is limiting requests right now."
	msgNoModel      = "The provider does not have this model."
	msgOverloaded   = "The provider is overloaded."
	msgServer       = "The provider had an internal error."
	msgTooLong      = "The conversation is too long for this model."
	msgBadRequest   = "The provider could not accept the request."
	msgUnknown      = "The provider returned an error."
	msgUnreachable  = "Could not reach the provider: check your connection."
	msgTimeout      = "The provider took too long to answer."
	msgTemporaryTip = "Try again in a moment."
)

// friendlyMessages lists every sentence above, for Localize.
var friendlyMessages = []string{
	msgBadKey, msgNoAccess, msgQuota, msgRateLimit, msgNoModel, msgOverloaded, msgServer,
	msgTooLong, msgBadRequest, msgUnknown, msgUnreachable, msgTimeout, msgTemporaryTip,
}

// providerError is the part of a provider's error body that says what went wrong. Each provider
// words it in its own fields: OpenAI and compatibles use code and type, Anthropic type, Gemini
// status; message is free text.
type providerError struct {
	Code    string
	Type    string
	Status  string
	Message string
}

// parseProviderError reads the standard error object of a body: {"error": {...}}, a list of
// them (Gemini streams) or a bare string. Anything else gives the zero value, and the HTTP code
// alone decides.
func parseProviderError(body string) providerError {
	body = strings.TrimSpace(body)
	if body == "" {
		return providerError{}
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		if strings.HasPrefix(body, "<") {
			return providerError{} // an HTML error page says nothing a reader can use
		}
		return providerError{Message: body}
	}
	if len(raw) > 0 && raw[0] == '[' {
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
			return providerError{}
		}
		raw = list[0]
	}
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &env) != nil || len(env.Error) == 0 {
		return providerError{}
	}
	var text string
	if json.Unmarshal(env.Error, &text) == nil {
		return providerError{Message: text}
	}
	var obj struct {
		Code    json.RawMessage `json:"code"`
		Type    string          `json:"type"`
		Status  string          `json:"status"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(env.Error, &obj) != nil {
		return providerError{}
	}
	code := strings.Trim(string(obj.Code), `"`)
	if code == "null" {
		code = ""
	}
	return providerError{Code: code, Type: obj.Type, Status: obj.Status, Message: obj.Message}
}

// has reports whether any of the fields contains one of subs, ignoring case.
func (p providerError) has(subs ...string) bool {
	all := strings.ToLower(p.Code + " " + p.Type + " " + p.Status + " " + p.Message)
	for _, s := range subs {
		if strings.Contains(all, s) {
			return true
		}
	}
	return false
}

// Friendly is the short sentence that says what an HTTP failure means, chosen from the status
// code and the error fields the provider sends, with a readable fallback for anything unknown.
func Friendly(code int, body string) string {
	p := parseProviderError(body)
	switch {
	case p.has("context_length_exceeded", "prompt is too long", "maximum context length",
		"exceeds the context window", "request_too_large", "input token count"):
		return msgTooLong
	case p.has("insufficient_quota", "billing", "credit balance", "exceeded your current quota",
		"payment_required"):
		return msgQuota
	case p.has("invalid_api_key", "authentication_error", "unauthenticated", "api key not valid",
		"incorrect api key", "api_key_invalid"):
		return msgBadKey
	case p.has("model_not_found", "not_found_error", "no such model", "does not exist"):
		return msgNoModel
	case p.has("overloaded"):
		return msgOverloaded
	}
	switch {
	case code == 401:
		return msgBadKey
	case code == 402:
		return msgQuota
	case code == 403:
		return msgNoAccess
	case code == 404:
		return msgNoModel
	case code == 408 || code == 504:
		return msgTimeout
	case code == 413:
		return msgTooLong
	case code == 429 && p.has("resource_exhausted", "quota"):
		return msgQuota
	case code == 429:
		return msgRateLimit
	case code == 503 || code == 529:
		return msgOverloaded
	case code >= 500:
		return msgServer
	case code == 400 || code == 422:
		return msgBadRequest
	}
	return msgUnknown
}

// oneLine is s on a single line, cut to max runes.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// Detail is the technical text of the failure, for the logs and for a client that folds it
// away: the status and the provider's own body.
func (e *HTTPError) Detail() string {
	return fmt.Sprintf("HTTP %d: %s", e.Code, truncate(e.Body, 400))
}

// Error is the readable sentence plus the status code. The body stays out of it: see Detail.
func (e *HTTPError) Error() string {
	msg := Friendly(e.Code, e.Body)
	// Where the sentence alone says little, the provider's own plain-text reason is kept after
	// it: "could not accept the request" is not actionable without "max_tokens is too large".
	if msg == msgBadRequest || msg == msgServer || msg == msgUnknown {
		if why := oneLine(parseProviderError(e.Body).Message, 160); why != "" {
			msg = strings.TrimSuffix(msg, ".") + ": " + why
		}
	}
	if e.Code >= 500 || e.Code == 429 {
		msg += " " + msgTemporaryTip
	}
	return fmt.Sprintf("%s (HTTP %d)", msg, e.Code)
}

// Detail returns the technical text of err: the provider's body for an HTTP failure, the error
// itself otherwise. Logs use it so nothing the reader no longer sees is lost.
func Detail(err error) string {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Detail()
	}
	if err == nil {
		return ""
	}
	var nf *netFailure
	if errors.As(err, &nf) {
		return nf.err.Error()
	}
	return err.Error()
}

// Explain is the readable text of a failed call. A network failure (no connection, DNS, a
// timeout) is named as such; an HTTP failure already reads well; anything else is returned as it is.
func Explain(err error) error {
	if err == nil {
		return nil
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	var ne net.Error
	if errors.As(err, &ne) {
		if ne.Timeout() {
			return &netFailure{msg: msgTimeout, err: err}
		}
		return &netFailure{msg: msgUnreachable, err: err}
	}
	return err
}

// netFailure is a network failure read as a sentence; the transport's own text stays reachable
// through Unwrap and Detail, not through Error.
type netFailure struct {
	msg string
	err error
}

func (n *netFailure) Error() string { return n.msg }
func (n *netFailure) Unwrap() error { return n.err }

// Localize translates the provider-failure sentences inside text into lang; every other word is
// left alone, so it can be applied to any message that may contain one.
func Localize(lang i18n.Lang, text string) string {
	if lang == i18n.EN || text == "" {
		return text
	}
	for _, en := range friendlyMessages {
		if strings.Contains(text, en) {
			text = strings.ReplaceAll(text, en, i18n.T(lang, en))
		}
	}
	return text
}

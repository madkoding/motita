// Package oauth implements the direct-login flows (device code and PKCE) that
// let a user connect to a provider without pasting an API key: the wizard shows
// a URL and a code, the user authorises in their browser, and the resulting
// token is stored for the LLM client to use.
//
// Three providers are supported:
//
//   - Gemini: Google's OAuth2 device flow (POST /device/code → poll /token).
//   - Copilot: GitHub's device flow + Copilot token exchange (two-step).
//   - Anthropic: PKCE authorisation-code flow (browser redirect + manual paste).
//
// The package has no dependency on internal/llm or internal/config: it produces
// tokens, and the caller decides what to do with them. This keeps the transport
// of each flow testable in isolation.
package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Token is what every flow returns: the credential the LLM client sends, when
// it expires, and a refresh token when the provider issues one.
type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	// TokenType is "Bearer" for every provider today, but is kept here so the
	// client does not hardcode it.
	TokenType string
	// Scopes is what the token actually granted, which may be narrower than what
	// was requested.
	Scopes string
}

// IsExpired reports whether the token has passed its expiry. A token with no
// expiry is treated as never expired (some providers issue long-lived tokens).
func (t Token) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(t.ExpiresAt)
}

// DeviceCode is the intermediate state of a device flow: the code the user
// enters and the device code the client polls with.
type DeviceCode struct {
	DeviceCode      string
	UserCode        string
	VerificationURL string
	ExpiresIn       int
	Interval        int
}

// HTTPClient is the interface every flow uses. It is exported so tests can
// inject a fake transport; production uses http.DefaultClient with a timeout.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// defaultClient returns an HTTP client with a reasonable timeout. Tests can
// replace it via SetDefaultClient to avoid network calls.
var defaultClientFn = func() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

func defaultClient() *http.Client {
	return defaultClientFn()
}

// SetDefaultClient replaces the default HTTP client factory. It is intended for
// tests that need to inject a fake transport without passing a client to every
// call. The restore function reverts to the original.
func SetDefaultClient(fn func() *http.Client) (restore func()) {
	old := defaultClientFn
	defaultClientFn = fn
	return func() { defaultClientFn = old }
}

// postForm sends a form-encoded POST and decodes the JSON response into dest.
func postForm(ctx context.Context, client HTTPClient, url string, form url.Values, dest any) error {
	if client == nil {
		client = defaultClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return doJSON(client, req, dest)
}

// postJSON sends a JSON POST and decodes the response.
func postJSON(ctx context.Context, client HTTPClient, url string, body any, headers map[string]string, dest any) error {
	if client == nil {
		client = defaultClient()
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return doJSON(client, req, dest)
}

// getJSON sends a GET and decodes the JSON response.
func getJSON(ctx context.Context, client HTTPClient, url string, headers map[string]string, dest any) error {
	if client == nil {
		client = defaultClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return doJSON(client, req, dest)
}

// doJSON executes a request and decodes the body. The body is decoded into
// dest regardless of the status code: OAuth device-flow polls return 400 with a
// JSON body that the caller must inspect ({"error":"authorization_pending"}).
// When the status is not 2xx AND dest did not capture a meaningful error field,
// a generic error is returned so non-OAuth callers still see the failure.
func doJSON(client HTTPClient, req *http.Request, dest any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if dest != nil {
		if jerr := json.Unmarshal(body, dest); jerr != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return fmt.Errorf("could not decode response: %w", jerr)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// ErrAuthorizationPending is returned by a device-flow poll when the user has
// not yet entered the code. The caller should wait and poll again.
var ErrAuthorizationPending = errors.New("authorization pending: the user has not yet entered the code")

// ErrSlowDown is returned when the server says the client is polling too fast.
var ErrSlowDown = errors.New("slow down: poll less frequently")

// ErrAccessDenied is returned when the user denied the authorisation.
var ErrAccessDenied = errors.New("access denied: the user refused the authorisation")

// ErrExpiredToken is returned when the device code expired before the user
// completed the authorisation.
var ErrExpiredToken = errors.New("the device code expired before the authorisation was completed")

// pollError interprets the error field of a device-flow token response and
// returns the corresponding sentinel error, or nil when there is no error.
func pollError(errStr string) error {
	switch errStr {
	case "":
		return nil
	case "authorization_pending":
		return ErrAuthorizationPending
	case "slow_down":
		return ErrSlowDown
	case "access_denied":
		return ErrAccessDenied
	case "expired_token":
		return ErrExpiredToken
	default:
		return fmt.Errorf("unexpected error from the server: %s", errStr)
	}
}

package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// --- RandReader injection for generatePKCE error paths -----------------------

// failingReader is an io.Reader whose Read always returns an error.
type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) { return 0, errors.New("entropy depleted") }

// shortReader returns `n` zero bytes then fails, so io.ReadFull fails on the
// second call (state generation) after the first (verifier) succeeds.
type shortReader struct {
	n    int
	read int
}

func (r *shortReader) Read(p []byte) (int, error) {
	if r.read >= r.n {
		return 0, errors.New("entropy depleted")
	}
	nn := len(p)
	if r.read+nn > r.n {
		nn = r.n - r.read
	}
	r.read += nn
	return nn, nil
}

func TestGeneratePKCEVerifierError(t *testing.T) {
	old := RandReader
	RandReader = failingReader{}
	defer func() { RandReader = old }()
	if _, err := generatePKCE(); err == nil {
		t.Error("expected error from failing RandReader on verifier")
	}
}

func TestGeneratePKCEStateError(t *testing.T) {
	old := RandReader
	RandReader = &shortReader{n: 32} // verifier (32 bytes) succeeds, state (16 bytes) fails
	defer func() { RandReader = old }()
	if _, err := generatePKCE(); err == nil {
		t.Error("expected error from failing RandReader on state")
	}
}

func TestAnthropicAuthorizeURLError(t *testing.T) {
	old := RandReader
	RandReader = failingReader{}
	defer func() { RandReader = old }()
	if _, _, err := AnthropicAuthorizeURL(AnthropicConfig{}); err == nil {
		t.Error("expected error from generatePKCE failure")
	}
}

// --- defaultClient & SetDefaultClient ---------------------------------------

func TestDefaultClient(t *testing.T) {
	c := defaultClient()
	if c == nil {
		t.Fatal("defaultClient returned nil")
	}
	if c.Timeout <= 0 {
		t.Errorf("Timeout = %v, want > 0", c.Timeout)
	}
}

func TestSetDefaultClient(t *testing.T) {
	fake := &http.Client{Timeout: 1}
	restore := SetDefaultClient(func() *http.Client { return fake })
	defer restore()
	if got := defaultClient(); got != fake {
		t.Error("defaultClient did not return the injected client")
	}
	restore()
	if got := defaultClient(); got == fake {
		t.Error("defaultClient should not return fake after restore")
	}
}

// --- doJSON edge cases -------------------------------------------------------

func TestDoJSONClientError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err := doJSON(client, req, &map[string]string{}); err == nil {
		t.Error("expected error from client.Do failure")
	}
}

// errBody is an io.ReadCloser whose Read always returns an error.
type errBody struct{}

func (errBody) Read(p []byte) (int, error) { return 0, errors.New("read error") }
func (errBody) Close() error               { return nil }

func TestDoJSONReadError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       errBody{},
				Header:     make(http.Header),
			}, nil
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err := doJSON(client, req, &map[string]string{}); err == nil {
		t.Error("expected error from ReadAll failure")
	}
}

func TestDoJSONNon2xxWithNilDest(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "internal server error"), nil
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err := doJSON(client, req, nil); err == nil {
		t.Error("expected error for non-2xx response with nil dest")
	}
}

func TestDoJSONNon2xxWithDest(t *testing.T) {
	// Non-2xx with a dest: the body is unmarshaled first, then the HTTP error
	// is returned. This is how device-flow poll responses work (400 with JSON).
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(strings.NewReader(`{"error":"authorization_pending"}`)),
				Header:     make(http.Header),
			}, nil
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	var dest map[string]string
	if err := doJSON(client, req, &dest); err == nil {
		t.Error("expected HTTP error for 400 response")
	}
}

func TestDoJSONSuccessNilDest(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			return jsonResp(map[string]string{"ok": "true"}), nil
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err := doJSON(client, req, nil); err != nil {
		t.Errorf("expected nil error for nil dest, got %v", err)
	}
}

func TestDoJSONUnmarshalError2xx(t *testing.T) {
	// 2xx response with invalid JSON and non-nil dest → unmarshal error.
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("{invalid json")),
				Header:     make(http.Header),
			}, nil
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err := doJSON(client, req, &map[string]string{}); err == nil {
		t.Error("expected error for invalid JSON on 2xx response")
	}
}

func TestDoJSONUnmarshalErrorNon2xx(t *testing.T) {
	// Non-2xx response with invalid JSON and non-nil dest: unmarshal fails but
	// since status is not 2xx, the unmarshal error is suppressed and the HTTP
	// error is returned instead.
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(strings.NewReader("{invalid json")),
				Header:     make(http.Header),
			}, nil
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err := doJSON(client, req, &map[string]string{}); err == nil {
		t.Error("expected HTTP error for non-2xx with invalid JSON")
	}
}

func TestDoJSONUnmarshalNon2xxValidJSON(t *testing.T) {
	// Non-2xx response with valid JSON: dest is populated, then HTTP error
	// is returned. This exercises the path where unmarshal succeeds on a
	// non-2xx response.
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(strings.NewReader(`{"error":"bad_request"}`)),
				Header:     make(http.Header),
			}, nil
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	var dest map[string]string
	err := doJSON(client, req, &dest)
	if err == nil {
		t.Error("expected HTTP error for 400")
	}
	if dest["error"] != "bad_request" {
		t.Errorf("dest error = %q, want bad_request", dest["error"])
	}
}

// --- postForm / postJSON / getJSON nil-client and bad-URL paths ---------------

func TestPostFormNilClient(t *testing.T) {
	// Nil client triggers defaultClient(); bad URL triggers NewRequest error.
	if err := postForm(context.Background(), nil, "http://\x00", url.Values{}, nil); err == nil {
		t.Error("expected error for bad URL with nil client")
	}
}

func TestPostJSONNilClient(t *testing.T) {
	if err := postJSON(context.Background(), nil, "http://\x00", map[string]string{}, nil, nil); err == nil {
		t.Error("expected error for bad URL with nil client")
	}
}

func TestPostJSONMarshalError(t *testing.T) {
	// A channel cannot be JSON-marshaled.
	if err := postJSON(context.Background(), newFakeClient(nil), "http://example.com", make(chan int), nil, nil); err == nil {
		t.Error("expected error for unmarshalable body")
	}
}

func TestGetJSONNilClient(t *testing.T) {
	if err := getJSON(context.Background(), nil, "http://\x00", nil, nil); err == nil {
		t.Error("expected error for bad URL with nil client")
	}
}

// --- postForm/postJSON/getJSON with headers and success ----------------------

func TestPostFormSuccessNilDest(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			if ct := req.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
				t.Errorf("Content-Type = %q", ct)
			}
			return jsonResp(map[string]string{"ok": "true"}), nil
		},
	})
	if err := postForm(context.Background(), client, "http://example.com", url.Values{"k": {"v"}}, nil); err != nil {
		t.Errorf("postForm: %v", err)
	}
}

func TestPostJSONSuccessWithHeaders(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("X-Custom") != "val" {
				t.Errorf("X-Custom = %q", req.Header.Get("X-Custom"))
			}
			return jsonResp(map[string]string{"ok": "true"}), nil
		},
	})
	var dest map[string]string
	if err := postJSON(context.Background(), client, "http://example.com", map[string]string{"k": "v"}, map[string]string{"X-Custom": "val"}, &dest); err != nil {
		t.Errorf("postJSON: %v", err)
	}
}

func TestGetJSONSuccessWithHeaders(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"example.com": func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("X-Custom") != "val" {
				t.Errorf("X-Custom = %q", req.Header.Get("X-Custom"))
			}
			return jsonResp(map[string]string{"ok": "true"}), nil
		},
	})
	var dest map[string]string
	if err := getJSON(context.Background(), client, "http://example.com", map[string]string{"X-Custom": "val"}, &dest); err != nil {
		t.Errorf("getJSON: %v", err)
	}
}

// --- Anthropic error paths ---------------------------------------------------

func TestAnthropicExchangeCodeError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"console.anthropic.com/v1/oauth/token": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := AnthropicExchangeCode(context.Background(), client, AnthropicConfig{}, "code", pkceParams{}); err == nil {
		t.Error("expected error from server failure")
	}
}

func TestAnthropicRefreshTokenError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"console.anthropic.com/v1/oauth/token": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := AnthropicRefreshToken(context.Background(), client, AnthropicConfig{}, "old-refresh"); err == nil {
		t.Error("expected error from server failure")
	}
}

// --- Gemini error paths ------------------------------------------------------

func TestGeminiRequestDeviceCodeError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2.googleapis.com/device/code": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := GeminiRequestDeviceCode(context.Background(), client, GeminiConfig{}); err == nil {
		t.Error("expected error from server failure")
	}
}

func TestGeminiPollTokenHTTPError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2.googleapis.com/token": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := GeminiPollToken(context.Background(), client, GeminiConfig{}, DeviceCode{}); err == nil {
		t.Error("expected error from server failure")
	}
}

func TestGeminiPollTokenHTTPErrNoPollError(t *testing.T) {
	// 400 with valid JSON but no "error" field: postForm returns an HTTP error,
	// pollError("") returns nil, so the original HTTP error is returned.
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2.googleapis.com/token": func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(strings.NewReader(`{"access_token":""}`)),
				Header:     make(http.Header),
			}, nil
		},
	})
	if _, err := GeminiPollToken(context.Background(), client, GeminiConfig{}, DeviceCode{}); err == nil {
		t.Error("expected HTTP error for 400 with no error field")
	}
}

func TestGeminiPollToken400WithPollError(t *testing.T) {
	// 400 with JSON error field "authorization_pending": postForm returns an
	// HTTP error, but pollError("authorization_pending") returns the sentinel,
	// so the sentinel error is returned instead of the HTTP error.
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2.googleapis.com/token": func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(strings.NewReader(`{"error":"authorization_pending"}`)),
				Header:     make(http.Header),
			}, nil
		},
	})
	if _, err := GeminiPollToken(context.Background(), client, GeminiConfig{}, DeviceCode{}); err != ErrAuthorizationPending {
		t.Errorf("err = %v, want ErrAuthorizationPending", err)
	}
}

func TestGeminiPollTokenUnknownError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2.googleapis.com/token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(geminiTokenResponse{Error: "unknown_thing"}), nil
		},
	})
	if _, err := GeminiPollToken(context.Background(), client, GeminiConfig{}, DeviceCode{}); err == nil {
		t.Error("expected error for unknown error string")
	}
}

func TestGeminiRefreshTokenHTTPError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2.googleapis.com/token": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := GeminiRefreshToken(context.Background(), client, GeminiConfig{}, "old-refresh"); err == nil {
		t.Error("expected error from server failure")
	}
}

func TestGeminiRefreshTokenErrorResponse(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2.googleapis.com/token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(geminiTokenResponse{Error: "invalid_grant"}), nil
		},
	})
	if _, err := GeminiRefreshToken(context.Background(), client, GeminiConfig{}, "old-refresh"); err == nil {
		t.Error("expected error for invalid_grant")
	}
}

// --- tokenFromResponse edge cases --------------------------------------------

func TestTokenFromResponseEmptyTokenType(t *testing.T) {
	tok := tokenFromResponse("access", "refresh", "", "scope", 3600)
	if tok.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want Bearer", tok.TokenType)
	}
}

func TestTokenFromResponseZeroExpiry(t *testing.T) {
	tok := tokenFromResponse("access", "refresh", "Bearer", "scope", 0)
	if !tok.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt should be zero, got %v", tok.ExpiresAt)
	}
}

// --- Copilot error paths -----------------------------------------------------

func TestCopilotRequestDeviceCodeError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/device/code": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := CopilotRequestDeviceCode(context.Background(), client, CopilotConfig{}); err == nil {
		t.Error("expected error from server failure")
	}
}

func TestCopilotPollAccessTokenHTTPError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := CopilotPollAccessToken(context.Background(), client, CopilotConfig{}, DeviceCode{}); err == nil {
		t.Error("expected error from server failure")
	}
}

func TestCopilotPollAccessTokenSlowDown(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{Error: "slow_down"}), nil
		},
	})
	if _, err := CopilotPollAccessToken(context.Background(), client, CopilotConfig{}, DeviceCode{}); err != ErrSlowDown {
		t.Errorf("err = %v, want ErrSlowDown", err)
	}
}

func TestCopilotPollAccessTokenHTTPErrNoPollError(t *testing.T) {
	// 400 with valid JSON but no "error" field: postJSON returns an HTTP error,
	// pollError("") returns nil, so the original HTTP error is returned.
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(strings.NewReader(`{"access_token":""}`)),
				Header:     make(http.Header),
			}, nil
		},
	})
	if _, err := CopilotPollAccessToken(context.Background(), client, CopilotConfig{}, DeviceCode{}); err == nil {
		t.Error("expected HTTP error for 400 with no error field")
	}
}

func TestCopilotPollAccessToken400WithPollError(t *testing.T) {
	// 400 with JSON error field "authorization_pending": postJSON returns an
	// HTTP error, but pollError("authorization_pending") returns the sentinel,
	// so the sentinel error is returned instead of the HTTP error.
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 400,
				Body:       io.NopCloser(strings.NewReader(`{"error":"authorization_pending"}`)),
				Header:     make(http.Header),
			}, nil
		},
	})
	if _, err := CopilotPollAccessToken(context.Background(), client, CopilotConfig{}, DeviceCode{}); err != ErrAuthorizationPending {
		t.Errorf("err = %v, want ErrAuthorizationPending", err)
	}
}

func TestCopilotPollAccessTokenExpiredToken(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{Error: "expired_token"}), nil
		},
	})
	if _, err := CopilotPollAccessToken(context.Background(), client, CopilotConfig{}, DeviceCode{}); err != ErrExpiredToken {
		t.Errorf("err = %v, want ErrExpiredToken", err)
	}
}

func TestCopilotPollAccessTokenUnknownError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{Error: "unknown_thing"}), nil
		},
	})
	if _, err := CopilotPollAccessToken(context.Background(), client, CopilotConfig{}, DeviceCode{}); err == nil {
		t.Error("expected error for unknown error string")
	}
}

func TestCopilotExchangeTokenError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"copilot_internal/v2/token": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := CopilotExchangeToken(context.Background(), client, "gho_test"); err == nil {
		t.Error("expected error from server failure")
	}
}

func TestCopilotExchangeTokenEmptyAPI(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"copilot_internal/v2/token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotTokenResponse{
				Token:     "tid=test",
				ExpiresAt: 1234567890,
			}), nil
		},
	})
	ct, err := CopilotExchangeToken(context.Background(), client, "gho_test")
	if err != nil {
		t.Fatalf("CopilotExchangeToken: %v", err)
	}
	if ct.APIBaseURL != "https://api.individual.githubcopilot.com" {
		t.Errorf("APIBaseURL = %q, want default", ct.APIBaseURL)
	}
}

func TestCopilotRefresh(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"copilot_internal/v2/token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotTokenResponse{
				Token:     "tid=test",
				ExpiresAt: 1234567890,
				Endpoints: struct {
					API string `json:"api"`
				}{API: "https://api.individual.githubcopilot.com"},
			}), nil
		},
	})
	ct, err := CopilotRefresh(context.Background(), client, "gho_test")
	if err != nil {
		t.Fatalf("CopilotRefresh: %v", err)
	}
	if ct.Token != "tid=test" {
		t.Errorf("Token = %q", ct.Token)
	}
}

// --- CopilotAuthResult.MarshalJSON -------------------------------------------

func TestCopilotAuthResultMarshalJSON(t *testing.T) {
	r := CopilotAuthResult{
		GitHubToken: "gho_testtoken",
		CopilotToken: CopilotToken{
			APIBaseURL: "https://api.individual.githubcopilot.com",
		},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var store CopilotStoreJSON
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if store.GitHubToken != "gho_testtoken" {
		t.Errorf("GitHubToken = %q", store.GitHubToken)
	}
	if store.APIBaseURL != "https://api.individual.githubcopilot.com" {
		t.Errorf("APIBaseURL = %q", store.APIBaseURL)
	}
}

// --- CopilotFullFlow tests ---------------------------------------------------

func TestCopilotFullFlowSuccess(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/device/code": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotDeviceCodeResponse{
				DeviceCode:      "dc123",
				UserCode:        "ABCD-1234",
				VerificationURI: "https://github.com/login/device",
				ExpiresIn:       900,
				Interval:        0, // covers interval <= 0 → 5
			}), nil
		},
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{
				AccessToken: "gho_testtoken",
				TokenType:   "bearer",
			}), nil
		},
		"copilot_internal/v2/token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotTokenResponse{
				Token:     "tid=test;exp=1234567890",
				ExpiresAt: 1234567890,
				Endpoints: struct {
					API string `json:"api"`
				}{API: "https://api.individual.githubcopilot.com"},
			}), nil
		},
	})
	var codeShown bool
	var pollCount int32
	result, err := CopilotFullFlow(context.Background(), client, CopilotConfig{},
		func(DeviceCode) { codeShown = true },
		func() { atomic.AddInt32(&pollCount, 1) },
	)
	if err != nil {
		t.Fatalf("CopilotFullFlow: %v", err)
	}
	if !codeShown {
		t.Error("onCode callback was not called")
	}
	if atomic.LoadInt32(&pollCount) < 1 {
		t.Error("onPoll callback was not called")
	}
	if result.GitHubToken != "gho_testtoken" {
		t.Errorf("GitHubToken = %q", result.GitHubToken)
	}
	if result.CopilotToken.Token != "tid=test;exp=1234567890" {
		t.Errorf("CopilotToken = %q", result.CopilotToken.Token)
	}
}

func TestCopilotFullFlowDeviceCodeError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/device/code": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := CopilotFullFlow(context.Background(), client, CopilotConfig{}, nil, nil); err == nil {
		t.Error("expected error from device code failure")
	}
}

func TestCopilotFullFlowAccessDenied(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/device/code": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotDeviceCodeResponse{
				DeviceCode: "dc123",
				Interval:   1,
			}), nil
		},
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{Error: "access_denied"}), nil
		},
	})
	if _, err := CopilotFullFlow(context.Background(), client, CopilotConfig{}, nil, nil); err == nil {
		t.Error("expected error from access_denied")
	}
}

func TestCopilotFullFlowSlowDown(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/device/code": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotDeviceCodeResponse{
				DeviceCode: "dc123",
				Interval:   1,
			}), nil
		},
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{Error: "slow_down"}), nil
		},
	})
	if _, err := CopilotFullFlow(context.Background(), client, CopilotConfig{}, nil, nil); err == nil {
		t.Error("expected error from slow_down")
	}
}

func TestCopilotFullFlowContextCancelled(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/device/code": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotDeviceCodeResponse{
				DeviceCode: "dc123",
				Interval:   1,
			}), nil
		},
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{Error: "authorization_pending"}), nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled so the select picks ctx.Done() immediately
	if _, err := CopilotFullFlow(ctx, client, CopilotConfig{}, nil, nil); err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestCopilotFullFlowExchangeError(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/device/code": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotDeviceCodeResponse{
				DeviceCode: "dc123",
				Interval:   1,
			}), nil
		},
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{AccessToken: "gho_testtoken"}), nil
		},
		"copilot_internal/v2/token": func(req *http.Request) (*http.Response, error) {
			return errResp(500, "server error"), nil
		},
	})
	if _, err := CopilotFullFlow(context.Background(), client, CopilotConfig{}, nil, nil); err == nil {
		t.Error("expected error from exchange failure")
	}
}

func TestCopilotFullFlowPendingThenSuccess(t *testing.T) {
	var pollCount int32
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/device/code": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotDeviceCodeResponse{
				DeviceCode: "dc123",
				Interval:   1, // 1-second wait between polls
			}), nil
		},
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			if atomic.AddInt32(&pollCount, 1) == 1 {
				return jsonResp(copilotAccessTokenResponse{Error: "authorization_pending"}), nil
			}
			return jsonResp(copilotAccessTokenResponse{AccessToken: "gho_testtoken"}), nil
		},
		"copilot_internal/v2/token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotTokenResponse{
				Token:     "tid=test",
				ExpiresAt: 1234567890,
				Endpoints: struct {
					API string `json:"api"`
				}{API: "https://api.individual.githubcopilot.com"},
			}), nil
		},
	})
	result, err := CopilotFullFlow(context.Background(), client, CopilotConfig{}, nil, nil)
	if err != nil {
		t.Fatalf("CopilotFullFlow: %v", err)
	}
	if result.GitHubToken != "gho_testtoken" {
		t.Errorf("GitHubToken = %q", result.GitHubToken)
	}
}

// --- pollError path on 400 responses (access_denied) ------------------------

// TestGeminiPollTokenAccessDeniedOn400: a 400 with {"error":"access_denied"}
// must return ErrAccessDenied via the pollError path inside GeminiPollToken.
func TestGeminiPollTokenAccessDeniedOn400(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2.googleapis.com/token": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":"access_denied"}`)), Header: make(http.Header)}, nil
		},
	})
	_, err := GeminiPollToken(context.Background(), client, GeminiConfig{}, DeviceCode{DeviceCode: "dc"})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("err = %v, want ErrAccessDenied", err)
	}
}

// TestCopilotPollAccessTokenAccessDeniedOn400: a 400 with
// {"error":"access_denied"} must return ErrAccessDenied via the pollError path.
func TestCopilotPollAccessTokenAccessDeniedOn400(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/oauth/access_token": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":"access_denied"}`)), Header: make(http.Header)}, nil
		},
	})
	_, err := CopilotPollAccessToken(context.Background(), client, CopilotConfig{}, DeviceCode{DeviceCode: "dc"})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("err = %v, want ErrAccessDenied", err)
	}
}

// --- Gemini default client ID/secret functions -------------------------------

func TestGeminiDefaultClientID(t *testing.T) {
	id := GeminiDefaultClientID()
	if id == "" {
		t.Fatal("expected non-empty client ID")
	}
	if !strings.Contains(id, "apps.googleusercontent.com") {
		t.Errorf("client ID does not look like a Google OAuth ID: %q", id)
	}
}

func TestGeminiDefaultClientIDFromEnv(t *testing.T) {
	t.Setenv("MOTITA_GEMINI_CLIENT_ID", "env-client-id")
	if got := GeminiDefaultClientID(); got != "env-client-id" {
		t.Errorf("got = %q, want env-client-id", got)
	}
}

func TestGeminiDefaultClientSecret(t *testing.T) {
	secret := GeminiDefaultClientSecret()
	if secret == "" {
		t.Fatal("expected non-empty client secret")
	}
	if !strings.HasPrefix(secret, "GOCSPX") {
		t.Errorf("client secret does not look like a Google OAuth secret: %q", secret)
	}
}

func TestGeminiDefaultClientSecretFromEnv(t *testing.T) {
	t.Setenv("MOTITA_GEMINI_CLIENT_SECRET", "env-secret")
	if got := GeminiDefaultClientSecret(); got != "env-secret" {
		t.Errorf("got = %q, want env-secret", got)
	}
}

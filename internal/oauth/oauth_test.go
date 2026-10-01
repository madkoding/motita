package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeTransport lets tests control HTTP responses by matching on URL.
type fakeTransport struct {
	responses map[string]func(*http.Request) (*http.Response, error)
}

func (t *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	handler, ok := t.responses[req.URL.String()]
	if !ok {
		// Try by path suffix for flexibility.
		for pattern, h := range t.responses {
			if strings.HasSuffix(req.URL.String(), pattern) || strings.Contains(req.URL.String(), pattern) {
				handler = h
				break
			}
		}
	}
	if handler == nil {
		return &http.Response{
			StatusCode: 404,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
		}, nil
	}
	return handler(req)
}

func jsonResp(v any) *http.Response {
	data, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(data))),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func errResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func newFakeClient(responses map[string]func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: &fakeTransport{responses: responses}}
}

// --- Gemini tests ------------------------------------------------------------

// --- Copilot tests -----------------------------------------------------------

func TestCopilotRequestDeviceCode(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/device/code": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotDeviceCodeResponse{
				DeviceCode:      "dc123",
				UserCode:        "ABCD-1234",
				VerificationURI: "https://github.com/login/device",
				ExpiresIn:       900,
				Interval:        5,
			}), nil
		},
	})
	dc, err := CopilotRequestDeviceCode(context.Background(), client, CopilotConfig{})
	if err != nil {
		t.Fatalf("CopilotRequestDeviceCode: %v", err)
	}
	if dc.UserCode != "ABCD-1234" {
		t.Errorf("UserCode = %q", dc.UserCode)
	}
	if dc.VerificationURL != "https://github.com/login/device" {
		t.Errorf("VerificationURL = %q", dc.VerificationURL)
	}
}

func TestCopilotPollAccessTokenSuccess(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{
				AccessToken: "gho_testtoken",
				TokenType:   "bearer",
				Scope:       "read:user",
			}), nil
		},
	})
	token, err := CopilotPollAccessToken(context.Background(), client, CopilotConfig{}, DeviceCode{DeviceCode: "dc"})
	if err != nil {
		t.Fatalf("CopilotPollAccessToken: %v", err)
	}
	if token != "gho_testtoken" {
		t.Errorf("token = %q", token)
	}
}

func TestCopilotPollAccessTokenPending(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"github.com/login/oauth/access_token": func(req *http.Request) (*http.Response, error) {
			return jsonResp(copilotAccessTokenResponse{Error: "authorization_pending"}), nil
		},
	})
	_, err := CopilotPollAccessToken(context.Background(), client, CopilotConfig{}, DeviceCode{})
	if err != ErrAuthorizationPending {
		t.Errorf("err = %v, want ErrAuthorizationPending", err)
	}
}

func TestCopilotExchangeToken(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"copilot_internal/v2/token": func(req *http.Request) (*http.Response, error) {
			// Verify the Authorization header uses "token" not "Bearer".
			if auth := req.Header.Get("Authorization"); !strings.HasPrefix(auth, "token ") {
				t.Errorf("Authorization = %q, want 'token gho_...'", auth)
			}
			return jsonResp(copilotTokenResponse{
				Token:     "tid=test;exp=1234567890",
				ExpiresAt: 1234567890,
				Endpoints: struct {
					API string `json:"api"`
				}{API: "https://api.individual.githubcopilot.com"},
				ChatEnabled: true,
			}), nil
		},
	})
	ct, err := CopilotExchangeToken(context.Background(), client, "gho_testtoken")
	if err != nil {
		t.Fatalf("CopilotExchangeToken: %v", err)
	}
	if ct.Token != "tid=test;exp=1234567890" {
		t.Errorf("Token = %q", ct.Token)
	}
	if ct.APIBaseURL != "https://api.individual.githubcopilot.com" {
		t.Errorf("APIBaseURL = %q", ct.APIBaseURL)
	}
	if ct.ChatURL() != "https://api.individual.githubcopilot.com/chat/completions" {
		t.Errorf("ChatURL = %q", ct.ChatURL())
	}
}

func TestCopilotRequiredHeaders(t *testing.T) {
	h := CopilotRequiredHeaders("test-tid")
	if h["Authorization"] != "Bearer test-tid" {
		t.Errorf("Authorization = %q", h["Authorization"])
	}
	if h["Copilot-Integration-Id"] != "vscode-chat" {
		t.Errorf("Copilot-Integration-Id = %q", h["Copilot-Integration-Id"])
	}
}

// --- Anthropic tests ---------------------------------------------------------

// --- Token tests -------------------------------------------------------------

func TestTokenIsExpired(t *testing.T) {
	// A token with no expiry never expires.
	tok := Token{}
	if tok.IsExpired() {
		t.Error("zero token should not be expired")
	}
	// A token with a past expiry is expired.
	tok2 := Token{ExpiresAt: timeNow().Add(-timeMinute)}
	if !tok2.IsExpired() {
		t.Error("past-expiry token should be expired")
	}
	// A token with a future expiry is not expired.
	tok3 := Token{ExpiresAt: timeNow().Add(timeHour)}
	if tok3.IsExpired() {
		t.Error("future-expiry token should not be expired")
	}
}

// Use package-level vars to avoid importing time in the test helper.
var (
	timeMinute = 60 * time.Second
	timeHour   = time.Hour
)

// timeNow is a thin wrapper so the test does not import time at the top level
// (it already imports it via the RoundTrip above indirectly, but keeping it
// explicit avoids confusion).
func timeNow() time.Time { return time.Now() }

func TestPollError(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"", nil},
		{"authorization_pending", ErrAuthorizationPending},
		{"slow_down", ErrSlowDown},
		{"access_denied", ErrAccessDenied},
		{"expired_token", ErrExpiredToken},
		{"unknown", fmt.Errorf("unexpected")},
	}
	for _, tc := range cases {
		got := pollError(tc.in)
		if tc.want == nil {
			if got != nil {
				t.Errorf("pollError(%q) = %v, want nil", tc.in, got)
			}
			continue
		}
		// For the sentinel errors, compare by identity.
		if got != tc.want && !strings.Contains(got.Error(), "unexpected") {
			t.Errorf("pollError(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

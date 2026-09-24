package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CopilotClientID is the public VS Code Copilot Chat OAuth App client_id. It is
// the only widely-allowed client for the copilot_internal/v2/token endpoint;
// other OAuth apps may receive 404. It is hardcoded in official Copilot
// extensions and is safe to use.
const CopilotClientID = "Iv1.b507a08c87ecfe98"

// CopilotConfig holds the parameters for the GitHub Copilot device flow. Only
// the ClientID is needed, and it defaults to the public Copilot client_id.
type CopilotConfig struct {
	ClientID string
}

// copilotDeviceCodeResponse is the JSON from POST /login/device/code.
type copilotDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// copilotAccessTokenResponse is the JSON from polling /login/oauth/access_token.
type copilotAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
}

// copilotTokenResponse is the JSON from GET /copilot_internal/v2/token.
type copilotTokenResponse struct {
	Token       string `json:"token"`
	ExpiresAt   int64  `json:"expires_at"`
	RefreshIn   int    `json:"refresh_in"`
	ChatEnabled bool   `json:"chat_enabled"`
	Endpoints   struct {
		API string `json:"api"`
	} `json:"endpoints"`
}

// CopilotRequestDeviceCode starts the GitHub device flow.
func CopilotRequestDeviceCode(ctx context.Context, client HTTPClient, cfg CopilotConfig) (DeviceCode, error) {
	if cfg.ClientID == "" {
		cfg.ClientID = CopilotClientID
	}
	body := map[string]string{
		"client_id": cfg.ClientID,
		"scope":     "read:user",
	}
	var resp copilotDeviceCodeResponse
	if err := postJSON(ctx, client, "https://github.com/login/device/code", body, nil, &resp); err != nil {
		return DeviceCode{}, err
	}
	return DeviceCode{
		DeviceCode:      resp.DeviceCode,
		UserCode:        resp.UserCode,
		VerificationURL: resp.VerificationURI,
		ExpiresIn:       resp.ExpiresIn,
		Interval:        resp.Interval,
	}, nil
}

// CopilotPollAccessToken polls GitHub's token endpoint until the user authorises
// the device. The returned string is the long-lived GitHub OAuth access token
// (gho_...), NOT the Copilot session token — the caller must exchange it.
func CopilotPollAccessToken(ctx context.Context, client HTTPClient, cfg CopilotConfig, dc DeviceCode) (string, error) {
	if cfg.ClientID == "" {
		cfg.ClientID = CopilotClientID
	}
	body := map[string]string{
		"client_id":   cfg.ClientID,
		"device_code": dc.DeviceCode,
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
	}
	var resp copilotAccessTokenResponse
	if err := postJSON(ctx, client, "https://github.com/login/oauth/access_token", body, nil, &resp); err != nil {
		// A 400 with a JSON error field is a normal poll response
		// (authorization_pending, slow_down). Check pollError first.
		if perr := pollError(resp.Error); perr != nil {
			return "", perr
		}
		return "", err
	}
	if err := pollError(resp.Error); err != nil {
		return "", err
	}
	return resp.AccessToken, nil
}

// CopilotExchangeToken exchanges the GitHub OAuth access token for a short-lived
// Copilot session token. The CopilotToken carries the token string, the API base
// URL (which varies by plan: Individual, Business, Enterprise), and the expiry.
type CopilotToken struct {
	Token       string
	APIBaseURL  string
	ExpiresAt   time.Time
	ChatEnabled bool
}

// CopilotExchangeToken performs the GET /copilot_internal/v2/token exchange.
func CopilotExchangeToken(ctx context.Context, client HTTPClient, githubToken string) (CopilotToken, error) {
	headers := map[string]string{
		"Authorization":         "token " + githubToken,
		"Editor-Version":        "vscode/1.99.3",
		"Editor-Plugin-Version": "copilot-chat/0.26.7",
		"User-Agent":            "GitHubCopilotChat/0.26.7",
	}
	var resp copilotTokenResponse
	if err := getJSON(ctx, client, "https://api.github.com/copilot_internal/v2/token", headers, &resp); err != nil {
		return CopilotToken{}, err
	}
	apiURL := resp.Endpoints.API
	if apiURL == "" {
		apiURL = "https://api.individual.githubcopilot.com"
	}
	return CopilotToken{
		Token:       resp.Token,
		APIBaseURL:  apiURL,
		ExpiresAt:   time.Unix(resp.ExpiresAt, 0),
		ChatEnabled: resp.ChatEnabled,
	}, nil
}

// CopilotRefresh re-exchanges the GitHub OAuth token for a fresh Copilot session
// token. The gho_ token is long-lived (does not expire unless revoked); only
// the Copilot session token is short-lived (~30 min).
func CopilotRefresh(ctx context.Context, client HTTPClient, githubToken string) (CopilotToken, error) {
	return CopilotExchangeToken(ctx, client, githubToken)
}

// CopilotRequiredHeaders returns the headers that must accompany every chat
// completions request to the Copilot API. The caller adds the Bearer token.
func CopilotRequiredHeaders(copilotToken string) map[string]string {
	return map[string]string{
		"Authorization":          "Bearer " + copilotToken,
		"Content-Type":           "application/json",
		"Editor-Version":         "vscode/1.99.3",
		"Editor-Plugin-Version":  "copilot-chat/0.26.7",
		"Copilot-Integration-Id": "vscode-chat",
		"User-Agent":             "GitHubCopilotChat/0.26.7",
		"Openai-Intent":          "conversation-panel",
		"X-Github-Api-Version":   "2025-01-21",
	}
}

// CopilotChatURL builds the full chat completions URL from the API base URL.
func (t CopilotToken) ChatURL() string {
	return t.APIBaseURL + "/chat/completions"
}

// CopilotModels is the list of models known to be available through the Copilot
// chat API. The API is undocumented and reverse-engineered, so this list may
// not be exhaustive or current.
var CopilotModels = []string{
	"gpt-4o",
	"gpt-4.1",
	"gpt-5-mini",
	"gpt-5.2",
	"gpt-5.2-codex",
	"claude-sonnet-4",
	"claude-sonnet-4.5",
	"gemini-2.5-pro",
}

// CopilotAuthResult is the full result of the Copilot device flow: the
// long-lived GitHub token and the short-lived Copilot session token, plus the
// API base URL to use for chat requests.
type CopilotAuthResult struct {
	GitHubToken  string
	CopilotToken CopilotToken
}

// CopilotFullFlow runs the complete Copilot device flow: device code → poll for
// GitHub token → exchange for Copilot token. The onCode callback is called with
// the device code so the wizard can display it to the user. The onPoll callback
// is called on each poll attempt (useful for showing a spinner).
//
// The interval between polls is taken from the device code response (default 5s).
// The function blocks until the user authorises or the flow expires.
func CopilotFullFlow(ctx context.Context, client HTTPClient, cfg CopilotConfig, onCode func(DeviceCode), onPoll func()) (CopilotAuthResult, error) {
	dc, err := CopilotRequestDeviceCode(ctx, client, cfg)
	if err != nil {
		return CopilotAuthResult{}, err
	}
	if onCode != nil {
		onCode(dc)
	}

	interval := dc.Interval
	if interval <= 0 {
		interval = 5
	}

	var ghToken string
	for {
		if onPoll != nil {
			onPoll()
		}
		ghToken, err = CopilotPollAccessToken(ctx, client, cfg, dc)
		if err == nil {
			break
		}
		if err == ErrSlowDown {
			interval += 5
		}
		if err == ErrAuthorizationPending {
			select {
			case <-ctx.Done():
				return CopilotAuthResult{}, ctx.Err()
			case <-time.After(time.Duration(interval) * time.Second):
			}
			continue
		}
		return CopilotAuthResult{}, err
	}

	ct, err := CopilotExchangeToken(ctx, client, ghToken)
	if err != nil {
		return CopilotAuthResult{}, fmt.Errorf("copilot token exchange failed: %w", err)
	}
	return CopilotAuthResult{GitHubToken: ghToken, CopilotToken: ct}, nil
}

// CopilotStoreJSON is the JSON shape for persisting a Copilot auth result to
// disk. The GitHub token is long-lived and the Copilot token is short-lived,
// so both are stored: the GitHub token is used to refresh the Copilot token.
type CopilotStoreJSON struct {
	GitHubToken string `json:"github_token"`
	APIBaseURL  string `json:"api_base_url"`
}

// MarshalJSON serializes the CopilotAuthResult for storage.
func (r CopilotAuthResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(CopilotStoreJSON{
		GitHubToken: r.GitHubToken,
		APIBaseURL:  r.CopilotToken.APIBaseURL,
	})
}

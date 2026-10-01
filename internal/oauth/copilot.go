package oauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	err := postJSON(ctx, client, "https://github.com/login/oauth/access_token", body, nil, &resp)
	// GitHub answers a pending poll with 200 and an error field; other servers use
	// 400. Either way the error field is what says whether to keep polling.
	if perr := pollError(resp.Error); perr != nil {
		return "", perr
	}
	if err != nil {
		return "", err
	}
	if resp.AccessToken == "" {
		return "", errors.New("GitHub returned no access token")
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
	apiURL := strings.TrimRight(resp.Endpoints.API, "/")
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

// CopilotModels are the ids the Copilot chat API has served; the live list is
// read from <api>/models, which is what the wizard and the web UI offer.
var CopilotModels = []string{
	"gpt-4.1",
	"gpt-4o",
	"gpt-5-mini",
	"claude-sonnet-4",
	"gemini-2.5-pro",
}

// CopilotAuthResult is the full result of the Copilot device flow: the
// long-lived GitHub token and the short-lived Copilot session token, plus the
// API base URL to use for chat requests.
type CopilotAuthResult struct {
	GitHubToken  string
	CopilotToken CopilotToken
}

// Credential converts the result into what is stored: the GitHub token is the
// refresh token (it is what renews the session token), and the session token is
// the access token.
func (r CopilotAuthResult) Credential() Credential {
	return Credential{
		Provider:     "copilot",
		AccessToken:  r.CopilotToken.Token,
		RefreshToken: r.GitHubToken,
		ExpiresAt:    r.CopilotToken.ExpiresAt,
		BaseURL:      r.CopilotToken.APIBaseURL,
	}
}

// CopilotRefreshCredential renews the Copilot session token of a credential
// whose refresh token is the GitHub OAuth token.
func CopilotRefreshCredential(ctx context.Context, client HTTPClient, old Credential) (Credential, error) {
	if old.RefreshToken == "" {
		return Credential{}, errors.New("the Copilot login has no GitHub token: log in again")
	}
	ct, err := CopilotExchangeToken(ctx, client, old.RefreshToken)
	if err != nil {
		return Credential{}, fmt.Errorf("could not renew the Copilot token (is the Copilot subscription active?): %w", err)
	}
	if ct.Token == "" {
		return Credential{}, errors.New("GitHub returned no Copilot token: the account has no active Copilot subscription")
	}
	return CopilotAuthResult{GitHubToken: old.RefreshToken, CopilotToken: ct}.Credential(), nil
}

// CopilotFullFlow runs the complete Copilot device flow: device code → poll for
// GitHub token → exchange for Copilot token. The onCode callback is called with
// the device code so the wizard can display it to the user. The onPoll callback
// is called on each poll attempt (useful for showing a spinner).
//
// The interval between polls is taken from the device code response (default
// 5s) and grows when GitHub answers slow_down. The function blocks until the
// user authorises, denies, or the code expires.
func CopilotFullFlow(ctx context.Context, client HTTPClient, cfg CopilotConfig, onCode func(DeviceCode), onPoll func()) (CopilotAuthResult, error) {
	dc, err := CopilotRequestDeviceCode(ctx, client, cfg)
	if err != nil {
		return CopilotAuthResult{}, err
	}
	if onCode != nil {
		onCode(dc)
	}
	ghToken, err := PollDevice(ctx, dc.Interval, PollSleep, func() (string, error) {
		if onPoll != nil {
			onPoll()
		}
		return CopilotPollAccessToken(ctx, client, cfg, dc)
	})
	if err != nil {
		return CopilotAuthResult{}, err
	}
	ct, err := CopilotExchangeToken(ctx, client, ghToken)
	if err != nil {
		return CopilotAuthResult{}, fmt.Errorf("copilot token exchange failed (is the Copilot subscription active?): %w", err)
	}
	return CopilotAuthResult{GitHubToken: ghToken, CopilotToken: ct}, nil
}

// PollSleep is the pause between device-flow polls. Tests replace it.
var PollSleep = time.After

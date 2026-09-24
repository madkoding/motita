package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
)

// AnthropicClientID is the Claude Code CLI's OAuth client_id, extracted from
// cli.js v2.0.69. It is used for the PKCE authorisation-code flow against
// Anthropic's console.
const AnthropicClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// AnthropicScopes are the scopes required for the Messages API.
const AnthropicScopes = "org:create_api_key user:profile user:inference"

// AnthropicConfig holds the parameters for the Anthropic PKCE flow.
type AnthropicConfig struct {
	ClientID string
}

// AnthropicAuthResult is the result of the Anthropic PKCE flow.
type AnthropicAuthResult struct {
	Token
	// The access token from Anthropic starts with sk-ant-oat01- and is used as
	// a Bearer token in the Authorization header for the Messages API.
}

// pkceParams holds the PKCE code verifier and challenge.
type pkceParams struct {
	Verifier  string
	Challenge string
	State     string
}

// generatePKCE creates a random code verifier and its S256 challenge.
// RandReader is the source of random bytes for PKCE. It is exported so tests
// in other packages can inject a failing reader to cover the error path.
var RandReader = rand.Reader

func generatePKCE() (pkceParams, error) {
	verifierBytes := make([]byte, 32)
	if _, err := io.ReadFull(RandReader, verifierBytes); err != nil {
		return pkceParams{}, fmt.Errorf("could not generate PKCE verifier: %w", err)
	}
	stateBytes := make([]byte, 16)
	if _, err := io.ReadFull(RandReader, stateBytes); err != nil {
		return pkceParams{}, fmt.Errorf("could not generate state: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	sum := sha256.Sum256(verifierBytes)
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	return pkceParams{Verifier: verifier, Challenge: challenge, State: state}, nil
}

// AnthropicAuthorizeURL builds the URL the user opens in their browser. After
// login and consent, Anthropic redirects to a hosted callback page that
// displays the authorisation code for the user to copy back.
func AnthropicAuthorizeURL(cfg AnthropicConfig) (string, pkceParams, error) {
	if cfg.ClientID == "" {
		cfg.ClientID = AnthropicClientID
	}
	pkce, err := generatePKCE()
	if err != nil {
		return "", pkceParams{}, err
	}
	q := url.Values{
		"code":                  {"true"},
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {"https://console.anthropic.com/oauth/code/callback"},
		"scope":                 {AnthropicScopes},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {"S256"},
		"state":                 {pkce.State},
	}
	return "https://claude.ai/oauth/authorize?" + q.Encode(), pkce, nil
}

// anthropicTokenResponse is the JSON from POST /v1/oauth/token.
type anthropicTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

// AnthropicExchangeCode exchanges the authorisation code (pasted by the user
// after visiting the authorize URL) for an access token and a refresh token.
func AnthropicExchangeCode(ctx context.Context, client HTTPClient, cfg AnthropicConfig, code string, pkce pkceParams) (AnthropicAuthResult, error) {
	if cfg.ClientID == "" {
		cfg.ClientID = AnthropicClientID
	}
	body := map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"code_verifier": pkce.Verifier,
		"client_id":     cfg.ClientID,
		"redirect_uri":  "https://console.anthropic.com/oauth/code/callback",
		"state":         pkce.State,
	}
	headers := map[string]string{
		"User-Agent": "anthropic",
	}
	var resp anthropicTokenResponse
	if err := postJSON(ctx, client, "https://console.anthropic.com/v1/oauth/token", body, headers, &resp); err != nil {
		return AnthropicAuthResult{}, err
	}
	return AnthropicAuthResult{
		Token: tokenFromResponse(resp.AccessToken, resp.RefreshToken, resp.TokenType, resp.Scope, resp.ExpiresIn),
	}, nil
}

// AnthropicRefreshToken refreshes an expired access token.
func AnthropicRefreshToken(ctx context.Context, client HTTPClient, cfg AnthropicConfig, refreshToken string) (AnthropicAuthResult, error) {
	if cfg.ClientID == "" {
		cfg.ClientID = AnthropicClientID
	}
	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     cfg.ClientID,
	}
	var resp anthropicTokenResponse
	if err := postJSON(ctx, client, "https://console.anthropic.com/v1/oauth/token", body, nil, &resp); err != nil {
		return AnthropicAuthResult{}, err
	}
	return AnthropicAuthResult{
		Token: tokenFromResponse(resp.AccessToken, resp.RefreshToken, resp.TokenType, resp.Scope, resp.ExpiresIn),
	}, nil
}

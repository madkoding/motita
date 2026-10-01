package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

// Codex: "Sign in with ChatGPT", the login the Codex CLI uses so a ChatGPT
// Plus/Pro/Business plan pays for the coding models instead of API credit.
//
// It is an authorisation-code flow with PKCE against auth.openai.com. The client
// only allows the redirect http://localhost:1455/auth/callback, so the callback
// is received on that port, or pasted by hand when the browser runs elsewhere.
// The resulting access token is a JWT that names the ChatGPT account; requests
// go to the Codex backend (CodexBackendURL) with that account in a header.
const (
	CodexClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	CodexIssuer      = "https://auth.openai.com"
	CodexRedirectURI = "http://localhost:1455/auth/callback"
	CodexListenAddr  = "127.0.0.1:1455"
	CodexScopes      = "openid profile email offline_access"
	// CodexBackendURL is the Responses endpoint a ChatGPT login is billed through.
	CodexBackendURL = "https://chatgpt.com/backend-api/codex"
)

// CodexAuthorizeURL builds the URL the user opens to sign in.
func CodexAuthorizeURL(p PKCE, redirectURI string) string {
	if redirectURI == "" {
		redirectURI = CodexRedirectURI
	}
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {CodexClientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {CodexScopes},
		"code_challenge":             {p.Challenge},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"state":                      {p.State},
		"originator":                 {"codex_cli_rs"},
	}
	return CodexIssuer + "/oauth/authorize?" + q.Encode()
}

type codexTokenResponse struct {
	IDToken          string `json:"id_token"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// CodexExchangeCode trades the authorisation code for tokens.
func CodexExchangeCode(ctx context.Context, client HTTPClient, code, redirectURI string, p PKCE) (Credential, error) {
	if redirectURI == "" {
		redirectURI = CodexRedirectURI
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {CodexClientID},
		"code_verifier": {p.Verifier},
	}
	var resp codexTokenResponse
	if err := postForm(ctx, client, CodexIssuer+"/oauth/token", form, &resp); err != nil {
		return Credential{}, err
	}
	return codexCredential(resp, Credential{})
}

// CodexRefresh renews the access token. OpenAI rotates refresh tokens, so the
// returned credential must replace the stored one.
func CodexRefresh(ctx context.Context, client HTTPClient, old Credential) (Credential, error) {
	if old.RefreshToken == "" {
		return Credential{}, errors.New("the ChatGPT login has no refresh token: log in again")
	}
	body := map[string]string{
		"client_id":     CodexClientID,
		"grant_type":    "refresh_token",
		"refresh_token": old.RefreshToken,
		"scope":         "openid profile email",
	}
	var resp codexTokenResponse
	if err := postJSON(ctx, client, CodexIssuer+"/oauth/token", body, nil, &resp); err != nil {
		return Credential{}, err
	}
	return codexCredential(resp, old)
}

func codexCredential(resp codexTokenResponse, old Credential) (Credential, error) {
	if resp.Error != "" {
		return Credential{}, errors.New("the ChatGPT login failed: " + strings.TrimSpace(resp.Error+" "+resp.ErrorDescription))
	}
	if resp.AccessToken == "" {
		return Credential{}, errors.New("the ChatGPT login returned no access token")
	}
	c := old
	c.Provider = "codex"
	c.AccessToken = resp.AccessToken
	if resp.RefreshToken != "" {
		c.RefreshToken = resp.RefreshToken
	}
	c.ExpiresAt = time.Time{}
	if resp.ExpiresIn > 0 {
		c.ExpiresAt = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	} else if exp := jwtExpiry(resp.AccessToken); !exp.IsZero() {
		c.ExpiresAt = exp
	}
	for _, tok := range []string{resp.IDToken, resp.AccessToken} {
		if id := ChatGPTAccountID(tok); id != "" {
			c.AccountID = id
			break
		}
	}
	if c.AccountID == "" {
		return Credential{}, errors.New("the ChatGPT login names no account: a ChatGPT plan that includes Codex is required")
	}
	return c, nil
}

// jwtClaims decodes the payload of a JWT without verifying it: the token is
// read only to learn what the issuer already told us, never to trust it.
func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}

// ChatGPTAccountID reads the ChatGPT account id out of an OpenAI id or access token.
func ChatGPTAccountID(token string) string {
	claims := jwtClaims(token)
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	id, _ := auth["chatgpt_account_id"].(string)
	return id
}

func jwtExpiry(token string) time.Time {
	if exp, ok := jwtClaims(token)["exp"].(float64); ok && exp > 0 {
		return time.Unix(int64(exp), 0)
	}
	return time.Time{}
}

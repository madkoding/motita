package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Gemini: a Google account login for the Gemini API (generativelanguage).
//
// Google's device flow does not grant the Gemini API scopes, and the public
// client of the Gemini CLI is meant for that CLI and its Code Assist backend, so
// neither is used. A login uses an OAuth client the user owns:
//
//   - their own "Desktop app" OAuth client (MOTITA_GEMINI_CLIENT_ID and
//     MOTITA_GEMINI_CLIENT_SECRET), through an authorisation-code flow with PKCE
//     and a loopback redirect; or
//   - the Application Default Credentials gcloud already wrote
//     (`gcloud auth application-default login`), whose refresh token is copied.
//
// Either way the requests carry a Bearer token and the Google Cloud project that
// pays for them (x-goog-user-project), as the Gemini API's OAuth guide requires.
const (
	GoogleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	GoogleTokenURL = "https://oauth2.googleapis.com/token"
	// GeminiScopes are the scopes the Gemini API documents for OAuth.
	GeminiScopes = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/generative-language.retriever"
)

// GeminiClient is the user's own OAuth client.
type GeminiClient struct {
	ID     string
	Secret string
}

// GeminiClientFromEnv reads the user's OAuth client from the environment.
func GeminiClientFromEnv() (GeminiClient, bool) {
	c := GeminiClient{
		ID:     strings.TrimSpace(os.Getenv("MOTITA_GEMINI_CLIENT_ID")),
		Secret: strings.TrimSpace(os.Getenv("MOTITA_GEMINI_CLIENT_SECRET")),
	}
	return c, c.ID != ""
}

// GeminiAuthorizeURL builds the consent URL for the user's client.
func GeminiAuthorizeURL(client GeminiClient, p PKCE, redirectURI string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {redirectURI},
		"scope":                 {GeminiScopes},
		"code_challenge":        {p.Challenge},
		"code_challenge_method": {"S256"},
		"state":                 {p.State},
		// offline + consent is what makes Google issue a refresh token every time,
		// not only on the first consent.
		"access_type": {"offline"},
		"prompt":      {"consent"},
	}
	return GoogleAuthURL + "?" + q.Encode()
}

type googleTokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// GeminiExchangeCode trades the authorisation code for tokens.
func GeminiExchangeCode(ctx context.Context, hc HTTPClient, client GeminiClient, code, redirectURI string, p PKCE, project string) (Credential, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {client.ID},
		"code_verifier": {p.Verifier},
	}
	if client.Secret != "" {
		form.Set("client_secret", client.Secret)
	}
	var resp googleTokenResponse
	if err := postForm(ctx, hc, GoogleTokenURL, form, &resp); err != nil {
		return Credential{}, err
	}
	return googleCredential(resp, Credential{ClientID: client.ID, ClientSecret: client.Secret, ProjectID: project})
}

// GeminiRefresh renews the access token with the client that issued it.
func GeminiRefresh(ctx context.Context, hc HTTPClient, old Credential) (Credential, error) {
	if old.RefreshToken == "" {
		return Credential{}, errors.New("the Google login has no refresh token: log in again")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {old.RefreshToken},
		"client_id":     {old.ClientID},
	}
	if old.ClientSecret != "" {
		form.Set("client_secret", old.ClientSecret)
	}
	var resp googleTokenResponse
	if err := postForm(ctx, hc, GoogleTokenURL, form, &resp); err != nil {
		return Credential{}, err
	}
	return googleCredential(resp, old)
}

func googleCredential(resp googleTokenResponse, old Credential) (Credential, error) {
	if resp.Error != "" {
		return Credential{}, errors.New("the Google login failed: " + strings.TrimSpace(resp.Error+" "+resp.ErrorDescription))
	}
	if resp.AccessToken == "" {
		return Credential{}, errors.New("the Google login returned no access token")
	}
	c := old
	c.Provider = "gemini"
	c.AccessToken = resp.AccessToken
	if resp.RefreshToken != "" {
		c.RefreshToken = resp.RefreshToken
	}
	c.ExpiresAt = time.Time{}
	if resp.ExpiresIn > 0 {
		c.ExpiresAt = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	}
	return c, nil
}

// ADCPath is where gcloud writes the Application Default Credentials.
func ADCPath(home string) string {
	if p := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		return p
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		if p := filepath.Join(appData, "gcloud", "application_default_credentials.json"); fileExists(p) {
			return p
		}
	}
	return filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// GeminiFromADC turns gcloud's user credentials into a Gemini credential. Only
// "authorized_user" credentials are accepted: a service account key is a
// different kind of secret and is not copied around.
func GeminiFromADC(path, project string) (Credential, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, fmt.Errorf("no gcloud credentials at %s (run `gcloud auth application-default login`): %w", path, err)
	}
	var adc struct {
		Type           string `json:"type"`
		ClientID       string `json:"client_id"`
		ClientSecret   string `json:"client_secret"`
		RefreshToken   string `json:"refresh_token"`
		QuotaProjectID string `json:"quota_project_id"`
	}
	if err := json.Unmarshal(data, &adc); err != nil {
		return Credential{}, fmt.Errorf("the gcloud credentials at %s are unreadable: %w", path, err)
	}
	if adc.Type != "authorized_user" || adc.RefreshToken == "" {
		return Credential{}, fmt.Errorf("the credentials at %s are not a user login (type %q)", path, adc.Type)
	}
	if project == "" {
		project = adc.QuotaProjectID
	}
	return Credential{
		Provider:     "gemini",
		RefreshToken: adc.RefreshToken,
		ClientID:     adc.ClientID,
		ClientSecret: adc.ClientSecret,
		ProjectID:    project,
	}, nil
}

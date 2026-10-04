package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endpoints are the three URLs of a standard OAuth 2.0 server, for the logins
// that are not tied to one provider: the git hosts (GitHub, GitLab, Bitbucket,
// Gitea) all speak the same two grants, a device flow (RFC 8628) and an
// authorisation code with PKCE (RFC 7636), and differ only in where they are.
type Endpoints struct {
	// DeviceURL starts a device flow. Empty means the server has none.
	DeviceURL string
	// AuthorizeURL is where the browser is sent for the code flow.
	AuthorizeURL string
	// TokenURL trades a device code, a code or a refresh token for a token.
	TokenURL string
	// BasicAuth sends the client id and secret as HTTP Basic instead of as form
	// fields, which is what Bitbucket's token endpoint requires.
	BasicAuth bool
}

// Client identifies the application to the server. Secret is empty for the
// public clients of a device flow.
type Client struct {
	ID     string
	Secret string
}

type genericDeviceResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	Error                   string `json:"error"`
	ErrorDescription        string `json:"error_description"`
}

type genericTokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// DeviceStart asks the server for a device code. The user opens VerificationURL
// and types UserCode; the caller then polls with DevicePoll.
func DeviceStart(ctx context.Context, hc HTTPClient, ep Endpoints, c Client, scope string) (DeviceCode, error) {
	if ep.DeviceURL == "" {
		return DeviceCode{}, errors.New("this server has no device login")
	}
	form := url.Values{"client_id": {c.ID}}
	if scope != "" {
		form.Set("scope", scope)
	}
	var resp genericDeviceResponse
	if err := postFormAuth(ctx, hc, ep.DeviceURL, form, "", "", &resp); err != nil {
		return DeviceCode{}, err
	}
	if resp.Error != "" {
		return DeviceCode{}, describeOAuthError(resp.Error, resp.ErrorDescription)
	}
	if resp.DeviceCode == "" {
		return DeviceCode{}, errors.New("the server returned no device code")
	}
	verify := resp.VerificationURIComplete
	if verify == "" {
		verify = resp.VerificationURI
	}
	return DeviceCode{
		DeviceCode:      resp.DeviceCode,
		UserCode:        resp.UserCode,
		VerificationURL: verify,
		ExpiresIn:       resp.ExpiresIn,
		Interval:        resp.Interval,
	}, nil
}

// DevicePoll asks once whether the user has approved the device. While they
// have not it returns ErrAuthorizationPending (or ErrSlowDown), which PollDevice
// understands.
func DevicePoll(ctx context.Context, hc HTTPClient, ep Endpoints, c Client, dc DeviceCode) (Token, error) {
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {dc.DeviceCode},
		"client_id":   {c.ID},
	}
	return tokenRequest(ctx, hc, ep, c, form)
}

// AuthCodeURL builds the URL the browser is sent to for the code flow.
func AuthCodeURL(ep Endpoints, c Client, redirectURI, scope string, p PKCE) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.ID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {p.Challenge},
		"code_challenge_method": {"S256"},
		"state":                 {p.State},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	sep := "?"
	if strings.Contains(ep.AuthorizeURL, "?") {
		sep = "&"
	}
	return ep.AuthorizeURL + sep + q.Encode()
}

// ExchangeAuthCode trades the code the browser came back with for a token.
func ExchangeAuthCode(ctx context.Context, hc HTTPClient, ep Endpoints, c Client, code, redirectURI string, p PKCE) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {c.ID},
		"code_verifier": {p.Verifier},
	}
	return tokenRequest(ctx, hc, ep, c, form)
}

// RefreshAccessToken renews a token. The server may rotate the refresh token, so
// the one in the result replaces the stored one when it is not empty.
func RefreshAccessToken(ctx context.Context, hc HTTPClient, ep Endpoints, c Client, refresh string) (Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {c.ID},
	}
	tok, err := tokenRequest(ctx, hc, ep, c, form)
	if err == nil && tok.RefreshToken == "" {
		tok.RefreshToken = refresh
	}
	return tok, err
}

// tokenRequest posts to the token endpoint and reads the answer: a token, or one
// of the sentinel errors a device poll understands.
func tokenRequest(ctx context.Context, hc HTTPClient, ep Endpoints, c Client, form url.Values) (Token, error) {
	user, pass := "", ""
	if ep.BasicAuth {
		user, pass = c.ID, c.Secret
	} else if c.Secret != "" {
		form.Set("client_secret", c.Secret)
	}
	var resp genericTokenResponse
	err := postFormAuth(ctx, hc, ep.TokenURL, form, user, pass, &resp)
	// A pending device poll is a 200 on GitHub and a 400 elsewhere: the error
	// field, not the status, says whether to go on.
	if resp.Error != "" {
		perr := pollError(resp.Error)
		for _, known := range []error{ErrAuthorizationPending, ErrSlowDown, ErrAccessDenied, ErrExpiredToken} {
			if errors.Is(perr, known) {
				return Token{}, perr
			}
		}
		return Token{}, describeOAuthError(resp.Error, resp.ErrorDescription)
	}
	if err != nil {
		return Token{}, err
	}
	if resp.AccessToken == "" {
		return Token{}, errors.New("the server returned no access token")
	}
	tok := Token{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		TokenType:    resp.TokenType,
		Scopes:       resp.Scope,
	}
	if resp.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	}
	return tok, nil
}

func describeOAuthError(code, description string) error {
	if description != "" {
		return errors.New(code + ": " + description)
	}
	return errors.New(code)
}

// postFormAuth is postForm with optional HTTP Basic credentials.
func postFormAuth(ctx context.Context, hc HTTPClient, target string, form url.Values, user, pass string, dest any) error {
	if hc == nil {
		hc = defaultClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	return doJSON(hc, req, dest)
}

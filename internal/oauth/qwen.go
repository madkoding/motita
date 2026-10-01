package oauth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// Qwen: the device login of Qwen Code (chat.qwen.ai), which gives a qwen.ai
// account access to the Qwen coding models without a DashScope key.
//
// It is a device flow (RFC 8628) with a PKCE verifier on top: the device code
// request carries the challenge and the token poll carries the verifier. The
// token response names the API host (resource_url) the token is valid for.
const (
	QwenClientID = "f0304373b74a44d2b584a3fb70ca9e56"
	QwenScopes   = "openid profile email model.completion"
	qwenOAuthURL = "https://chat.qwen.ai/api/v1/oauth2"
	// QwenDefaultBaseURL is used when a token names no resource_url.
	QwenDefaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
)

type qwenDeviceResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	Error                   string `json:"error"`
}

type qwenTokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	ResourceURL      string `json:"resource_url"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// QwenRequestDeviceCode starts the device login.
func QwenRequestDeviceCode(ctx context.Context, client HTTPClient, p PKCE) (DeviceCode, error) {
	form := url.Values{
		"client_id":             {QwenClientID},
		"scope":                 {QwenScopes},
		"code_challenge":        {p.Challenge},
		"code_challenge_method": {"S256"},
	}
	var resp qwenDeviceResponse
	if err := postForm(ctx, client, qwenOAuthURL+"/device/code", form, &resp); err != nil {
		return DeviceCode{}, err
	}
	if resp.DeviceCode == "" {
		return DeviceCode{}, errors.New("qwen.ai returned no device code")
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

// QwenPollToken asks once whether the user approved the device. While they have
// not, it returns ErrAuthorizationPending (or ErrSlowDown).
func QwenPollToken(ctx context.Context, client HTTPClient, dc DeviceCode, p PKCE) (Credential, error) {
	form := url.Values{
		"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":     {QwenClientID},
		"device_code":   {dc.DeviceCode},
		"code_verifier": {p.Verifier},
	}
	var resp qwenTokenResponse
	err := postForm(ctx, client, qwenOAuthURL+"/token", form, &resp)
	if perr := pollError(resp.Error); perr != nil {
		return Credential{}, perr
	}
	if err != nil {
		return Credential{}, err
	}
	return qwenCredential(resp, Credential{})
}

// QwenRefresh renews the access token.
func QwenRefresh(ctx context.Context, client HTTPClient, old Credential) (Credential, error) {
	if old.RefreshToken == "" {
		return Credential{}, errors.New("the Qwen login has no refresh token: log in again")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {old.RefreshToken},
		"client_id":     {QwenClientID},
	}
	var resp qwenTokenResponse
	if err := postForm(ctx, client, qwenOAuthURL+"/token", form, &resp); err != nil {
		return Credential{}, err
	}
	return qwenCredential(resp, old)
}

func qwenCredential(resp qwenTokenResponse, old Credential) (Credential, error) {
	if resp.Error != "" {
		return Credential{}, errors.New("the Qwen login failed: " + strings.TrimSpace(resp.Error+" "+resp.ErrorDescription))
	}
	if resp.AccessToken == "" {
		return Credential{}, errors.New("the Qwen login returned no access token")
	}
	c := old
	c.Provider = "qwen"
	c.AccessToken = resp.AccessToken
	if resp.RefreshToken != "" {
		c.RefreshToken = resp.RefreshToken
	}
	c.ExpiresAt = time.Time{}
	if resp.ExpiresIn > 0 {
		c.ExpiresAt = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	}
	if resp.ResourceURL != "" {
		c.BaseURL = QwenBaseURL(resp.ResourceURL)
	}
	return c, nil
}

// QwenBaseURL turns a resource_url ("portal.qwen.ai") into the OpenAI-compatible
// base URL the token is valid for ("https://portal.qwen.ai/v1").
func QwenBaseURL(resource string) string {
	r := strings.TrimRight(strings.TrimSpace(resource), "/")
	if r == "" {
		return QwenDefaultBaseURL
	}
	if !strings.HasPrefix(r, "http://") && !strings.HasPrefix(r, "https://") {
		r = "https://" + r
	}
	if !strings.HasSuffix(r, "/v1") {
		r += "/v1"
	}
	return r
}

// PollDevice polls a device flow until it ends: approved, denied, expired, or
// cancelled. It honours the server's interval and slows down when told to.
// sleep is injectable so a test does not wait.
func PollDevice[T any](ctx context.Context, interval int, sleep func(time.Duration) <-chan time.Time, poll func() (T, error)) (T, error) {
	if interval <= 0 {
		interval = 5
	}
	if sleep == nil {
		sleep = time.After
	}
	for {
		v, err := poll()
		switch {
		case err == nil:
			return v, nil
		case errors.Is(err, ErrSlowDown):
			interval += 5
		case errors.Is(err, ErrAuthorizationPending):
		default:
			return v, err
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-sleep(time.Duration(interval) * time.Second):
		}
	}
}

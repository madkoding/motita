package oauth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Literals used more than once in this file.
const (
	httpsScheme = "https://"
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
	// Qwen Code tags the request with an id; qwen.ai's gateway expects it.
	headers := map[string]string{"x-request-id": newRequestID()}
	var resp qwenDeviceResponse
	if err := postFormHeaders(ctx, client, qwenOAuthURL+"/device/code", form, headers, &resp); err != nil {
		return DeviceCode{}, qwenLoginError(err)
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
		return Credential{}, qwenLoginError(err)
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
		return Credential{}, qwenLoginError(err)
	}
	return qwenCredential(resp, old)
}

// ErrQwenLoginUnavailable is a qwen.ai login answered with a web page instead of
// the OAuth JSON. Qwen ended the free Qwen OAuth tier on 2026-04-15, and since
// then its OAuth endpoints answer with a page; a DashScope key is the way in.
var ErrQwenLoginUnavailable = errors.New(
	"the qwen.ai login is not available (Qwen ended its free OAuth login on 2026-04-15): " +
		"use a DashScope (Alibaba Cloud Model Studio) API key instead")

// qwenLoginError explains a non-JSON answer from qwen.ai, keeping the status (the
// page itself is never quoted).
func qwenLoginError(err error) error {
	if errors.Is(err, ErrNotJSON) {
		return fmt.Errorf("%w; %w", ErrQwenLoginUnavailable, err)
	}
	return err
}

// newRequestID returns a random UUID v4 for the x-request-id header.
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
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
// base URL the token is valid for ("https://portal.qwen.ai/v1"). It is always
// https: the bearer token travels with every request, and a server that answered
// "http://" must not get it sent in clear text.
func QwenBaseURL(resource string) string {
	r := strings.TrimRight(strings.TrimSpace(resource), "/")
	if r == "" {
		return QwenDefaultBaseURL
	}
	lower := strings.ToLower(r)
	if strings.HasPrefix(lower, httpsScheme) {
		r = r[len(httpsScheme):]
	} else if strings.HasPrefix(lower, "http://") {
		r = r[len("http://"):]
	} else {
		// No scheme: the bare host gets https below.
	}
	r = httpsScheme + r
	if !strings.HasSuffix(r, "/v1") {
		r += "/v1"
	}
	return r
}

// PollDevice polls a device flow until it ends: approved, denied, expired, or
// cancelled. It honours the server's interval and slows down when told to.
// sleep is injectable so a test does not wait.
func PollDevice[T any](
	ctx context.Context, interval int, sleep func(time.Duration) <-chan time.Time, poll func() (T, error),
) (T, error) {
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

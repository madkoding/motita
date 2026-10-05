package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func formOf(t *testing.T, req *http.Request) url.Values {
	t.Helper()
	b, _ := io.ReadAll(req.Body)
	v, err := url.ParseQuery(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDeviceStart(t *testing.T) {
	ep := Endpoints{DeviceURL: "https://h.test/device", TokenURL: "https://h.test/token"}
	hc := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"h.test/device": func(r *http.Request) (*http.Response, error) {
			f := formOf(t, r)
			if f.Get("client_id") != "cid" || f.Get("scope") != "repo" {
				t.Errorf("form = %v", f)
			}
			return jsonResp(map[string]any{"device_code": "d", "user_code": "U-1", "verification_uri": "https://h.test/v", "expires_in": 900, "interval": 5}), nil
		},
	})
	dc, err := DeviceStart(context.Background(), hc, ep, Client{ID: "cid"}, "repo")
	if err != nil || dc.UserCode != "U-1" || dc.VerificationURL != "https://h.test/v" || dc.Interval != 5 {
		t.Fatalf("dc=%+v err=%v", dc, err)
	}
	// verification_uri_complete wins, and an empty scope is not sent.
	hc = newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"h.test/device": func(r *http.Request) (*http.Response, error) {
			if _, ok := formOf(t, r)["scope"]; ok {
				t.Error("an empty scope must not be sent")
			}
			return jsonResp(map[string]any{"device_code": "d", "verification_uri": "https://h.test/v", "verification_uri_complete": "https://h.test/v?c=1"}), nil
		},
	})
	dc, err = DeviceStart(context.Background(), hc, ep, Client{ID: "cid"}, "")
	if err != nil || dc.VerificationURL != "https://h.test/v?c=1" {
		t.Fatalf("dc=%+v err=%v", dc, err)
	}
}

func TestDeviceStartFailures(t *testing.T) {
	if _, err := DeviceStart(context.Background(), nil, Endpoints{}, Client{}, ""); err == nil {
		t.Error("an endpoint without a device URL must be refused")
	}
	ep := Endpoints{DeviceURL: "https://h.test/device"}
	cases := map[string]func(*http.Request) (*http.Response, error){
		"transport": func(*http.Request) (*http.Response, error) { return nil, errors.New("down") },
		"oauth": func(*http.Request) (*http.Response, error) {
			return jsonResp(map[string]any{"error": "unauthorized_client", "error_description": "no"}), nil
		},
		"oauth2": func(*http.Request) (*http.Response, error) { return jsonResp(map[string]any{"error": "bad"}), nil },
		"empty":  func(*http.Request) (*http.Response, error) { return jsonResp(map[string]any{}), nil },
	}
	for name, h := range cases {
		hc := newFakeClient(map[string]func(*http.Request) (*http.Response, error){"h.test/device": h})
		if _, err := DeviceStart(context.Background(), hc, ep, Client{ID: "c"}, ""); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestDevicePollAndRefresh(t *testing.T) {
	ep := Endpoints{TokenURL: "https://h.test/token"}
	step := 0
	hc := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"h.test/token": func(r *http.Request) (*http.Response, error) {
			f := formOf(t, r)
			switch f.Get("grant_type") {
			case "refresh_token":
				return jsonResp(map[string]any{"access_token": "new"}), nil
			}
			step++
			switch step {
			case 1:
				return jsonResp(map[string]any{"error": "authorization_pending"}), nil
			case 2:
				return errResp(400, `{"error":"slow_down"}`), nil
			case 3:
				return jsonResp(map[string]any{"error": "access_denied"}), nil
			case 4:
				return jsonResp(map[string]any{"error": "expired_token"}), nil
			case 5:
				return jsonResp(map[string]any{"error": "weird", "error_description": "why"}), nil
			case 6:
				return errResp(500, "boom"), nil
			case 7:
				return jsonResp(map[string]any{}), nil
			}
			return jsonResp(map[string]any{"access_token": "tok", "refresh_token": "r", "token_type": "bearer", "scope": "repo", "expires_in": 3600}), nil
		},
	})
	dc := DeviceCode{DeviceCode: "d"}
	want := []error{ErrAuthorizationPending, ErrSlowDown, ErrAccessDenied, ErrExpiredToken}
	for i, w := range want {
		if _, err := DevicePoll(context.Background(), hc, ep, Client{ID: "c"}, dc); !errors.Is(err, w) {
			t.Errorf("step %d: %v, want %v", i+1, err, w)
		}
	}
	for i := 5; i <= 7; i++ {
		if _, err := DevicePoll(context.Background(), hc, ep, Client{ID: "c"}, dc); err == nil || errors.Is(err, ErrAuthorizationPending) {
			t.Errorf("step %d: want a plain error, got %v", i, err)
		}
	}
	tok, err := DevicePoll(context.Background(), hc, ep, Client{ID: "c"}, dc)
	if err != nil || tok.AccessToken != "tok" || tok.RefreshToken != "r" || tok.ExpiresAt.IsZero() || tok.Scopes != "repo" {
		t.Fatalf("tok=%+v err=%v", tok, err)
	}
	// A refresh that returns no new refresh token keeps the old one.
	tok, err = RefreshAccessToken(context.Background(), hc, ep, Client{ID: "c"}, "old")
	if err != nil || tok.AccessToken != "new" || tok.RefreshToken != "old" {
		t.Fatalf("tok=%+v err=%v", tok, err)
	}
	if _, err := RefreshAccessToken(context.Background(), newFakeClient(nil), ep, Client{ID: "c"}, "old"); err == nil {
		t.Error("a failed refresh must be an error")
	}
}

func TestAuthCodeURLAndExchange(t *testing.T) {
	p := PKCE{Verifier: "v", Challenge: "ch", State: "st"}
	u := AuthCodeURL(Endpoints{AuthorizeURL: "https://h.test/auth"}, Client{ID: "c"}, "http://localhost:1/cb", "repo", p)
	if !strings.HasPrefix(u, "https://h.test/auth?") || !strings.Contains(u, "code_challenge=ch") || !strings.Contains(u, "scope=repo") {
		t.Errorf("url = %s", u)
	}
	u = AuthCodeURL(Endpoints{AuthorizeURL: "https://h.test/auth?x=1"}, Client{ID: "c"}, "r", "", p)
	if !strings.Contains(u, "?x=1&") || strings.Contains(u, "scope=") {
		t.Errorf("url = %s", u)
	}

	var gotUser, gotPass string
	var gotSecret string
	hc := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"h.test/token": func(r *http.Request) (*http.Response, error) {
			gotUser, gotPass, _ = r.BasicAuth()
			gotSecret = formOf(t, r).Get("client_secret")
			return jsonResp(map[string]any{"access_token": "a"}), nil
		},
	})
	ep := Endpoints{TokenURL: "https://h.test/token"}
	if _, err := ExchangeAuthCode(context.Background(), hc, ep, Client{ID: "c", Secret: "s"}, "code", "r", p); err != nil || gotSecret != "s" || gotUser != "" {
		t.Errorf("form secret: %q user %q err %v", gotSecret, gotUser, err)
	}
	ep.BasicAuth = true
	if _, err := ExchangeAuthCode(context.Background(), hc, ep, Client{ID: "c", Secret: "s"}, "code", "r", p); err != nil || gotUser != "c" || gotPass != "s" || gotSecret != "" {
		t.Errorf("basic: %q %q %q err %v", gotUser, gotPass, gotSecret, err)
	}
	if _, err := ExchangeAuthCode(context.Background(), hc, Endpoints{TokenURL: "http://bad host/%"}, Client{}, "c", "r", p); err == nil {
		t.Error("a malformed URL must be an error")
	}
}

func TestPostFormAuthUsesTheDefaultClient(t *testing.T) {
	restore := SetDefaultClient(func() *http.Client {
		return newFakeClient(map[string]func(*http.Request) (*http.Response, error){
			"h.test/token": func(*http.Request) (*http.Response, error) { return jsonResp(map[string]any{"access_token": "a"}), nil },
		})
	})
	defer restore()
	tok, err := RefreshAccessToken(context.Background(), nil, Endpoints{TokenURL: "https://h.test/token"}, Client{ID: "c"}, "r")
	if err != nil || tok.AccessToken != "a" {
		t.Fatalf("tok=%+v err=%v", tok, err)
	}
}

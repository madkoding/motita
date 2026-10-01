package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- PKCE ---------------------------------------------------------------------

// TestPKCEChallengeIsTheHashOfTheVerifierString: RFC 7636 §4.2 hashes the verifier
// AS SENT. The challenge used to hash the random bytes behind it, so no server could
// ever match a code_verifier to its challenge and every code exchange failed.
func TestPKCEChallengeIsTheHashOfTheVerifierString(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	if p.Challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Errorf("challenge %q is not S256(verifier)", p.Challenge)
	}
	if len(p.Verifier) < 43 || p.State == "" {
		t.Errorf("verifier %q too short or state empty", p.Verifier)
	}
}

func TestNewPKCEReportsEntropyFailures(t *testing.T) {
	for _, r := range []io.Reader{failingReader{}, &shortReader{n: 32}} {
		old := RandReader
		RandReader = r
		_, err := NewPKCE()
		RandReader = old
		if err == nil {
			t.Error("a failing random source must be an error")
		}
	}
}

// --- Store --------------------------------------------------------------------

func TestCredentialStoreRoundTripsWithPrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "auth")
	if HasCredential(dir, "qwen") {
		t.Fatal("nothing stored yet")
	}
	if _, err := LoadCredential(dir, "qwen"); err != ErrNoCredential {
		t.Fatalf("err = %v", err)
	}
	c := Credential{Provider: "qwen", AccessToken: "a", RefreshToken: "r", BaseURL: "https://portal.qwen.ai/v1", ExpiresAt: time.Now().Add(time.Hour).UTC()}
	if err := SaveCredential(dir, c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(CredentialPath(dir, "qwen"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, a credential must be 0600", info.Mode().Perm())
	}
	got, err := LoadCredential(dir, "QWEN")
	if err != nil || got.AccessToken != "a" || got.BaseURL != c.BaseURL || !got.ExpiresAt.Equal(c.ExpiresAt) {
		t.Errorf("got %+v, %v", got, err)
	}
	if !HasCredential(dir, "qwen") {
		t.Error("HasCredential must see it")
	}
	if err := DeleteCredential(dir, "qwen"); err != nil || HasCredential(dir, "qwen") {
		t.Errorf("delete: %v", err)
	}
	if err := DeleteCredential(dir, "qwen"); err != nil {
		t.Errorf("deleting twice is not an error: %v", err)
	}
	if err := SaveCredential("", c); err == nil {
		t.Error("no directory must be an error")
	}
	if err := SaveCredential(dir, Credential{}); err == nil {
		t.Error("a credential without a provider must be refused")
	}
	_ = os.WriteFile(CredentialPath(dir, "bad"), []byte("{"), 0o600)
	if _, err := LoadCredential(dir, "bad"); err == nil {
		t.Error("an unreadable file must be an error")
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Now()
	cases := []struct {
		c    Credential
		want bool
	}{
		{Credential{}, true},
		{Credential{AccessToken: "a"}, false},
		{Credential{AccessToken: "a", ExpiresAt: now.Add(time.Hour)}, false},
		{Credential{AccessToken: "a", ExpiresAt: now.Add(time.Minute)}, true},
		{Credential{AccessToken: "a", ExpiresAt: now.Add(-time.Minute)}, true},
	}
	for i, tc := range cases {
		if got := tc.c.NeedsRefresh(now); got != tc.want {
			t.Errorf("%d: NeedsRefresh = %v", i, got)
		}
	}
}

// --- Loopback -----------------------------------------------------------------

func TestParseAuthCode(t *testing.T) {
	cases := []struct {
		in, state, want string
		bad             bool
	}{
		{"http://localhost:1455/auth/callback?code=abc&state=s1", "s1", "abc", false},
		{"http://localhost:1455/auth/callback?code=abc&state=other", "s1", "", true},
		{"http://localhost/?error=access_denied", "s1", "", true},
		{"code=abc&state=s1", "s1", "abc", false},
		{"?code=abc", "s1", "abc", false},
		{"abc#s1", "s1", "abc", false},
		{"  rawcode \n", "s1", "rawcode", false},
		{"", "s1", "", true},
		{"http://x/?state=s1", "s1", "", true},
	}
	for _, tc := range cases {
		got, err := ParseAuthCode(tc.in, tc.state)
		if (err != nil) != tc.bad || got != tc.want {
			t.Errorf("ParseAuthCode(%q) = %q, %v", tc.in, got, err)
		}
	}
}

func TestLoopbackReceivesTheBrowserRedirect(t *testing.T) {
	lb, err := StartLoopback("127.0.0.1:0", "127.0.0.1", "/cb")
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()
	go func() {
		resp, err := http.Get(lb.RedirectURI + "?code=xyz&state=st")
		if err == nil {
			resp.Body.Close()
		}
	}()
	code, pasted, err := lb.Wait(context.Background(), "st", nil)
	if err != nil || code != "xyz" || pasted {
		t.Errorf("code=%q pasted=%v err=%v", code, pasted, err)
	}
}

func TestLoopbackRefusesAForeignStateAndAcceptsAPaste(t *testing.T) {
	lb, err := StartLoopback("127.0.0.1:0", "localhost", "/cb")
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()
	if !strings.HasPrefix(lb.RedirectURI, "http://localhost:") {
		t.Errorf("redirect = %q", lb.RedirectURI)
	}
	go func() {
		resp, err := http.Get(strings.Replace(lb.RedirectURI, "localhost", "127.0.0.1", 1) + "?code=xyz&state=evil")
		if err == nil {
			resp.Body.Close()
		}
	}()
	if _, _, err := lb.Wait(context.Background(), "st", nil); err == nil {
		t.Error("a callback with another state must be refused")
	}
	paste := make(chan string, 1)
	paste <- lb.RedirectURI + "?code=pasted&state=st"
	code, pasted, err := lb.Wait(context.Background(), "st", paste)
	if err != nil || code != "pasted" || !pasted {
		t.Errorf("code=%q pasted=%v err=%v", code, pasted, err)
	}
	var none *Loopback
	closed := make(chan string)
	close(closed)
	if _, _, err := none.Wait(context.Background(), "st", closed); err == nil {
		t.Error("no browser and no paste must be an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := lb.Wait(ctx, "st", nil); err == nil {
		t.Error("a cancelled wait must be an error")
	}
}

// --- Codex ----------------------------------------------------------------------

func fakeJWT(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

func TestCodexAuthorizeURLAndExchange(t *testing.T) {
	p, _ := NewPKCE()
	u, _ := url.Parse(CodexAuthorizeURL(p, ""))
	q := u.Query()
	if u.Host != "auth.openai.com" || q.Get("client_id") != CodexClientID || q.Get("redirect_uri") != CodexRedirectURI ||
		q.Get("code_challenge") != p.Challenge || q.Get("state") != p.State || !strings.Contains(q.Get("scope"), "offline_access") {
		t.Errorf("authorize URL = %s", u)
	}
	id := fakeJWT(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct"}, "exp": float64(time.Now().Add(time.Hour).Unix())})
	var form url.Values
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"auth.openai.com/oauth/token": func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			form, _ = url.ParseQuery(string(b))
			return jsonResp(map[string]any{"id_token": id, "access_token": "opaque", "refresh_token": "rt"}), nil
		},
	})
	c, err := CodexExchangeCode(context.Background(), client, "the-code", "", p)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("code_verifier") != p.Verifier || form.Get("code") != "the-code" || form.Get("grant_type") != "authorization_code" {
		t.Errorf("form = %v", form)
	}
	if c.Provider != "codex" || c.AccountID != "acct" || c.RefreshToken != "rt" {
		t.Errorf("cred = %+v", c)
	}
}

func TestCodexLoginWithoutAnAccountIsRefused(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth/token": func(*http.Request) (*http.Response, error) {
			return jsonResp(map[string]any{"access_token": "opaque"}), nil
		},
	})
	if _, err := CodexRefresh(context.Background(), client, Credential{RefreshToken: "r"}); err == nil {
		t.Error("a token that names no ChatGPT account must be refused")
	}
	if _, err := CodexRefresh(context.Background(), client, Credential{}); err == nil {
		t.Error("no refresh token must be an error")
	}
	errClient := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth/token": func(*http.Request) (*http.Response, error) {
			return errResp(400, `{"error":"invalid_grant"}`), nil
		},
	})
	if _, err := CodexRefresh(context.Background(), errClient, Credential{RefreshToken: "r"}); err == nil {
		t.Error("a refused refresh must be an error")
	}
	if ChatGPTAccountID("not-a-jwt") != "" || ChatGPTAccountID("a.!!!.c") != "" {
		t.Error("a malformed token has no account")
	}
}

// --- Qwen -----------------------------------------------------------------------

func TestQwenDeviceFlow(t *testing.T) {
	p, _ := NewPKCE()
	var polls int
	var pollForm url.Values
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2/device/code": func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			f, _ := url.ParseQuery(string(b))
			if f.Get("code_challenge") != p.Challenge || f.Get("client_id") != QwenClientID {
				t.Errorf("device form = %v", f)
			}
			return jsonResp(map[string]any{"device_code": "dc", "user_code": "UC", "verification_uri": "https://chat.qwen.ai/authorize", "verification_uri_complete": "https://chat.qwen.ai/authorize?user_code=UC", "expires_in": 600}), nil
		},
		"oauth2/token": func(r *http.Request) (*http.Response, error) {
			polls++
			b, _ := io.ReadAll(r.Body)
			pollForm, _ = url.ParseQuery(string(b))
			if polls == 1 {
				return errResp(400, `{"error":"authorization_pending"}`), nil
			}
			return jsonResp(map[string]any{"access_token": "qa", "refresh_token": "qr", "expires_in": 3600, "resource_url": "portal.qwen.ai"}), nil
		},
	})
	dc, err := QwenRequestDeviceCode(context.Background(), client, p)
	if err != nil || dc.VerificationURL != "https://chat.qwen.ai/authorize?user_code=UC" {
		t.Fatalf("dc=%+v err=%v", dc, err)
	}
	instant := func(time.Duration) <-chan time.Time { ch := make(chan time.Time, 1); ch <- time.Now(); return ch }
	cred, err := PollDevice(context.Background(), dc.Interval, instant, func() (Credential, error) {
		return QwenPollToken(context.Background(), client, dc, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if pollForm.Get("code_verifier") != p.Verifier || pollForm.Get("device_code") != "dc" {
		t.Errorf("poll form = %v", pollForm)
	}
	if cred.BaseURL != "https://portal.qwen.ai/v1" || cred.AccessToken != "qa" || cred.Provider != "qwen" {
		t.Errorf("cred = %+v", cred)
	}
}

func TestQwenBaseURLAndRefresh(t *testing.T) {
	cases := map[string]string{
		"":                           QwenDefaultBaseURL,
		"portal.qwen.ai":             "https://portal.qwen.ai/v1",
		"https://portal.qwen.ai/v1/": "https://portal.qwen.ai/v1",
	}
	for in, want := range cases {
		if got := QwenBaseURL(in); got != want {
			t.Errorf("QwenBaseURL(%q) = %q", in, got)
		}
	}
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2/token": func(*http.Request) (*http.Response, error) {
			return jsonResp(map[string]any{"access_token": "new"}), nil
		},
	})
	c, err := QwenRefresh(context.Background(), client, Credential{RefreshToken: "keep", BaseURL: "https://portal.qwen.ai/v1"})
	if err != nil || c.AccessToken != "new" || c.RefreshToken != "keep" || c.BaseURL != "https://portal.qwen.ai/v1" {
		t.Errorf("c=%+v err=%v", c, err)
	}
	if _, err := QwenRefresh(context.Background(), client, Credential{}); err == nil {
		t.Error("no refresh token must be an error")
	}
}

func TestPollDeviceStopsOnDenialAndCancellation(t *testing.T) {
	instant := func(time.Duration) <-chan time.Time { ch := make(chan time.Time, 1); ch <- time.Now(); return ch }
	if _, err := PollDevice(context.Background(), 0, instant, func() (int, error) { return 0, ErrAccessDenied }); err != ErrAccessDenied {
		t.Errorf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	never := func(time.Duration) <-chan time.Time { return nil }
	if _, err := PollDevice(ctx, 1, never, func() (int, error) { return 0, ErrAuthorizationPending }); err == nil {
		t.Error("a cancelled poll must end")
	}
}

// --- Gemini -----------------------------------------------------------------------

func TestGeminiAuthorizeURLAskForARefreshToken(t *testing.T) {
	p, _ := NewPKCE()
	u, _ := url.Parse(GeminiAuthorizeURL(GeminiClient{ID: "cid"}, p, "http://127.0.0.1:5555/"))
	q := u.Query()
	if q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("client_id") != "cid" ||
		!strings.Contains(q.Get("scope"), "generative-language") || q.Get("code_challenge") != p.Challenge {
		t.Errorf("URL = %s", u)
	}
}

func TestGeminiExchangeAndRefreshKeepTheClientAndProject(t *testing.T) {
	var forms []url.Values
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2.googleapis.com/token": func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			f, _ := url.ParseQuery(string(b))
			forms = append(forms, f)
			return jsonResp(map[string]any{"access_token": "ya29", "refresh_token": "1//r", "expires_in": 3599}), nil
		},
	})
	p, _ := NewPKCE()
	c, err := GeminiExchangeCode(context.Background(), client, GeminiClient{ID: "cid", Secret: "sec"}, "code", "http://127.0.0.1:1/", p, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if c.ProjectID != "proj" || c.ClientID != "cid" || c.ClientSecret != "sec" || c.RefreshToken != "1//r" {
		t.Errorf("cred = %+v", c)
	}
	c2, err := GeminiRefresh(context.Background(), client, c)
	if err != nil || c2.ProjectID != "proj" {
		t.Fatalf("refresh: %+v %v", c2, err)
	}
	if forms[1].Get("grant_type") != "refresh_token" || forms[1].Get("client_secret") != "sec" {
		t.Errorf("refresh form = %v", forms[1])
	}
	if _, err := GeminiRefresh(context.Background(), client, Credential{}); err == nil {
		t.Error("no refresh token must be an error")
	}
}

func TestGeminiFromADC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adc.json")
	_ = os.WriteFile(path, []byte(`{"type":"authorized_user","client_id":"c","client_secret":"s","refresh_token":"r","quota_project_id":"qp"}`), 0o600)
	c, err := GeminiFromADC(path, "")
	if err != nil || c.ProjectID != "qp" || c.RefreshToken != "r" || c.ClientID != "c" {
		t.Errorf("c=%+v err=%v", c, err)
	}
	if c, _ := GeminiFromADC(path, "mine"); c.ProjectID != "mine" {
		t.Error("an explicit project wins over the quota project")
	}
	_ = os.WriteFile(path, []byte(`{"type":"service_account"}`), 0o600)
	if _, err := GeminiFromADC(path, ""); err == nil {
		t.Error("a service account key must not be copied")
	}
	if _, err := GeminiFromADC(filepath.Join(dir, "none"), ""); err == nil {
		t.Error("a missing file must be an error")
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	if ADCPath("/home/x") != path {
		t.Error("GOOGLE_APPLICATION_CREDENTIALS must win")
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("APPDATA", "")
	if got := ADCPath("/home/x"); got != "/home/x/.config/gcloud/application_default_credentials.json" {
		t.Errorf("ADCPath = %q", got)
	}
}

func TestGeminiClientFromEnv(t *testing.T) {
	t.Setenv("MOTITA_GEMINI_CLIENT_ID", "")
	if _, ok := GeminiClientFromEnv(); ok {
		t.Error("no client must be reported")
	}
	t.Setenv("MOTITA_GEMINI_CLIENT_ID", " id ")
	t.Setenv("MOTITA_GEMINI_CLIENT_SECRET", "s")
	if c, ok := GeminiClientFromEnv(); !ok || c.ID != "id" || c.Secret != "s" {
		t.Errorf("c = %+v", c)
	}
}

// --- Copilot ------------------------------------------------------------------------

func TestCopilotRefreshCredential(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"copilot_internal/v2/token": func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "token gho_1" {
				t.Errorf("auth = %q", r.Header.Get("Authorization"))
			}
			return jsonResp(map[string]any{"token": "tid", "expires_at": time.Now().Add(time.Hour).Unix(), "endpoints": map[string]string{"api": "https://api.enterprise.githubcopilot.com/"}}), nil
		},
	})
	c, err := CopilotRefreshCredential(context.Background(), client, Credential{RefreshToken: "gho_1"})
	if err != nil || c.AccessToken != "tid" || c.RefreshToken != "gho_1" || c.BaseURL != "https://api.enterprise.githubcopilot.com" {
		t.Errorf("c=%+v err=%v", c, err)
	}
	if _, err := CopilotRefreshCredential(context.Background(), client, Credential{}); err == nil {
		t.Error("no GitHub token must be an error")
	}
	empty := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"copilot_internal/v2/token": func(*http.Request) (*http.Response, error) { return jsonResp(map[string]any{}), nil },
	})
	if _, err := CopilotRefreshCredential(context.Background(), empty, Credential{RefreshToken: "g"}); err == nil {
		t.Error("no Copilot token must be an error")
	}
}

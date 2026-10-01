package oauth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tokenServer(body string, status int) *http.Client {
	return newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"": func(*http.Request) (*http.Response, error) {
			if status != 200 {
				return errResp(status, body), nil
			}
			r := errResp(200, body)
			return r, nil
		},
	})
}

func TestStoreEdges(t *testing.T) {
	if _, err := LoadCredential("", "qwen"); err != ErrNoCredential {
		t.Errorf("no directory: %v", err)
	}
	if HasCredential("", "qwen") {
		t.Error("no directory has no login")
	}
	dir := t.TempDir()
	_ = os.Mkdir(CredentialPath(dir, "dirlike"), 0o700)
	if _, err := LoadCredential(dir, "dirlike"); err == nil || err == ErrNoCredential {
		t.Errorf("an unreadable path must be an error: %v", err)
	}
	_ = os.WriteFile(CredentialPath(dir, "anon"), []byte(`{"access_token":"a"}`), 0o600)
	if c, _ := LoadCredential(dir, "anon"); c.Provider != "anon" {
		t.Errorf("the provider is taken from the file name: %+v", c)
	}
	file := filepath.Join(dir, "file")
	_ = os.WriteFile(file, nil, 0o600)
	if err := SaveCredential(filepath.Join(file, "sub"), Credential{Provider: "qwen"}); err == nil {
		t.Error("a directory that cannot be created must be an error")
	}
	_ = os.Mkdir(filepath.Join(dir, fmt.Sprintf(".blocked-%d.tmp", os.Getpid())), 0o700)
	if err := SaveCredential(dir, Credential{Provider: "blocked"}); err == nil {
		t.Error("a temporary file that cannot be written must be an error")
	}
	_ = os.MkdirAll(filepath.Join(CredentialPath(dir, "taken"), "x"), 0o700)
	if err := SaveCredential(dir, Credential{Provider: "taken"}); err == nil {
		t.Error("a destination that cannot be replaced must be an error")
	}
}

func TestLoopbackEdges(t *testing.T) {
	lb, err := StartLoopback("127.0.0.1:0", "127.0.0.1", "/cb")
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()
	port := strings.TrimPrefix(lb.RedirectURI[strings.LastIndex(lb.RedirectURI, ":"):], ":")
	port = strings.TrimSuffix(port, "/cb")
	if _, err := StartLoopback(net.JoinHostPort("127.0.0.1", port), "127.0.0.1", "/cb"); err == nil {
		t.Error("a busy port must be an error")
	}
	for _, q := range []string{"?error=access_denied&error_description=nope", "?state=s"} {
		resp, err := http.Get(lb.RedirectURI + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", q, resp.StatusCode)
		}
	}
	// The first result is queued; the second is dropped rather than blocking.
	if _, _, err := lb.Wait(context.Background(), "s", nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("a refused login must say why: %v", err)
	}
	paste := make(chan string)
	close(paste)
	go func() {
		time.Sleep(20 * time.Millisecond)
		r, err := http.Get(lb.RedirectURI + "?code=late&state=s")
		if err == nil {
			r.Body.Close()
		}
	}()
	if code, _, err := lb.Wait(context.Background(), "s", paste); err != nil || code != "late" {
		t.Errorf("a closed paste must leave the browser waiting: %q %v", code, err)
	}
	var none *Loopback
	none.Close()
	(&Loopback{}).Close()
	for _, in := range []string{"http://[::1/?code=x", "code=%zz"} {
		if _, err := ParseAuthCode(in, "s"); err == nil {
			t.Errorf("%q must be refused", in)
		}
	}
}

func TestTokenEndpointFailures(t *testing.T) {
	ctx := context.Background()
	p, _ := NewPKCE()
	refused := tokenServer(`{"error":"invalid_grant","error_description":"expired"}`, 400)
	errBody := tokenServer(`{"error":"invalid_grant","error_description":"expired"}`, 200)
	noToken := tokenServer(`{}`, 200)
	for name, fn := range map[string]func(*http.Client) error{
		"codex exchange": func(c *http.Client) error { _, err := CodexExchangeCode(ctx, c, "c", "", p); return err },
		"gemini exchange": func(c *http.Client) error {
			_, err := GeminiExchangeCode(ctx, c, GeminiClient{ID: "id"}, "c", "r", p, "proj")
			return err
		},
		"gemini refresh": func(c *http.Client) error { _, err := GeminiRefresh(ctx, c, Credential{RefreshToken: "r"}); return err },
		"qwen refresh":   func(c *http.Client) error { _, err := QwenRefresh(ctx, c, Credential{RefreshToken: "r"}); return err },
		"qwen poll":      func(c *http.Client) error { _, err := QwenPollToken(ctx, c, DeviceCode{}, p); return err },
		"qwen device":    func(c *http.Client) error { _, err := QwenRequestDeviceCode(ctx, c, p); return err },
	} {
		for kind, c := range map[string]*http.Client{"refused": refused, "error body": errBody, "no token": noToken} {
			if err := fn(c); err == nil {
				t.Errorf("%s (%s) must be an error", name, kind)
			}
		}
	}
	if err := func() error {
		_, err := CopilotPollAccessToken(ctx, noToken, CopilotConfig{}, DeviceCode{})
		return err
	}(); err == nil {
		t.Error("a poll that returns no token must be an error")
	}
	if _, err := CopilotRefreshCredential(ctx, refused, Credential{RefreshToken: "g"}); err == nil {
		t.Error("a refused Copilot exchange must be an error")
	}
}

func TestQwenDeviceCodeFallsBackToTheBareURL(t *testing.T) {
	p, _ := NewPKCE()
	c := tokenServer(`{"device_code":"d","user_code":"U","verification_uri":"https://chat.qwen.ai/authorize"}`, 200)
	dc, err := QwenRequestDeviceCode(context.Background(), c, p)
	if err != nil || dc.VerificationURL != "https://chat.qwen.ai/authorize" {
		t.Errorf("dc=%+v err=%v", dc, err)
	}
	if v, err := PollDevice(context.Background(), 1, nil, func() (int, error) { return 7, nil }); err != nil || v != 7 {
		t.Errorf("PollDevice: %v %v", v, err)
	}
}

func TestCodexExpiryFromTheToken(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	claims := fmt.Sprintf(`{"exp":%d,"https://api.openai.com/auth":{"chatgpt_account_id":"a"}}`, exp)
	jwt := "e30." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".s"
	c, err := CodexRefresh(context.Background(), tokenServer(`{"access_token":"`+jwt+`"}`, 200), Credential{RefreshToken: "r"})
	if err != nil || c.ExpiresAt.Unix() != exp {
		t.Errorf("c=%+v err=%v", c, err)
	}
	notJSON := "e30." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".s"
	if ChatGPTAccountID(notJSON) != "" || !jwtExpiry(notJSON).IsZero() {
		t.Error("a payload that is not JSON has no claims")
	}
}

func TestADCEdges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("APPDATA", dir)
	if got := ADCPath("/home/x"); got != "/home/x/.config/gcloud/application_default_credentials.json" {
		t.Errorf("no file under APPDATA: %q", got)
	}
	win := filepath.Join(dir, "gcloud", "application_default_credentials.json")
	_ = os.MkdirAll(filepath.Dir(win), 0o700)
	_ = os.WriteFile(win, []byte("{"), 0o600)
	if got := ADCPath("/home/x"); got != win {
		t.Errorf("APPDATA: %q", got)
	}
	if _, err := GeminiFromADC(win, ""); err == nil {
		t.Error("an unreadable ADC file must be an error")
	}
}

func TestRemainingTokenBranches(t *testing.T) {
	p, _ := NewPKCE()
	if _, err := QwenPollToken(context.Background(), tokenServer(`{}`, 500), DeviceCode{}, p); err == nil {
		t.Error("a server error without an OAuth error must be an error")
	}
	claims := `{"https://api.openai.com/auth":{"chatgpt_account_id":"a"}}`
	jwt := "e30." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".s"
	c, err := CodexRefresh(context.Background(), tokenServer(`{"access_token":"`+jwt+`","expires_in":60}`, 200), Credential{RefreshToken: "r"})
	if err != nil || time.Until(c.ExpiresAt) > time.Minute+time.Second || c.RefreshToken != "r" {
		t.Errorf("c=%+v err=%v", c, err)
	}
}

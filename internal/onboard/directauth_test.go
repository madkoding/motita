package onboard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/oauth"
)

// --- directauth.go tests ----------------------------------------------------

// fakeRT is a minimal RoundTripper for testing the OAuth flows without a network.
type onboardRT struct {
	responses map[string]func(*http.Request) (*http.Response, error)
}

func (rt *onboardRT) RoundTrip(req *http.Request) (*http.Response, error) {
	handler, ok := rt.responses[req.URL.Path]
	if !ok {
		handler, ok = rt.responses[req.URL.Host+req.URL.Path]
	}
	if !ok {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header)}, nil
	}
	return handler(req)
}

func jsonBody(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

// useAuthDir points the stored logins at a temporary directory.
func useAuthDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := authDir
	authDir = func() string { return dir }
	t.Cleanup(func() { authDir = old })
	return dir
}

func instantSleep(t *testing.T) {
	t.Helper()
	old := sleepFor
	sleepFor = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	oldPoll := oauth.PollSleep
	oauth.PollSleep = sleepFor
	t.Cleanup(func() { sleepFor = old; oauth.PollSleep = oldPoll })
}

func withTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	t.Cleanup(restore)
}

func resp200(body string) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: jsonBody(body), Header: make(http.Header)}, nil
}

// TestRunDirectAuthUnsupportedProvider: direct login for a provider that has none,
// Anthropic included (a Claude subscription goes through claude-code), is refused.
func TestRunDirectAuthUnsupportedProvider(t *testing.T) {
	for _, id := range []string{"openai", "anthropic"} {
		_, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")), Provider{ID: id})
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
}

// TestCopilotDirectAuthStoresARenewableLogin: the GitHub token is stored as the
// refresh token, so the short-lived Copilot token can be renewed at run time.
func TestCopilotDirectAuthStoresARenewableLogin(t *testing.T) {
	dir := useAuthDir(t)
	instantSleep(t)
	withTransport(t, &onboardRT{responses: map[string]func(*http.Request) (*http.Response, error){
		"/login/device/code": func(*http.Request) (*http.Response, error) {
			return resp200(`{"device_code":"dc","user_code":"XYZ-WUV","verification_uri":"https://github.com/login/device","interval":1,"expires_in":900}`)
		},
		"/login/oauth/access_token": func(*http.Request) (*http.Response, error) {
			return resp200(`{"access_token":"gho_tok","token_type":"bearer"}`)
		},
		"/copilot_internal/v2/token": func(*http.Request) (*http.Response, error) {
			return resp200(`{"token":"tid","expires_at":9999999999,"endpoints":{"api":"https://api.individual.githubcopilot.com"}}`)
		},
	}})
	var out bytes.Buffer
	key, err := runDirectAuth(context.Background(), &out, bufio.NewReader(strings.NewReader("")), Provider{ID: "copilot"})
	if err != nil || key != "" {
		t.Fatalf("key=%q err=%v (a login is stored, not returned as a key)", key, err)
	}
	if !strings.Contains(out.String(), "XYZ-WUV") {
		t.Error("the user code must be shown")
	}
	c, err := oauth.LoadCredential(dir, "copilot")
	if err != nil || c.RefreshToken != "gho_tok" || c.AccessToken != "tid" || c.BaseURL == "" {
		t.Errorf("stored %+v, %v", c, err)
	}
}

func TestCopilotDirectAuthReportsAMissingSubscription(t *testing.T) {
	useAuthDir(t)
	instantSleep(t)
	withTransport(t, &onboardRT{responses: map[string]func(*http.Request) (*http.Response, error){
		"/login/device/code": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 500, Body: jsonBody(`{"error":"server_error"}`), Header: make(http.Header)}, nil
		},
	}})
	if _, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")), Provider{ID: "copilot"}); err == nil || !strings.Contains(err.Error(), "copilot") {
		t.Fatalf("err = %v", err)
	}
	withTransport(t, &onboardRT{responses: map[string]func(*http.Request) (*http.Response, error){
		"/login/device/code":        func(*http.Request) (*http.Response, error) { return resp200(`{"device_code":"dc","interval":1}`) },
		"/login/oauth/access_token": func(*http.Request) (*http.Response, error) { return resp200(`{"access_token":"gho"}`) },
		"/copilot_internal/v2/token": func(*http.Request) (*http.Response, error) {
			return resp200(`{}`)
		},
	}})
	if _, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")), Provider{ID: "copilot"}); err == nil || !strings.Contains(err.Error(), "subscription") {
		t.Fatalf("err = %v", err)
	}
}

// TestQwenDirectAuthStoresTheResourceHost: the login names the host its token
// is valid for, and that host is what the client will use.
func TestQwenDirectAuthStoresTheResourceHost(t *testing.T) {
	dir := useAuthDir(t)
	instantSleep(t)
	polls := 0
	withTransport(t, &onboardRT{responses: map[string]func(*http.Request) (*http.Response, error){
		"/api/v1/oauth2/device/code": func(*http.Request) (*http.Response, error) {
			return resp200(`{"device_code":"dc","user_code":"QW-1","verification_uri_complete":"https://chat.qwen.ai/authorize?user_code=QW-1","expires_in":600}`)
		},
		"/api/v1/oauth2/token": func(*http.Request) (*http.Response, error) {
			polls++
			if polls == 1 {
				return &http.Response{StatusCode: 400, Body: jsonBody(`{"error":"slow_down"}`), Header: make(http.Header)}, nil
			}
			return resp200(`{"access_token":"qa","refresh_token":"qr","expires_in":3600,"resource_url":"portal.qwen.ai"}`)
		},
	}})
	var out bytes.Buffer
	if _, err := runDirectAuth(context.Background(), &out, bufio.NewReader(strings.NewReader("")), Provider{ID: "qwen"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "QW-1") {
		t.Error("the user code must be shown")
	}
	c, _ := oauth.LoadCredential(dir, "qwen")
	if c.BaseURL != "https://portal.qwen.ai/v1" || c.RefreshToken != "qr" {
		t.Errorf("stored %+v", c)
	}
}

// TestCodexDirectAuthAcceptsAPastedRedirect: on a machine the browser cannot
// reach, the user pastes the URL the browser ended on.
func TestCodexDirectAuthAcceptsAPastedRedirect(t *testing.T) {
	dir := useAuthDir(t)
	oldStart := startLoopback
	startLoopback = func(string, string, string) (*oauth.Loopback, error) {
		return nil, errors.New("port 1455 is busy")
	}
	t.Cleanup(func() { startLoopback = oldStart })
	oldRand := oauth.RandReader
	oauth.RandReader = strings.NewReader(strings.Repeat("a", 48))
	t.Cleanup(func() { oauth.RandReader = oldRand })
	pk, _ := oauth.NewPKCE()
	oauth.RandReader = strings.NewReader(strings.Repeat("a", 48))

	claims := `{"https://api.openai.com/auth":{"chatgpt_account_id":"acct-1"}}`
	jwt := "e30." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".sig"
	withTransport(t, &onboardRT{responses: map[string]func(*http.Request) (*http.Response, error){
		"/oauth/token": func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), "code=the-code") {
				t.Errorf("form = %s", b)
			}
			return resp200(`{"id_token":"` + jwt + `","access_token":"at","refresh_token":"rt","expires_in":3600}`)
		},
	}})
	pasted := "http://localhost:1455/auth/callback?code=the-code&state=" + pk.State + "\n"
	var out bytes.Buffer
	if _, err := runDirectAuth(context.Background(), &out, bufio.NewReader(strings.NewReader(pasted)), Provider{ID: "codex"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auth.openai.com/oauth/authorize") || !strings.Contains(out.String(), "port 1455 is busy") {
		t.Errorf("output = %s", out.String())
	}
	c, _ := oauth.LoadCredential(dir, "codex")
	if c.AccountID != "acct-1" || c.AccessToken != "at" {
		t.Errorf("stored %+v", c)
	}
}

// TestGeminiDirectAuthUsesGcloudCredentials: without a client of the user's own,
// gcloud's Application Default Credentials are used, and renewed once to prove them.
func TestGeminiDirectAuthUsesGcloudCredentials(t *testing.T) {
	dir := useAuthDir(t)
	adc := filepath.Join(t.TempDir(), "adc.json")
	_ = os.WriteFile(adc, []byte(`{"type":"authorized_user","client_id":"c","client_secret":"s","refresh_token":"r","quota_project_id":"qp"}`), 0o600)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	t.Setenv("MOTITA_GEMINI_CLIENT_ID", "")
	withTransport(t, &onboardRT{responses: map[string]func(*http.Request) (*http.Response, error){
		"/token": func(*http.Request) (*http.Response, error) {
			return resp200(`{"access_token":"ya29","expires_in":3600}`)
		},
	}})
	if _, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("\n")), Provider{ID: "gemini"}); err != nil {
		t.Fatal(err)
	}
	c, _ := oauth.LoadCredential(dir, "gemini")
	if c.ProjectID != "qp" || c.AccessToken != "ya29" || c.RefreshToken != "r" {
		t.Errorf("stored %+v", c)
	}
}

func TestGeminiDirectAuthExplainsWhatIsMissing(t *testing.T) {
	useAuthDir(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("MOTITA_GEMINI_CLIENT_ID", "")
	var out bytes.Buffer
	if _, err := runDirectAuth(context.Background(), &out, bufio.NewReader(strings.NewReader("\n")), Provider{ID: "gemini"}); err == nil {
		t.Fatal("no client and no gcloud login must be an error")
	}
	if !strings.Contains(out.String(), "MOTITA_GEMINI_CLIENT_ID") || !strings.Contains(out.String(), "gcloud auth application-default login") {
		t.Errorf("the way out must be explained: %s", out.String())
	}
	t.Setenv("MOTITA_GEMINI_CLIENT_ID", "cid")
	if _, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("\n")), Provider{ID: "gemini"}); err == nil {
		t.Error("an own client without a project must be refused")
	}
}

// TestWaitForCodeConsumesTheEnterAfterABrowserLogin: the reader started for a
// paste must not be left behind to swallow the next answer.
func TestWaitForCodeConsumesTheEnterAfterABrowserLogin(t *testing.T) {
	lb, err := oauth.StartLoopback("127.0.0.1:0", "127.0.0.1", "/cb")
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()
	pr, pw := io.Pipe()
	in := bufio.NewReader(pr)
	go func() {
		r, err := http.Get(lb.RedirectURI + "?code=c1&state=s")
		if err == nil {
			r.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(pw, "\nnext answer\n")
	}()
	code, err := waitForCode(context.Background(), io.Discard, in, lb, "s")
	if err != nil || code != "c1" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	line, _ := in.ReadString('\n')
	if line != "next answer\n" {
		t.Errorf("the next answer was swallowed: %q", line)
	}
}

func TestReadLineSuccess(t *testing.T) {
	line, err := readLine(context.Background(), bufio.NewReader(strings.NewReader("hello\n")))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.TrimSpace(line) != "hello" {
		t.Errorf("line = %q", line)
	}
}

// TestReadLineContextCancelled: readLine returns the context error when the
// context is cancelled before input is available.
func TestReadLineContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readLine(ctx, bufio.NewReader(strings.NewReader("")))
	if err == nil {
		t.Fatal("expected context error")
	}
}

// TestPrintDeviceInfo: the device info output contains the URL and code.
func TestPrintDeviceInfo(t *testing.T) {
	var out bytes.Buffer
	printDeviceInfo(&out, oauth.DeviceCode{
		VerificationURL: "https://example.com/device",
		UserCode:        "ABC-123",
	})
	s := out.String()
	if !strings.Contains(s, "https://example.com/device") {
		t.Error("URL not shown")
	}
	if !strings.Contains(s, "ABC-123") {
		t.Error("code not shown")
	}
}

// --- onboard.go coverage gaps -----------------------------------------------

// TestAskAPIKeyDirectAuthThreeAttempts: three invalid choices at the auth menu
// return an error.
func TestAskAPIKeyDirectAuthThreeAttempts(t *testing.T) {
	dir := t.TempDir()
	stubDirectAuth(t, "stub")
	// Provider=gemini(7), auth-choice: three invalid answers ("3","3","3") → error.
	_, _, err := run(context.Background(), t, dir, []string{"7", "3", "3", "3"}, Answers{})
	if err == nil {
		t.Fatal("expected error after three invalid auth choices")
	}
}

// TestAskAPIKeyDirectAuthDefaultChoice: pressing Enter at the auth menu selects
// direct auth (option 1, the default).
func TestAskAPIKeyDirectAuthDefaultChoice(t *testing.T) {
	dir := t.TempDir()
	stubDirectAuth(t, "default-direct")
	// Provider=gemini(7), auth-choice: Enter (default=1=direct auth), model=1, anchor=no check(3),
	// save.
	_, _, err := run(context.Background(), t, dir, []string{"7", "", "1", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// The stub returns "default-direct" as the key; it is written to the env file.
	env, _ := os.ReadFile(filepath.Join(dir, "config.env"))
	if !strings.Contains(string(env), "default-direct") {
		t.Errorf("env file does not contain the token: %s", env)
	}
}

// TestAskAPIKeyDirectAuthChoosePasteKey: choosing option 2 (paste key) goes to
// the traditional key entry path.
func TestAskAPIKeyDirectAuthChoosePasteKey(t *testing.T) {
	dir := t.TempDir()
	// Provider=gemini(7), auth-choice=2 (paste key), key="my-key", model=1, anchor=no check(3),
	// save.
	_, _, err := run(context.Background(), t, dir, []string{"7", "2", "my-key", "1", "3", ""}, Answers{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "config.env"))
	if !strings.Contains(string(env), "my-key") {
		t.Errorf("env file does not contain the key: %s", env)
	}
}

// TestAskAPIKeyDirectAuthAskError: when the reader runs out of input at the
// auth choice question, askAPIKey returns the error (not a cancellation).
func TestAskAPIKeyDirectAuthAskError(t *testing.T) {
	dir := t.TempDir()
	// Provider=gemini(7), auth-choice: EOF (no more input) → ask returns io.EOF.
	_, _, err := run(context.Background(), t, dir, []string{"7"}, Answers{})
	if err == nil {
		t.Fatal("expected error when input runs out at auth choice")
	}
}

// TestPrintPrompt: printPrompt writes the prompt with an arrow.
func TestPrintPrompt(t *testing.T) {
	var out bytes.Buffer
	printPrompt(&out, "Enter:")
	s := out.String()
	if !strings.Contains(s, "Enter:") {
		t.Errorf("output = %q", s)
	}
}

// TestStripANSI: stripANSI removes escape sequences.
func TestStripANSI(t *testing.T) {
	input := "\x1b[36mhello\x1b[0m world"
	got := stripANSI(input)
	if got != "hello world" {
		t.Errorf("got = %q", got)
	}
}

// TestStripANSIEmpty: stripANSI on empty string returns empty.
func TestStripANSIEmpty(t *testing.T) {
	if stripANSI("") != "" {
		t.Error("expected empty string")
	}
}

// TestStripANSINoEscape: stripANSI on a string without escapes returns it as-is.
func TestStripANSINoEscape(t *testing.T) {
	if stripANSI("plain text") != "plain text" {
		t.Error("expected plain text")
	}
}

// TestWriteFileAtomicMkdirError: writing to a path whose parent directory
// cannot be created returns an error.
func TestWriteFileAtomicMkdirError(t *testing.T) {
	// A path inside /proc cannot be created as a directory.
	err := writeFileAtomic("/proc/nonexistent-cannot-create/motita.yaml", []byte("test"))
	if err == nil {
		t.Fatal("expected error when directory cannot be created")
	}
}

// TestRunWriteConfigError: when writeFileAtomicFn fails on the config file,
// Run returns an error.
func TestRunWriteConfigError(t *testing.T) {
	dir := t.TempDir()
	old := writeFileAtomicFn
	writeFileAtomicFn = func(string, []byte) error { return errors.New("disk full") }
	t.Cleanup(func() { writeFileAtomicFn = old })
	_, _, err := run(context.Background(), t, dir, []string{"openai", "1", "3", "", "my-key"}, Answers{})
	if err == nil {
		t.Fatal("expected error when config write fails")
	}
}

// TestRunWriteCredError: when writeFileAtomicFn fails on the credentials file
// (but succeeds on config), Run returns an error.
func TestRunWriteCredError(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	old := writeFileAtomicFn
	writeFileAtomicFn = func(path string, content []byte) error {
		calls++
		if calls == 2 {
			return errors.New("cred write failed")
		}
		return old(path, content)
	}
	t.Cleanup(func() { writeFileAtomicFn = old })
	_, _, err := run(context.Background(), t, dir, []string{"openai", "1", "3", "", "my-key"}, Answers{})
	if err == nil {
		t.Fatal("expected error when credentials write fails")
	}
}

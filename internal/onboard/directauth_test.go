package onboard

import (
	"bufio"
	"bytes"
	"context"
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

// TestRunDirectAuthUnsupportedProvider: calling direct auth for a provider that
// does not support it returns an error immediately.
func TestRunDirectAuthUnsupportedProvider(t *testing.T) {
	_, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")), Provider{ID: "openai"})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err = %v", err)
	}
}

// TestRunDirectAuthDispatchGemini: runDirectAuth dispatches to geminiDirectAuth.
func TestRunDirectAuthDispatchGemini(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"device_code":"dc","user_code":"UC","verification_url":"https://x.com","interval":0,"expires_in":900}`), Header: make(http.Header)}, nil
			},
			"/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"access_token":"tok","token_type":"Bearer","expires_in":3600}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()
	tok, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")), Provider{ID: "gemini"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "tok" {
		t.Errorf("token = %q", tok)
	}
}

// TestRunDirectAuthDispatchCopilot: runDirectAuth dispatches to copilotDirectAuth.
func TestRunDirectAuthDispatchCopilot(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/login/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"device_code":"dc","user_code":"UC","verification_uri":"https://x.com","interval":0,"expires_in":900}`), Header: make(http.Header)}, nil
			},
			"/login/oauth/access_token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"access_token":"gho_tok","token_type":"bearer","scope":"read:user"}`), Header: make(http.Header)}, nil
			},
			"/copilot_internal/v2/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"token":"tid","expires_at":9999999999}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()
	tok, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")), Provider{ID: "copilot"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "gho_tok" {
		t.Errorf("token = %q", tok)
	}
}

// TestRunDirectAuthDispatchAnthropic: runDirectAuth dispatches to anthropicDirectAuth.
func TestRunDirectAuthDispatchAnthropic(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/v1/oauth/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"access_token":"sk-ant-tok","token_type":"bearer","expires_in":28800}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()
	tok, err := runDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("code123\n")), Provider{ID: "anthropic"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "sk-ant-tok" {
		t.Errorf("token = %q", tok)
	}
}

// TestGeminiDirectAuthSuccess: the full Gemini device flow completes when the
// poll returns a token.
func TestGeminiDirectAuthSuccess(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"device_code":"dc123","user_code":"ABC-DEF","verification_url":"https://example.com/device","interval":0,"expires_in":900}`), Header: make(http.Header)}, nil
			},
			"/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"access_token":"ya29.test-token","token_type":"Bearer","expires_in":3600,"refresh_token":"ref123"}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	var out bytes.Buffer
	tok, err := geminiDirectAuth(context.Background(), &out, bufio.NewReader(strings.NewReader("")))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "ya29.test-token" {
		t.Errorf("token = %q", tok)
	}
	if !strings.Contains(out.String(), "ABC-DEF") {
		t.Error("user code not shown")
	}
}

// TestGeminiDirectAuthDeviceCodeError: when the device code request fails, the
// flow returns an error.
func TestGeminiDirectAuthDeviceCodeError(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 500, Body: jsonBody(`{"error":"server_error"}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	_, err := geminiDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")))
	if err == nil || !strings.Contains(err.Error(), "device flow") {
		t.Fatalf("err = %v", err)
	}
}

// TestGeminiDirectAuthContextCancelled: when the context is cancelled while
// polling, the flow returns the context error.
func TestGeminiDirectAuthContextCancelled(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"device_code":"dc123","user_code":"ABC-DEF","verification_url":"https://example.com/device","interval":1,"expires_in":900}`), Header: make(http.Header)}, nil
			},
			"/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 400, Body: jsonBody(`{"error":"authorization_pending"}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := geminiDirectAuth(ctx, io.Discard, bufio.NewReader(strings.NewReader("")))
	if err == nil {
		t.Fatal("expected context deadline error")
	}
}

// TestGeminiDirectAuthPollError: when the poll returns a non-recoverable error,
// the flow returns it.
func TestGeminiDirectAuthPollError(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"device_code":"dc123","user_code":"ABC-DEF","verification_url":"https://example.com/device","interval":0,"expires_in":900}`), Header: make(http.Header)}, nil
			},
			"/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 400, Body: jsonBody(`{"error":"access_denied"}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	_, err := geminiDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")))
	if err == nil {
		t.Fatal("expected access_denied error")
	}
}

// TestGeminiDirectAuthSlowDown: the slow_down error increases the interval but
// the flow continues; a subsequent poll succeeds.
func TestGeminiDirectAuthSlowDown(t *testing.T) {
	calls := 0
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"device_code":"dc123","user_code":"ABC-DEF","verification_url":"https://example.com/device","interval":0,"expires_in":900}`), Header: make(http.Header)}, nil
			},
			"/token": func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 400, Body: jsonBody(`{"error":"slow_down"}`), Header: make(http.Header)}, nil
				}
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"access_token":"ya29.slow","token_type":"Bearer","expires_in":3600}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	// Override the sleep to be instant in tests.
	oldAfter := sleepFor
	sleepFor = func(_ time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	t.Cleanup(func() { sleepFor = oldAfter })

	tok, err := geminiDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "ya29.slow" {
		t.Errorf("token = %q", tok)
	}
}

// TestGeminiDirectAuthPendingThenSuccess: the authorization_pending error makes
// the flow wait and retry; a subsequent poll succeeds.
func TestGeminiDirectAuthPendingThenSuccess(t *testing.T) {
	calls := 0
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"device_code":"dc123","user_code":"ABC-DEF","verification_url":"https://example.com/device","interval":0,"expires_in":900}`), Header: make(http.Header)}, nil
			},
			"/token": func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 400, Body: jsonBody(`{"error":"authorization_pending"}`), Header: make(http.Header)}, nil
				}
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"access_token":"ya29.pending","token_type":"Bearer","expires_in":3600}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	oldAfter := sleepFor
	sleepFor = func(_ time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	t.Cleanup(func() { sleepFor = oldAfter })

	tok, err := geminiDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "ya29.pending" {
		t.Errorf("token = %q", tok)
	}
}

// TestCopilotDirectAuthSuccess: the full Copilot flow completes.
func TestCopilotDirectAuthSuccess(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/login/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"device_code":"dc456","user_code":"XYZ-WUV","verification_uri":"https://github.com/login/device","interval":0,"expires_in":900}`), Header: make(http.Header)}, nil
			},
			"/login/oauth/access_token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"access_token":"gho_test-copilot","token_type":"bearer","scope":"read:user"}`), Header: make(http.Header)}, nil
			},
			"/copilot_internal/v2/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"token":"tid:test-copilot","expires_at":9999999999}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	oldAfter := sleepFor
	sleepFor = func(_ time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	t.Cleanup(func() { sleepFor = oldAfter })

	var out bytes.Buffer
	tok, err := copilotDirectAuth(context.Background(), &out, bufio.NewReader(strings.NewReader("")))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "gho_test-copilot" {
		t.Errorf("token = %q, want gho_test-copilot", tok)
	}
	if !strings.Contains(out.String(), "XYZ-WUV") {
		t.Error("user code not shown")
	}
}

// TestCopilotDirectAuthDeviceCodeError: device code request failure.
func TestCopilotDirectAuthDeviceCodeError(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/login/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 500, Body: jsonBody(`{"error":"server_error"}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	_, err := copilotDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")))
	if err == nil || !strings.Contains(err.Error(), "copilot") {
		t.Fatalf("err = %v", err)
	}
}

// TestAnthropicDirectAuthSuccess: the PKCE flow with a pasted code.
func TestAnthropicDirectAuthSuccess(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/v1/oauth/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"access_token":"sk-ant-oauth-test","token_type":"bearer","expires_in":28800,"refresh_token":"rt123"}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	var out bytes.Buffer
	tok, err := anthropicDirectAuth(context.Background(), &out, bufio.NewReader(strings.NewReader("my-auth-code\n")))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "sk-ant-oauth-test" {
		t.Errorf("token = %q", tok)
	}
}

// TestAnthropicDirectAuthEmptyCode: pasting an empty code returns an error.
func TestAnthropicDirectAuthEmptyCode(t *testing.T) {
	_, err := anthropicDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("\n")))
	if err == nil || !strings.Contains(err.Error(), "no code") {
		t.Fatalf("err = %v", err)
	}
}

// TestAnthropicDirectAuthExchangeError: when the exchange fails, an error is returned.
func TestAnthropicDirectAuthExchangeError(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/v1/oauth/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 400, Body: jsonBody(`{"error":"invalid_grant"}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	_, err := anthropicDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("bad-code\n")))
	if err == nil || !strings.Contains(err.Error(), "exchange") {
		t.Fatalf("err = %v", err)
	}
}

// TestGeminiDirectAuthSlowDownContextCancelled: when the context is cancelled
// during a slow_down wait, the flow returns the context error.
func TestGeminiDirectAuthSlowDownContextCancelled(t *testing.T) {
	rt := &onboardRT{
		responses: map[string]func(*http.Request) (*http.Response, error){
			"/device/code": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: jsonBody(`{"device_code":"dc","user_code":"UC","verification_url":"https://x.com","interval":0,"expires_in":900}`), Header: make(http.Header)}, nil
			},
			"/token": func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 400, Body: jsonBody(`{"error":"slow_down"}`), Header: make(http.Header)}, nil
			},
		},
	}
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := geminiDirectAuth(ctx, io.Discard, bufio.NewReader(strings.NewReader("")))
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
}

// TestAnthropicDirectAuthAuthorizeError: when the PKCE generation fails, the
// flow returns an error.
func TestAnthropicDirectAuthAuthorizeError(t *testing.T) {
	old := oauth.RandReader
	oauth.RandReader = &eofReader{}
	t.Cleanup(func() { oauth.RandReader = old })

	_, err := anthropicDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("code\n")))
	if err == nil || !strings.Contains(err.Error(), "auth flow") {
		t.Fatalf("err = %v", err)
	}
}

// TestAnthropicDirectAuthReadLineError: when reading the code from the user
// fails (EOF without newline), the flow returns an error.
func TestAnthropicDirectAuthReadLineError(t *testing.T) {
	_, err := anthropicDirectAuth(context.Background(), io.Discard, bufio.NewReader(strings.NewReader("")))
	if err == nil {
		t.Fatal("expected readLine error")
	}
}

// failingReader always returns an error.
type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }
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

// TestSayRaw: sayRaw writes without a trailing newline.
func TestSayRaw(t *testing.T) {
	var out bytes.Buffer
	s := &session{out: &out}
	s.sayRaw("hello %s", "world")
	if out.String() != "hello world" {
		t.Errorf("output = %q", out.String())
	}
}

// TestAskAPIKeyDirectAuthThreeAttempts: three invalid choices at the auth menu
// return an error.
func TestAskAPIKeyDirectAuthThreeAttempts(t *testing.T) {
	dir := t.TempDir()
	stubDirectAuth(t, "stub")
	// Provider=anthropic(5), model=1, anchor=always-pass(2), baseURL=default,
	// auth-choice: three invalid answers ("3","3","3") → error.
	_, _, err := run(context.Background(), t, dir, []string{"5", "1", "2", "", "3", "3", "3"}, Answers{})
	if err == nil {
		t.Fatal("expected error after three invalid auth choices")
	}
}

// TestAskAPIKeyDirectAuthDefaultChoice: pressing Enter at the auth menu selects
// direct auth (option 1, the default).
func TestAskAPIKeyDirectAuthDefaultChoice(t *testing.T) {
	dir := t.TempDir()
	stubDirectAuth(t, "default-direct")
	// Provider=anthropic(5), model=1, anchor=always-pass(2), baseURL=default,
	// auth-choice: Enter (default=1=direct auth).
	_, _, err := run(context.Background(), t, dir, []string{"5", "1", "2", "", ""}, Answers{})
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
	// Provider=anthropic(5), model=1, anchor=always-pass(2), baseURL=default,
	// auth-choice=2 (paste key), key="my-key".
	_, _, err := run(context.Background(), t, dir, []string{"5", "1", "2", "", "2", "my-key"}, Answers{})
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
	// Provider=anthropic(5), model=1, anchor=always-pass(2), baseURL=default,
	// auth-choice: EOF (no more input) → ask returns io.EOF.
	_, _, err := run(context.Background(), t, dir, []string{"5", "1", "2", ""}, Answers{})
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

package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/oauth"
)

// loginRT answers the providers' OAuth endpoints by path, so the real flows run with no network.
type loginRT map[string]func(*http.Request) (*http.Response, error)

func (rt loginRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if h, ok := rt[r.URL.Path]; ok {
		return h(r)
	}
	return answer(404, `{"error":"not_found"}`)
}

func answer(code int, body string) (*http.Response, error) {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

func ok200(body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) { return answer(200, body) }
}

// useOAuth routes every OAuth request through rt and makes the device polls instant.
func useOAuth(t *testing.T, rt loginRT) {
	t.Helper()
	restore := oauth.SetDefaultClient(func() *http.Client { return &http.Client{Transport: rt} })
	old := oauth.PollSleep
	oauth.PollSleep = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	t.Cleanup(func() {
		restore()
		oauth.PollSleep = old
	})
}

// failingRand makes NewPKCE fail.
func failingRand(t *testing.T) {
	t.Helper()
	old := oauth.RandReader
	oauth.RandReader = strings.NewReader("")
	t.Cleanup(func() { oauth.RandReader = old })
}

const copilotDevice = `{"device_code":"dc","user_code":"AB-12",` +
	`"verification_uri":"https://github.com/login/device","interval":1}`

func TestCopilotLogin(t *testing.T) {
	useOAuth(t, loginRT{})
	if _, err := copilotLogin(context.Background()); err == nil {
		t.Error("a refused device code is an error")
	}
	useOAuth(t, loginRT{
		"/login/device/code":         ok200(copilotDevice),
		"/login/oauth/access_token":  ok200(`{"access_token":"gho"}`),
		"/copilot_internal/v2/token": ok200(`{"token":"tid","expires_at":9999999999}`),
	})
	f, err := copilotLogin(context.Background())
	if err != nil || f.Method != "device" || f.Code != "AB-12" {
		t.Fatalf("%+v %v", f, err)
	}
	cred, err := f.Wait(context.Background(), nil)
	if err != nil || cred.RefreshToken != "gho" || cred.AccessToken != "tid" {
		t.Errorf("%+v %v", cred, err)
	}
	useOAuth(t, loginRT{
		"/login/device/code":         ok200(copilotDevice),
		"/login/oauth/access_token":  ok200(`{"access_token":"gho"}`),
		"/copilot_internal/v2/token": ok200(`{}`),
	})
	f, _ = copilotLogin(context.Background())
	if _, err := f.Wait(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "subscription") {
		t.Errorf("no Copilot token: %v", err)
	}
	useOAuth(t, loginRT{
		"/login/device/code":        ok200(copilotDevice),
		"/login/oauth/access_token": ok200(`{"error":"access_denied"}`),
	})
	f, _ = copilotLogin(context.Background())
	if _, err := f.Wait(context.Background(), nil); err == nil {
		t.Error("a denied login is an error")
	}
}

func TestCodexLoginTakesThePastedAddress(t *testing.T) {
	old := startCodexLoopback
	startCodexLoopback = func(string, string, string) (*oauth.Loopback, error) { return nil, errors.New("busy") }
	t.Cleanup(func() { startCodexLoopback = old })
	claims := `{"https://api.openai.com/auth":{"chatgpt_account_id":"acct"}}`
	jwt := "e30." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".sig"
	useOAuth(t, loginRT{"/oauth/token": ok200(`{"id_token":"` + jwt + `","access_token":"at","refresh_token":"rt"}`)})
	f, err := codexLogin(context.Background())
	if err != nil || f.Method != "code" || !strings.Contains(f.URL, "auth.openai.com") {
		t.Fatalf("%+v %v", f, err)
	}
	state := f.URL[strings.Index(f.URL, "state=")+len("state="):]
	if i := strings.IndexByte(state, '&'); i >= 0 {
		state = state[:i]
	}
	pasted := make(chan string, 1)
	pasted <- "http://localhost:1455/auth/callback?code=c&state=" + state
	if cred, err := f.Wait(context.Background(), pasted); err != nil || cred.AccountID != "acct" {
		t.Errorf("%+v %v", cred, err)
	}
	f, _ = codexLogin(context.Background())
	closed := make(chan string)
	close(closed)
	if _, err := f.Wait(context.Background(), closed); err == nil {
		t.Error("no code, no login")
	}
	failingRand(t)
	if _, err := codexLogin(context.Background()); err == nil {
		t.Error("no randomness, no login")
	}
}

func refusalCode(err error) string {
	var r *loginRefusal
	if errors.As(err, &r) {
		if strings.ContainsAny(r.msg, "/`") || strings.Contains(r.msg, "gcloud") {
			return "leaks:" + r.msg
		}
		return r.code
	}
	return ""
}

func TestGeminiLoginUsesGcloud(t *testing.T) {
	adc := filepath.Join(t.TempDir(), "adc.json")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	_, err := geminiLogin(context.Background())
	if c := refusalCode(err); c != "gemini_no_gcloud" || !strings.Contains(err.Error(), "AI Studio key") {
		t.Errorf("no gcloud login: %q %v", c, err)
	}
	_ = os.WriteFile(adc, []byte(`not json`), 0o600)
	_, err = geminiLogin(context.Background())
	if c := refusalCode(err); c != "gemini_bad_login" {
		t.Errorf("unreadable login: %q %v", c, err)
	}
	_ = os.WriteFile(adc, []byte(`{"type":"authorized_user","refresh_token":"r"}`), 0o600)
	_, err = geminiLogin(context.Background())
	if c := refusalCode(err); c != "gemini_no_project" {
		t.Errorf("no project: %q %v", c, err)
	}
	_ = os.WriteFile(adc, []byte(`{"type":"authorized_user","refresh_token":"r","quota_project_id":"qp"}`), 0o600)
	useOAuth(t, loginRT{})
	_, err = geminiLogin(context.Background())
	if c := refusalCode(err); c != "gemini_expired" {
		t.Errorf("a refresh that fails: %q %v", c, err)
	}
	useOAuth(t, loginRT{"/token": ok200(`{"access_token":"ya29","expires_in":3600}`)})
	f, err := geminiLogin(context.Background())
	if err != nil || f.Method != "instant" {
		t.Fatalf("%+v %v", f, err)
	}
	if cred, _ := f.Wait(context.Background(), nil); cred.ProjectID != "qp" {
		t.Errorf("%+v", cred)
	}
}

// The gemini login is only offered where gcloud's login exists, and a refusal reaches the page
// with its code and no path.
func TestGeminiLoginOfferedOnlyWithGcloud(t *testing.T) {
	adc := filepath.Join(t.TempDir(), "adc.json")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	srv, _ := llmServer(t, filepath.Join(t.TempDir(), "c.yaml"))
	offered := func() bool {
		return providerOf(decodeLLM(t, llmCall(t, srv, http.MethodGet, "/v1/llm", "")), "gemini").SupportsLogin
	}
	if offered() {
		t.Error("no gcloud login, no sign-in button")
	}
	w := llmCall(t, srv, http.MethodPost, "/v1/llm/providers/gemini/login", "")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), `"code":"gemini_no_gcloud"`) ||
		strings.Contains(w.Body.String(), "gcloud ") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	_ = os.WriteFile(adc, []byte(`{}`), 0o600)
	if !offered() {
		t.Error("a gcloud login offers the sign-in button")
	}
}

// fakeStarters replaces the login table for one test.
func fakeStarters(t *testing.T, m map[string]func(context.Context) (loginFlow, error)) {
	t.Helper()
	old := llmLoginStarters
	llmLoginStarters = m
	t.Cleanup(func() { llmLoginStarters = old })
}

func decodeLogin(t *testing.T, w *httptest.ResponseRecorder) loginView {
	t.Helper()
	var v loginView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return v
}

// waitStatus polls a login until it leaves pending.
func waitStatus(t *testing.T, srv *Server, id string) loginView {
	t.Helper()
	for i := 0; i < 500; i++ {
		v := decodeLogin(t, get(t, srv, "/v1/llm/logins/"+id, testToken))
		if v.Status != "pending" {
			return v
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the login never ended")
	return loginView{}
}

func TestLLMLoginOverHTTP(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, svc := llmServer(t, path)
	fakeStarters(t, fakeLoginFlows())
	id := checkCodeLogin(t, srv, svc)
	checkDeviceLogin(t, srv)
	checkOtherLogins(t, srv, id)
	checkLoginForget(t, srv)
}

// fakeLoginFlows is one flow of each kind: a pasted code, a device code, a failing start and an
// instant login.
func fakeLoginFlows() map[string]func(context.Context) (loginFlow, error) {
	return map[string]func(context.Context) (loginFlow, error){
		"codex": func(context.Context) (loginFlow, error) {
			return loginFlow{Method: "code", URL: "https://auth.example/authorize",
				Wait: func(ctx context.Context, pasted <-chan string) (oauth.Credential, error) {
					select {
					case code := <-pasted:
						return oauth.Credential{AccessToken: "at-" + code, RefreshToken: "r"}, nil
					case <-ctx.Done():
						return oauth.Credential{}, ctx.Err()
					}
				}}, nil
		},
		"copilot": func(context.Context) (loginFlow, error) {
			return loginFlow{Method: "device", URL: "https://github.com/login/device", Code: "AB-12",
				Wait: func(ctx context.Context, _ <-chan string) (oauth.Credential, error) {
					<-ctx.Done()
					return oauth.Credential{}, ctx.Err()
				}}, nil
		},
		"openai": func(context.Context) (loginFlow, error) { return loginFlow{}, errors.New("the provider is down") },
		"gemini": func(context.Context) (loginFlow, error) {
			return loginFlow{Method: "instant", Wait: func(context.Context, <-chan string) (oauth.Credential, error) {
				return oauth.Credential{RefreshToken: "g", ProjectID: "p"}, nil
			}}, nil
		},
	}
}

// checkCodeLogin runs the code flow to its end and returns its id.
func checkCodeLogin(t *testing.T, srv *Server, svc *refreshingService) string {
	t.Helper()
	// A code flow: the page shows the link, the pasted address finishes it.
	v := decodeLogin(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/codex/login", ""))
	if v.Status != "pending" || v.Method != "code" || v.URL == "" {
		t.Fatalf("start: %+v", v)
	}
	for body, code := range map[string]int{`not json`: 400, `{"code":" "}`: 400} {
		if w := llmCall(t, srv, http.MethodPost, "/v1/llm/logins/"+v.ID+"/paste", body); w.Code != code {
			t.Errorf("paste %s: %d", body, w.Code)
		}
	}
	paste := "/v1/llm/logins/" + v.ID + "/paste"
	if w := llmCall(t, srv, http.MethodPost, paste, `{"code":"c1"}`); w.Code != http.StatusAccepted {
		t.Fatalf("paste: %d", w.Code)
	}
	if got := waitStatus(t, srv, v.ID); got.Status != "connected" {
		t.Fatalf("end: %+v", got)
	}
	if !config.HasLogin("codex") {
		t.Error("the login is stored where the setup stores it")
	}
	found := false
	for _, r := range svc.refreshed {
		found = found || strings.HasPrefix(r, "codex|")
	}
	if !found {
		t.Errorf("open sessions are told: %v", svc.refreshed)
	}
	if p := providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), "codex"); p.Key != config.KeyLogin {
		t.Errorf("codex = %+v", p)
	}
	return v.ID
}

// checkDeviceLogin starts the device flow and cancels it.
func checkDeviceLogin(t *testing.T, srv *Server) {
	t.Helper()
	// A device flow is cancelled; a device flow takes no pasted code.
	d := decodeLogin(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/copilot/login", ""))
	if d.Code != "AB-12" {
		t.Errorf("device: %+v", d)
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/llm/logins/"+d.ID+"/paste", `{"code":"x"}`); w.Code != 400 {
		t.Errorf("paste on a device flow: %d", w.Code)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/llm/logins/"+d.ID, ""); w.Code != http.StatusNoContent {
		t.Errorf("cancel: %d", w.Code)
	}
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		if w := llmCall(t, srv, m, "/v1/llm/logins/"+d.ID, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s after cancel: %d", m, w.Code)
		}
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/llm/logins/nope/paste", `{"code":"x"}`); w.Code != 404 {
		t.Errorf("paste to nothing: %d", w.Code)
	}
}

// checkOtherLogins runs the instant login and the ones that cannot start; codexID is the finished
// code login the next one prunes.
func checkOtherLogins(t *testing.T, srv *Server, codexID string) {
	t.Helper()
	// An instant login answers its end; the finished codex login is let go.
	g := decodeLogin(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/gemini/login", ""))
	if g.Status != "connected" {
		t.Errorf("instant: %+v", g)
	}
	if w := get(t, srv, "/v1/llm/logins/"+codexID, testToken); w.Code != http.StatusNotFound {
		t.Errorf("a finished login is pruned by the next one: %d", w.Code)
	}

	if w := llmCall(t, srv, http.MethodPost, "/v1/llm/providers/openai/login", ""); w.Code != http.StatusBadGateway {
		t.Errorf("a provider that fails to start: %d", w.Code)
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/llm/providers/anthropic/login", ""); w.Code != http.StatusBadRequest {
		t.Errorf("no sign-in: %d", w.Code)
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/llm/providers/nope/login", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", w.Code)
	}
}

// checkLoginForget disconnects the providers signed in with a login.
func checkLoginForget(t *testing.T, srv *Server) {
	t.Helper()
	// Disconnecting forgets the login.
	w := llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/codex", "")
	if w.Code != http.StatusOK || config.HasLogin("codex") {
		t.Errorf("forget: %d %v", w.Code, config.HasLogin("codex"))
	}
	old := deleteLogin
	deleteLogin = func(string, string) error { return errors.New("read-only") }
	w = llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/gemini", "")
	deleteLogin = old
	if w.Code != http.StatusInternalServerError {
		t.Errorf("a login that cannot be removed: %d", w.Code)
	}
}

func TestLLMLoginThatCannotBeSaved(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	fakeStarters(t, map[string]func(context.Context) (loginFlow, error){
		"gemini": func(context.Context) (loginFlow, error) {
			return loginFlow{Method: "instant", Wait: func(context.Context, <-chan string) (oauth.Credential, error) {
				return oauth.Credential{RefreshToken: "g"}, nil
			}}, nil
		},
	})
	old := saveLogin
	saveLogin = func(string, oauth.Credential) error { return errors.New("disk full") }
	defer func() { saveLogin = old }()
	v := decodeLogin(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/gemini/login", ""))
	if v.Status != "failed" || !strings.Contains(v.Error, "disk full") {
		t.Errorf("%+v", v)
	}
}

func TestLLMLoginPasteWhileOneIsWaiting(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	release := make(chan struct{})
	fakeStarters(t, map[string]func(context.Context) (loginFlow, error){
		"codex": func(context.Context) (loginFlow, error) {
			return loginFlow{Method: "code", Wait: func(ctx context.Context, _ <-chan string) (oauth.Credential, error) {
				<-release
				return oauth.Credential{}, errors.New("refused")
			}}, nil
		},
	})
	v := decodeLogin(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/codex/login", ""))
	for i := 0; i < 2; i++ {
		paste := "/v1/llm/logins/" + v.ID + "/paste"
		if w := llmCall(t, srv, http.MethodPost, paste, `{"code":"x"}`); w.Code != http.StatusAccepted {
			t.Errorf("paste %d: %d", i, w.Code)
		}
	}
	close(release)
	if got := waitStatus(t, srv, v.ID); got.Status != "failed" || got.Error != "refused" {
		t.Errorf("%+v", got)
	}
}

func TestLLMKeyHintAndListedModels(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	if err := config.SetCredential(path, "OPENAI_API_KEY", "sk-proj-0123456789abcd"); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCredential(path, "ANTHROPIC_API_KEY", "short"); err != nil {
		t.Fatal(err)
	}
	srv, _ := llmServer(t, path)
	v := decodeLLM(t, get(t, srv, "/v1/llm", testToken))
	if p := providerOf(v, "openai"); p.KeyHint != "…abcd" {
		t.Errorf("openai = %+v", p)
	}
	if p := providerOf(v, "anthropic"); p.Key != config.KeyStored || p.KeyHint != "" {
		t.Errorf("a short key shows nothing: %+v", p)
	}
	if strings.Contains(get(t, srv, "/v1/llm", testToken).Body.String(), "0123456789") {
		t.Error("only the last four characters leave the gateway")
	}

	old := listProviderModels
	listProviderModels = func(context.Context, config.LLM) ([]string, error) { return []string{"z-model", "a-model"}, nil }
	defer func() { listProviderModels = old }()
	llmCall(t, srv, http.MethodPost, "/v1/llm/providers/openai/test", `{}`)
	w := get(t, srv, "/v1/sessions/"+DefaultSession+"/providers", testToken)
	var got struct {
		Providers []providerInfo `json:"providers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	for _, p := range got.Providers {
		if p.ID == "openai" && strings.Join(p.Models, ",") != "a-model,z-model" {
			t.Errorf("the session picker offers what the test listed: %+v", p)
		}
		if p.ID == "anthropic" && (len(p.Models) == 0 || p.Models[0] == "a-model") {
			t.Errorf("a provider never tested keeps its catalogue: %+v", p)
		}
	}
}

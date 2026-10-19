package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/oauth"
	"github.com/madkoding/motita/internal/onboard"
)

// refreshingService is the fake that also takes a provider's new key, like the production runner.
type refreshingService struct {
	fakeService
	refreshed []string
}

func (r *refreshingService) RefreshProvider(provider, apiKey, baseURL string) {
	r.refreshed = append(r.refreshed, provider+"|"+apiKey+"|"+baseURL)
	if strings.EqualFold(r.cfg.LLM.Provider, provider) {
		r.cfg.LLM.APIKey, r.cfg.LLM.BaseURL = apiKey, baseURL
	}
}

// llmEnv clears every key source and gives the test its own home, auth directory and
// configuration file with the body given. It returns the file's path.
func llmEnv(t *testing.T, body string) string {
	t.Helper()
	for _, v := range []string{"MOTITA_LLM_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY",
		"DASHSCOPE_API_KEY", "OLLAMA_API_KEY", "GITHUB_COPILOT_TOKEN", "MOTITA_LLM_PROVIDER", "MOTITA_LLM_MODEL",
		"MOTITA_LLM_BASE_URL", "OPENAI_BASE_URL"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MOTITA_AUTH_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config.UseCredentialsBeside(path)
	t.Cleanup(func() { config.UseCredentialsBeside("") })
	return path
}

const openAIConfig = "llm:\n  provider: openai\n  model: gpt-5-mini\n  base_url: https://api.openai.com/v1\n"

// llmCall sends one request with the bearer token and a JSON body, and returns the recorder.
func llmCall(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func decodeLLM(t *testing.T, w *httptest.ResponseRecorder) llmView {
	t.Helper()
	var v llmView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body.String(), err)
	}
	return v
}

func providerOf(v llmView, id string) llmProviderView {
	for _, p := range v.Providers {
		if p.ID == id {
			return p
		}
	}
	return llmProviderView{}
}

func llmServer(t *testing.T, path string) (*Server, *refreshingService) {
	t.Helper()
	svc := &refreshingService{}
	svc.cfg = config.Default()
	svc.cfg.LLM.Provider, svc.cfg.LLM.Model = "openai", "gpt-5-mini"
	srv := newTestServer(t, svc, func(o *Options) {
		o.ConfigPath = path
		o.NewService = func() (Service, error) {
			n := &refreshingService{}
			n.cfg = config.Default()
			return n, nil
		}
	})
	return srv, svc
}

// checkQwenHasNoLogin: Qwen ended its OAuth login on 2026-04-15, so only the DashScope key is offered,
// with its link.
func checkQwenHasNoLogin(t *testing.T, srv *Server, v llmView) {
	t.Helper()
	if p := providerOf(v, "qwen"); p.SupportsLogin || p.ConsoleURL == "" || p.Key != config.KeyMissing {
		t.Errorf("qwen = %+v", p)
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/llm/providers/qwen/login", ""); w.Code != http.StatusBadRequest {
		t.Errorf("qwen has no sign-in to start: %d", w.Code)
	}
}

func TestLLMListsEveryProviderWithoutAFile(t *testing.T) {
	llmEnv(t, "")
	svc := &fakeService{}
	svc.cfg = config.Default()
	svc.cfg.LLM.Provider, svc.cfg.LLM.Model = "anthropic", "claude-sonnet-5-5"
	srv := newTestServer(t, svc)
	v := decodeLLM(t, get(t, srv, "/v1/llm", testToken))
	if v.Writable || v.Provider != "anthropic" || len(v.Providers) != len(onboard.Providers()) {
		t.Fatalf("view = %+v", v)
	}
	if p := providerOf(v, "claude-code"); p.Key != config.KeyNotNeeded || p.Login == "" || len(p.Models) == 0 {
		t.Errorf("claude-code = %+v", p)
	}
	if p := providerOf(v, "anthropic"); !p.Default || p.Key != config.KeyMissing || p.AskBaseURL {
		t.Errorf("anthropic = %+v", p)
	}
	if p := providerOf(v, "ollama"); !p.AskBaseURL || p.BaseURL != "" || p.DefaultBaseURL != "http://localhost:11434/v1" {
		t.Errorf("ollama = %+v", p)
	}
	p := providerOf(v, "ollama-cloud")
	if p.AskBaseURL || p.BaseURL != "https://ollama.com/v1" || p.Key != config.KeyMissing {
		t.Errorf("ollama-cloud = %+v", p)
	}
	checkQwenHasNoLogin(t, srv, v)
	for _, w := range []*httptest.ResponseRecorder{
		llmCall(t, srv, http.MethodPut, "/v1/llm/providers/anthropic", `{"api_key":"k"}`),
		llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/anthropic", ""),
		llmCall(t, srv, http.MethodPut, "/v1/llm/default", `{"provider":"anthropic","model":"m"}`),
	} {
		if w.Code != http.StatusConflict {
			t.Errorf("no file to save in: %d %s", w.Code, w.Body.String())
		}
	}
	if w := get(t, srv, "/v1/llm", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the list needs the token: %d", w.Code)
	}
}

func TestLLMSavesAKeyWithoutEverSendingItBack(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	if err := config.SetCredential(path, "MOTITA_LLM_API_KEY", "sk-old"); err != nil {
		t.Fatal(err)
	}
	srv, svc := llmServer(t, path)
	v := decodeLLM(t, get(t, srv, "/v1/llm", testToken))
	if p := providerOf(v, "openai"); !v.Writable || !p.Default || p.Key != config.KeyStored {
		t.Fatalf("openai = %+v", p)
	}

	w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/anthropic", `{"api_key":" sk-ant-secret "}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "sk-ant") {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if got := config.StoredCredentials(path)["ANTHROPIC_API_KEY"]; got != "sk-ant-secret" {
		t.Errorf("stored %q", got)
	}
	if len(svc.refreshed) != 1 || svc.refreshed[0] != "anthropic|sk-ant-secret|https://api.anthropic.com" {
		t.Errorf("refreshed = %v", svc.refreshed)
	}

	// A new key for the configured OpenAI replaces the generic one the setup wrote.
	if w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/openai", `{"api_key":"sk-new"}`); w.Code != http.StatusOK {
		t.Fatalf("openai: %d %s", w.Code, w.Body.String())
	}
	creds := config.StoredCredentials(path)
	if creds["MOTITA_LLM_API_KEY"] != "" || creds["OPENAI_API_KEY"] != "sk-new" {
		t.Errorf("creds = %v", creds)
	}
	if svc.cfg.LLM.APIKey != "sk-new" {
		t.Errorf("the open session runs with the new key: %q", svc.cfg.LLM.APIKey)
	}

	checkLLMForget(t, srv)
}

// checkLLMForget removes the saved Anthropic key, then forgets a keyless and an unknown provider.
func checkLLMForget(t *testing.T, srv *Server) {
	t.Helper()
	// Removing it takes the key and the endpoint away.
	w := llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/anthropic", "")
	var p llmProviderView
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != http.StatusOK || p.Key != config.KeyMissing {
		t.Errorf("forget: %d %+v", w.Code, p)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/claude-code", ""); w.Code != http.StatusOK {
		t.Errorf("forgetting a keyless provider is a no-op: %d", w.Code)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/nope", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown provider: %d", w.Code)
	}
}

func TestLLMSavesAnEndpoint(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/ollama", `{"base_url":"http://box.lan:11434/v1/"}`)
	var p llmProviderView
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != http.StatusOK || p.BaseURL != "http://box.lan:11434/v1" || p.Key != config.KeyNotNeeded {
		t.Fatalf("ollama: %d %s", w.Code, w.Body.String())
	}
	// The default provider's endpoint is written into the configuration too.
	if w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/openai",
		`{"base_url":"https://proxy.example.com/v1","api_key":"sk"}`); w.Code != http.StatusOK {
		t.Fatalf("openai: %d %s", w.Code, w.Body.String())
	}
	cfg, err := config.LoadWithoutKey(path)
	if err != nil || cfg.LLM.BaseURL != "https://proxy.example.com/v1" || cfg.LLM.Model != "gpt-5-mini" {
		t.Errorf("file: %v %+v", err, cfg.LLM)
	}
	if w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/openai", `{"base_url":""}`); w.Code != http.StatusOK {
		t.Fatalf("reset: %d", w.Code)
	}
	if cfg, _ := config.LoadWithoutKey(path); cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("an empty address goes back to the default: %q", cfg.LLM.BaseURL)
	}
}

func TestLLMSaveRefusals(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	cases := []struct {
		id, body string
		code     int
	}{
		{"nope", `{"api_key":"k"}`, http.StatusNotFound},
		{"anthropic", `not json`, http.StatusBadRequest},
		{"anthropic", `{"base_url":"https://x.example"}`, http.StatusBadRequest},
		{"ollama", `{"base_url":"ftp://x"}`, http.StatusBadRequest},
		{"claude-code", `{"api_key":"k"}`, http.StatusBadRequest},
		{"anthropic", `{"api_key":"  "}`, http.StatusBadRequest},
		{"anthropic", `{"api_key":"two words"}`, http.StatusBadRequest},
		{"anthropic", `{"api_key":"` + strings.Repeat("k", 5000) + `"}`, http.StatusBadRequest},
		{"openai", `{"base_url":"http://example.com/v1","api_key":"k"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/"+tc.id, tc.body); w.Code != tc.code {
			t.Errorf("%s %s: %d, want %d (%s)", tc.id, tc.body, w.Code, tc.code, w.Body.String())
		}
	}
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/anthropic", `{"api_key":"k"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "env_key") {
		t.Errorf("an exported key wins: %d %s", w.Code, w.Body.String())
	}
	p := providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), "anthropic")
	if p.KeyVariable != "ANTHROPIC_API_KEY" {
		t.Errorf("the variable is named: %+v", p)
	}
}

func TestLLMKeyVariableOfTheOpenAIPair(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	t.Setenv("MOTITA_LLM_API_KEY", "g")
	v := decodeLLM(t, get(t, srv, "/v1/llm", testToken))
	if p := providerOf(v, "openai"); p.KeyVariable != "MOTITA_LLM_API_KEY" {
		t.Errorf("openai = %+v", p)
	}
	os.Unsetenv("MOTITA_LLM_API_KEY")
	t.Setenv("OPENAI_API_KEY", "o")
	v = decodeLLM(t, get(t, srv, "/v1/llm", testToken))
	if p := providerOf(v, "openai"); p.KeyVariable != "OPENAI_API_KEY" {
		t.Errorf("openai = %+v", p)
	}
	if p := providerOf(v, "codex"); p.KeyVariable != "OPENAI_API_KEY" {
		t.Errorf("codex = %+v", p)
	}
}

func TestLLMSaveWriteFailures(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	fail := func(n int) func() {
		calls := 0
		oldC, oldD := setCredential, setLLMDefault
		setCredential = func(p, name, value string) error {
			calls++
			if calls == n {
				return errors.New("disk full")
			}
			return oldC(p, name, value)
		}
		setLLMDefault = func(p, provider, model, base string) error {
			calls++
			if calls == n {
				return errors.New("disk full")
			}
			return oldD(p, provider, model, base)
		}
		return func() { setCredential, setLLMDefault = oldC, oldD }
	}
	// 1: the key, 2: the generic key dropped, 3: the endpoint, 4: the configuration's endpoint.
	for n := 1; n <= 4; n++ {
		restore := fail(n)
		w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/openai", `{"api_key":"k","base_url":"https://a.example/v1"}`)
		restore()
		if w.Code != http.StatusInternalServerError {
			t.Errorf("failure %d: %d %s", n, w.Code, w.Body.String())
		}
	}
	restore := fail(1)
	w := llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/anthropic", "")
	restore()
	if w.Code != http.StatusInternalServerError {
		t.Errorf("forget failure: %d", w.Code)
	}
}

func TestLLMTestAsksTheProvider(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	if err := config.SetCredential(path, "OPENAI_API_KEY", "sk-saved"); err != nil {
		t.Fatal(err)
	}
	srv, _ := llmServer(t, path)
	var seen config.LLM
	old := listProviderModels
	listProviderModels = func(_ context.Context, l config.LLM) ([]string, error) {
		seen = l
		if l.APIKey == "bad" {
			return nil, errors.New("401 unauthorized")
		}
		return []string{"b", "a"}, nil
	}
	defer func() { listProviderModels = old }()
	r := testResultOf(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/openai/test", `{}`))
	if !r.OK || strings.Join(r.Models, ",") != "a,b" || seen.APIKey != "sk-saved" || seen.Model != "gpt-5-mini" {
		t.Errorf("saved key: %+v %+v", r, seen)
	}
	r = testResultOf(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/anthropic/test", `{"api_key":"bad"}`))
	if r.OK || !strings.Contains(r.Error, "401") || seen.BaseURL != "https://api.anthropic.com" {
		t.Errorf("refused key: %+v %+v", r, seen)
	}
	r = testResultOf(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/ollama/test",
		`{"base_url":"http://box.lan:11434/v1"}`))
	if !r.OK || seen.BaseURL != "http://box.lan:11434/v1" || seen.APIKey != "" {
		t.Errorf("self-hosted: %+v %+v", r, seen)
	}
	r = testResultOf(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/claude-code/test", `{}`))
	if !r.OK || len(r.Models) == 0 {
		t.Errorf("claude-code: %+v", r)
	}
	checkLLMTestRefusals(t, srv)
}

// testResultOf decodes the answer of a provider test, which is 200 whatever the provider said.
func testResultOf(t *testing.T, w *httptest.ResponseRecorder) testResult {
	t.Helper()
	var r testResult
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return r
}

// checkLLMTestRefusals checks the tests the gateway refuses before asking any provider.
func checkLLMTestRefusals(t *testing.T, srv *Server) {
	t.Helper()
	for body, code := range map[string]string{
		`{"base_url":"https://elsewhere.example/v1"}`:        "key_for_new_address",
		`{"base_url":"nope"}`:                                "",
		`{"base_url":"http://example.com/v1","api_key":"k"}`: "cleartext",
		`not json`: "",
	} {
		w := llmCall(t, srv, http.MethodPost, "/v1/llm/providers/openai/test", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), code) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/llm/providers/nope/test", `{}`); w.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", w.Code)
	}
}

func TestListProviderModelsUsesTheProvidersClient(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer up.Close()
	got, err := listProviderModels(context.Background(), config.LLM{Provider: "openai", BaseURL: up.URL, APIKey: "k"})
	if err != nil || len(got) != 1 || got[0] != "m1" {
		t.Errorf("%v %v", got, err)
	}
	if _, err := listProviderModels(context.Background(), config.LLM{Provider: "nope"}); err == nil {
		t.Error("an unknown provider has no client")
	}
}

func TestLLMDefaultIsWhatNewSessionsStartWith(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	if err := config.SetCredential(path, "MOTITA_LLM_API_KEY", "sk-openai"); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCredential(path, "ANTHROPIC_API_KEY", "sk-ant"); err != nil {
		t.Fatal(err)
	}
	srv, _ := llmServer(t, path)
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/default", `{"provider":"anthropic","model":"claude-opus-5-5"}`)
	v := decodeLLM(t, w)
	if w.Code != http.StatusOK || v.Provider != "anthropic" || v.Model != "claude-opus-5-5" {
		t.Fatalf("default: %d %+v", w.Code, v)
	}
	creds := config.StoredCredentials(path)
	if creds["MOTITA_LLM_API_KEY"] != "" || creds["OPENAI_API_KEY"] != "sk-openai" {
		t.Errorf("the generic key moves to OpenAI's own variable: %v", creds)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.LLM.APIKey != "sk-ant" || cfg.LLM.BaseURL != "https://api.anthropic.com" {
		t.Errorf("the file loads with the new provider's key: %v %+v", err, cfg.LLM)
	}

	w = llmCall(t, srv, http.MethodPost, "/v1/sessions", `{}`)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	conv := srv.sessions[created.ID]
	if conv == nil {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	got := conv.svc.Config().LLM
	if got.Provider != "anthropic" || got.Model != "claude-opus-5-5" || got.APIKey != "sk-ant" {
		t.Errorf("a new session starts on the default: %+v", got)
	}

	// Back to the same provider with another model: nothing to move.
	if w := llmCall(t, srv, http.MethodPut, "/v1/llm/default",
		`{"provider":"anthropic","model":"claude-haiku-4-5"}`); w.Code != http.StatusOK {
		t.Errorf("same provider: %d", w.Code)
	}
}

func TestLLMDefaultRefusals(t *testing.T) {
	path := llmEnv(t, openAIConfig+"  api_key: inline\n")
	if err := config.SetCredential(path, "ANTHROPIC_API_KEY", "sk-ant"); err != nil {
		t.Fatal(err)
	}
	srv, _ := llmServer(t, path)
	for body, want := range map[string]int{
		`not json`:                             http.StatusBadRequest,
		`{"provider":"nope","model":"m"}`:      http.StatusBadRequest,
		`{"provider":"openai","model":" "}`:    http.StatusBadRequest,
		`{"provider":"openai","model":"a\nb"}`: http.StatusBadRequest,
		`{"provider":"gemini","model":"m"}`:    http.StatusConflict,
		`{"provider":"anthropic","model":"m"}`: http.StatusConflict,
	} {
		if w := llmCall(t, srv, http.MethodPut, "/v1/llm/default", body); w.Code != want {
			t.Errorf("%s: %d, want %d (%s)", body, w.Code, want, w.Body.String())
		}
	}
}

func TestLLMDefaultWriteFailures(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	for n := 1; n <= 3; n++ {
		if err := config.SetCredential(path, "MOTITA_LLM_API_KEY", "sk-openai"); err != nil {
			t.Fatal(err)
		}
		_ = config.SetCredential(path, "OPENAI_API_KEY", "")
		_ = config.SetCredential(path, "ANTHROPIC_API_KEY", "sk-ant")
		calls := 0
		oldC, oldD := setCredential, setLLMDefault
		setCredential = func(p, name, value string) error {
			if calls++; calls == n {
				return errors.New("disk full")
			}
			return oldC(p, name, value)
		}
		setLLMDefault = func(string, string, string, string) error {
			if calls++; calls == n {
				return errors.New("disk full")
			}
			return nil
		}
		w := llmCall(t, srv, http.MethodPut, "/v1/llm/default", `{"provider":"anthropic","model":"m"}`)
		setCredential, setLLMDefault = oldC, oldD
		if w.Code != http.StatusInternalServerError {
			t.Errorf("failure %d: %d %s", n, w.Code, w.Body.String())
		}
	}
}

func TestLLMFallsBackWhenTheFileNoLongerLoads(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	if err := os.WriteFile(path, []byte("llm: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := decodeLLM(t, get(t, srv, "/v1/llm", testToken))
	if v.Provider != "openai" {
		t.Errorf("the open session's provider answers: %+v", v)
	}
	// A conversation with no refresher still starts on the default.
	srv.opts.NewService = func() (Service, error) {
		f := &fakeService{}
		f.cfg = config.Default()
		return f, nil
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/sessions", `{}`); w.Code != http.StatusCreated {
		t.Errorf("create: %d %s", w.Code, w.Body.String())
	}
}

func TestCheckBaseURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://api.openai.com/v1/":      true,
		"http://box.lan:11434/v1":         true,
		"http://127.0.0.1:11434":          true,
		"http://[::1]:11434/v1":           true,
		"http://192.168.1.20:11434/v1":    true,
		"ftp://x":                         false,
		"nope":                            false,
		"https://user:pw@api.example/v1":  false,
		"https://api.example/v1?x=1":      false,
		"https://api.example/v1#frag":     false,
		"http://169.254.169.254/latest":   false,
		"http://[fe80::1]/v1":             false,
		"http://[1.2]:11434":              false,
		"http://0.0.0.0:11434":            false,
		"https://api.example/v1 evil.com": false,
	} {
		if _, why := checkBaseURL(raw); (why == "") != ok {
			t.Errorf("%q: refusal %q, want accepted %v", raw, why, ok)
		}
	}
}

// usageServer is a gateway whose default provider (anthropic) has a key.
func usageServer(t *testing.T) *Server {
	t.Helper()
	llmEnv(t, "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-0123456789abcdef")
	svc := &fakeService{}
	svc.cfg = config.Default()
	svc.cfg.LLM.Provider, svc.cfg.LLM.Model = "anthropic", "claude-sonnet-5-5"
	return newTestServer(t, svc)
}

// readUsage reads GET /v1/llm/usage.
func readUsage(t *testing.T, srv *Server) llmUsageView {
	t.Helper()
	w := get(t, srv, "/v1/llm/usage", testToken)
	var v llmUsageView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %s: %v", w.Code, w.Body.String(), err)
	}
	return v
}

// usageRow is the row of provider id, or nil.
func usageRow(v llmUsageView, id string) *llmUsageRow {
	for i := range v.Providers {
		if v.Providers[i].ID == id {
			return &v.Providers[i]
		}
	}
	return nil
}

var usageLast = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// usedProviders is a ledger of three providers: calls and a rate window, calls only, limits only.
func usedProviders() []llm.ProviderUsage {
	return []llm.ProviderUsage{
		{Provider: "anthropic", Usage: llm.Usage{Input: 1_000_000, Output: 500_000, CacheRead: 3, CacheWrite: 2},
			Calls: 4, Since: usageLast.Add(-time.Hour), Last: usageLast,
			Limits: &llm.RateLimits{Tokens: &llm.Window{Limit: 100, Remaining: 40}}},
		{Provider: "openai", Usage: llm.Usage{Input: 1}, Calls: 1, Last: usageLast},
		{Provider: "gemini", Limits: &llm.RateLimits{}},
	}
}

func TestLLMUsageListsAProviderWithAKeyAtZeroAndHidesOneWithout(t *testing.T) {
	srv := usageServer(t)
	v := readUsage(t, srv)
	r := usageRow(v, "anthropic")
	if r == nil || !r.Default || r.Calls != 0 || r.Last != nil {
		t.Errorf("anthropic before any call: %+v", r)
	}
	if usageRow(v, "openai") != nil {
		t.Error("a provider with no key and no call is listed")
	}
	if w := get(t, srv, "/v1/llm/usage", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the usage needs the token: %d", w.Code)
	}
}

func TestLLMUsageShowsTheTokensOfACalledProvider(t *testing.T) {
	srv := usageServer(t)
	srv.usageTotals = usedProviders
	r := usageRow(readUsage(t, srv), "anthropic")
	if r == nil || r.Input != 1_000_000 || r.Output != 500_000 {
		t.Fatalf("tokens: %+v", r)
	}
	if r.CacheRead != 3 || r.CacheWrite != 2 || r.Calls != 4 {
		t.Errorf("cache and calls: %+v", r)
	}
	if r.Last == nil || !r.Last.Equal(usageLast) || r.Since == nil || !r.Since.Equal(usageLast.Add(-time.Hour)) {
		t.Errorf("times: %+v", r)
	}
	if r.Limits == nil || r.Limits.Tokens.Remaining != 40 {
		t.Errorf("limits: %+v", r.Limits)
	}
}

func TestLLMUsageListsAProviderThatOnlyReportedARateLimit(t *testing.T) {
	srv := usageServer(t)
	srv.usageTotals = usedProviders
	// No call counted, so there is no count to date.
	g := usageRow(readUsage(t, srv), "gemini")
	if g == nil || g.Since != nil || g.Last != nil || g.Limits == nil {
		t.Errorf("gemini with limits only: %+v", g)
	}
}

func TestLLMUsageLeavesOutAnOllamaOnTheUsersOwnServer(t *testing.T) {
	srv := usageServer(t)
	srv.usageTotals = func() []llm.ProviderUsage {
		return []llm.ProviderUsage{{Provider: "ollama", Calls: 1}, {Provider: "ollama-cloud", Calls: 1}}
	}
	other := filepath.Join(t.TempDir(), "motita.yaml")
	ollamaBase := config.ProviderBaseURLVariable("ollama")
	if err := config.SetCredential(other, ollamaBase, "http://localhost:11434/v1"); err != nil {
		t.Fatal(err)
	}
	config.UseCredentialsBeside(other)
	t.Cleanup(func() { config.UseCredentialsBeside(config.File()) })
	v := readUsage(t, srv)
	if usageRow(v, "ollama") != nil {
		t.Error("a self-hosted Ollama has no usage to show")
	}
	if usageRow(v, "ollama-cloud") == nil {
		t.Error("Ollama Cloud keeps its row")
	}
}

// copilotUsageServer is a gateway with a Copilot login whose quota read is `read`.
func copilotUsageServer(t *testing.T, read func() (oauth.CopilotQuota, error)) (*Server, *int) {
	t.Helper()
	srv := usageServer(t)
	calls := 0
	old := copilotUserQuota
	copilotUserQuota = func(context.Context, oauth.HTTPClient, string) (oauth.CopilotQuota, error) {
		calls++
		return read()
	}
	t.Cleanup(func() { copilotUserQuota = old })
	login := oauth.Credential{Provider: "copilot", AccessToken: "t", RefreshToken: "gho_x"}
	if err := oauth.SaveCredential(config.AuthDir(), login); err != nil {
		t.Fatal(err)
	}
	return srv, &calls
}

func TestLLMUsageShowsTheCopilotMonthlyQuota(t *testing.T) {
	reset := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	srv, calls := copilotUsageServer(t, func() (oauth.CopilotQuota, error) {
		return oauth.CopilotQuota{Entitlement: 300, Remaining: 75, ResetsAt: reset}, nil
	})
	srv.usageTotals = func() []llm.ProviderUsage {
		perMinute := &llm.RateLimits{Tokens: &llm.Window{Limit: 5, Remaining: 4}}
		return []llm.ProviderUsage{{Provider: "copilot", Calls: 1, Limits: perMinute}}
	}
	r := usageRow(readUsage(t, srv), "copilot")
	if r == nil || r.Limits == nil || len(r.Limits.Quotas) != 1 || r.Limits.Tokens == nil {
		t.Fatalf("copilot row: %+v", r)
	}
	if q := r.Limits.Quotas[0]; q.Name != "month" || q.Used != 75 || q.ResetsAt == nil || !q.ResetsAt.Equal(reset) {
		t.Errorf("quota: %+v", q)
	}
	readUsage(t, srv) // kept for a few minutes: GitHub is not asked again
	if *calls != 1 {
		t.Errorf("GitHub was asked %d times", *calls)
	}
}

func TestLLMUsageSaysNothingOfACopilotQuotaItCannotRead(t *testing.T) {
	cases := map[string]func() (oauth.CopilotQuota, error){
		"unlimited": func() (oauth.CopilotQuota, error) { return oauth.CopilotQuota{Unlimited: true}, nil },
		"failing":   func() (oauth.CopilotQuota, error) { return oauth.CopilotQuota{}, errors.New("offline") },
	}
	for name, read := range cases {
		srv, calls := copilotUsageServer(t, read)
		srv.usageTotals = func() []llm.ProviderUsage { return []llm.ProviderUsage{{Provider: "copilot", Calls: 1}} }
		if r := usageRow(readUsage(t, srv), "copilot"); r == nil || r.Limits != nil {
			t.Errorf("%s: a quota was invented: %+v", name, r)
		}
		readUsage(t, srv)
		if *calls != 1 {
			t.Errorf("%s: asked %d times inside the minute", name, *calls)
		}
		for k, e := range srv.quotaCache.items {
			e.at = time.Now().Add(-2 * quotaKeepFail)
			srv.quotaCache.items[k] = e
		}
		readUsage(t, srv)
		if *calls != 2 {
			t.Errorf("%s: a failed read is not tried again after a minute: %d", name, *calls)
		}
	}
}

func TestACopilotQuotaNeedsALoginOrAConfiguredKey(t *testing.T) {
	srv := usageServer(t)
	if q := srv.planQuotas(context.Background(), "", config.LLM{Provider: "copilot"}, "copilot"); q != nil {
		t.Errorf("no login, no key: %+v", q)
	}
	if copilotToken(config.LLM{Provider: "copilot", APIKey: "gho_k"}) != "gho_k" {
		t.Error("a copilot key is a GitHub token")
	}
	if copilotToken(config.LLM{Provider: "openai", APIKey: "k"}) != "" {
		t.Error("any other key is not")
	}
	if withQuotas(nil, nil) != nil {
		t.Error("nothing to add")
	}
	if got := withQuotas(nil, []llm.Quota{{Name: "month"}}); len(got.Quotas) != 1 {
		t.Errorf("limits made from nothing: %+v", got)
	}
}

// ollamaUsageServer is a gateway with an Ollama Cloud key whose balance read is `read`.
func ollamaUsageServer(t *testing.T, read func() ([]llm.Quota, error)) (*Server, *int) {
	t.Helper()
	srv := usageServer(t)
	calls := 0
	old := ollamaQuotas
	ollamaQuotas = func(_ context.Context, _ *http.Client, key string) ([]llm.Quota, error) {
		calls++
		if key != "ol-key" {
			t.Errorf("key = %q", key)
		}
		return read()
	}
	t.Cleanup(func() { ollamaQuotas = old })
	t.Setenv("OLLAMA_API_KEY", "ol-key")
	srv.usageTotals = func() []llm.ProviderUsage {
		return []llm.ProviderUsage{{Provider: "ollama-cloud", Calls: 1}, {Provider: "ollama", Calls: 1}}
	}
	return srv, &calls
}

func TestLLMUsageShowsTheOllamaCloudQuotas(t *testing.T) {
	srv, calls := ollamaUsageServer(t, func() ([]llm.Quota, error) {
		return []llm.Quota{{Name: "month", Used: 27.5}, {Name: "week", Used: 60}}, nil
	})
	v := readUsage(t, srv)
	r := usageRow(v, "ollama-cloud")
	if r == nil || r.Limits == nil || len(r.Limits.Quotas) != 2 || r.Limits.Quotas[0].Used != 27.5 {
		t.Fatalf("ollama-cloud row: %+v", r)
	}
	if usageRow(v, "ollama") == nil {
		t.Error("an Ollama with a key keeps its row")
	}
	readUsage(t, srv)
	if *calls != 1 {
		t.Errorf("the balance was asked %d times for one key", *calls)
	}
}

func TestLLMUsageSaysNothingOfAnOllamaBalanceItCannotRead(t *testing.T) {
	srv, _ := ollamaUsageServer(t, func() ([]llm.Quota, error) { return nil, errors.New("503") })
	if r := usageRow(readUsage(t, srv), "ollama-cloud"); r == nil || r.Limits != nil {
		t.Errorf("a quota was invented: %+v", r)
	}
	if got := srv.planQuotas(context.Background(), "", config.LLM{}, "openai"); got != nil {
		t.Errorf("a provider with no quota API: %+v", got)
	}
}

func TestTheUsagePanelNamesAProviderShortly(t *testing.T) {
	long := onboard.Provider{Name: "Ollama Cloud (hosted, with a key)", Short: "Ollama Cloud"}
	if got := shortName(long); got != "Ollama Cloud" {
		t.Errorf("short: %q", got)
	}
	if got := shortName(onboard.Provider{Name: "Custom"}); got != "Custom" {
		t.Errorf("no short name: %q", got)
	}
}

// TestAnOllamaCloudKeyDoesNotMakeTheLocalOllamaFollowed: both Ollama entries read OLLAMA_API_KEY,
// so a cloud key alone must not list (or ask the cloud for the quotas of) the entry for the user's
// own server.
func TestAnOllamaCloudKeyDoesNotMakeTheLocalOllamaFollowed(t *testing.T) {
	srv := usageServer(t)
	t.Setenv("OLLAMA_API_KEY", "cloud-key-0123456789abcdef")
	v := readUsage(t, srv)
	if usageRow(v, "ollama") != nil {
		t.Error("the local Ollama must not show usage because Ollama Cloud has a key")
	}
	if usageRow(v, "ollama-cloud") == nil {
		t.Error("Ollama Cloud keeps its row")
	}
}

// TestAConfiguredOllamaIsFollowedOnlyWhenItReachesTheCloud: a configuration naming "ollama" with no
// base_url has always reached Ollama Cloud; one pointing at a server of its own has no usage.
func TestAConfiguredOllamaIsFollowedOnlyWhenItReachesTheCloud(t *testing.T) {
	for base, want := range map[string]bool{"": true, "http://localhost:11434/v1": false} {
		srv := usageServer(t)
		t.Setenv("OLLAMA_API_KEY", "cloud-key-0123456789abcdef")
		cfg := srv.opts.Service.Config()
		cfg.LLM.Provider, cfg.LLM.BaseURL = "ollama", base
		srv.opts.Service.(*fakeService).cfg = cfg
		if got := usageRow(readUsage(t, srv), "ollama") != nil; got != want {
			t.Errorf("base %q: row = %v, want %v", base, got, want)
		}
	}
}

// TestACloudKeyIsNeverTheLocalOllamasKey: the key of Ollama Cloud is not sent to a server of one's
// own, and an Ollama with no endpoint saved is the local address, not the cloud's.
func TestACloudKeyIsNeverTheLocalOllamasKey(t *testing.T) {
	llmEnv(t, "")
	t.Setenv("OLLAMA_API_KEY", "cloud-key-0123456789abcdef")
	configured := config.LLM{Provider: "anthropic"}
	if got := effectiveBase(configured, "ollama"); got != ollamaLocalURL {
		t.Errorf("local Ollama base = %q", got)
	}
	if got := effectiveKey(configured, "ollama"); got != "" {
		t.Errorf("the cloud key reached the local Ollama: %q", got)
	}
	if got := effectiveKey(configured, "ollama-cloud"); got == "" {
		t.Error("Ollama Cloud keeps its key")
	}
	legacy := config.LLM{Provider: "ollama", APIKey: "k"}
	if got := effectiveBase(legacy, "ollama"); got != "https://ollama.com/v1" {
		t.Errorf("a legacy configuration keeps reaching the cloud: %q", got)
	}
	if got := effectiveKey(legacy, "ollama"); got != "k" {
		t.Errorf("legacy cloud key = %q", got)
	}
	own := config.LLM{Provider: "ollama", BaseURL: ollamaLocalURL, APIKey: "k"}
	if got := effectiveKey(own, "ollama"); got != "" {
		t.Errorf("a configured local Ollama sends no key: %q", got)
	}
	if got := effectiveOwnBase(own, "ollama"); got != ollamaLocalURL {
		t.Errorf("own base = %q", got)
	}
}

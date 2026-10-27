package gateway

import (
	"errors"
	"net/http"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

func (m *modelsService) SetCustomModel(c config.CustomModel) { m.custom = append(m.custom, c) }

func TestCustomModelIsSavedAndReachesEverySession(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, svc := modelsServer(t, path)
	if v := decodeLLM(t, get(t, srv, "/v1/llm", testToken)); !v.Custom.Empty() {
		t.Fatalf("no custom model yet: %+v", v.Custom)
	}
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/custom-model",
		`{"model":" llama-3.3-70b ","temperature":0.3,"max_tokens":8192,"context_window":128000}`)
	v := decodeLLM(t, w)
	if w.Code != http.StatusOK || v.Custom.Model != "llama-3.3-70b" || v.Custom.MaxTokens != 8192 ||
		v.Custom.ContextWindow != 128000 || v.Custom.Temperature == nil || *v.Custom.Temperature != 0.3 {
		t.Fatalf("save: %d %+v", w.Code, v.Custom)
	}
	cfg, err := config.LoadWithoutKey(path)
	if err != nil || cfg.CustomModel.Model != "llama-3.3-70b" || cfg.LLM.Custom.MaxTokens != 8192 {
		t.Errorf("the file holds the block: %v %+v", err, cfg.CustomModel)
	}
	if len(svc.custom) != 1 || svc.custom[0].Model != "llama-3.3-70b" {
		t.Errorf("the open session takes it: %+v", svc.custom)
	}
	// The model is the one catalogue entry of the custom provider.
	if p := providerOf(v, "custom"); len(p.Models) != 1 || p.Models[0].ID != "llama-3.3-70b" || !p.AskBaseURL {
		t.Errorf("custom provider: %+v", p)
	}
	// A new session starts with it.
	w = llmCall(t, srv, http.MethodPost, "/v1/sessions", `{}`)
	if w.Code >= 300 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, c := range srv.sessions {
		if m, ok := c.svc.(*modelsService); ok && len(m.custom) == 1 && m != svc {
			found = true
		}
	}
	if !found {
		t.Error("a new session starts with the custom model")
	}
	// An empty model clears the block.
	w = llmCall(t, srv, http.MethodPut, "/v1/llm/custom-model", `{"model":""}`)
	if v := decodeLLM(t, w); w.Code != http.StatusOK || !v.Custom.Empty() {
		t.Errorf("clear: %d %+v", w.Code, v.Custom)
	}
}

func TestCustomModelRefusals(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := modelsServer(t, path)
	for body, want := range map[string]int{
		`not json`:                            http.StatusBadRequest,
		`{"model":"m","temperature":3}`:       http.StatusBadRequest,
		`{"model":"m","max_tokens":-1}`:       http.StatusBadRequest,
		`{"model":"m","context_window":-5}`:   http.StatusBadRequest,
		`{"model":"m","max_tokens":99999999}`: http.StatusBadRequest,
	} {
		if w := llmCall(t, srv, http.MethodPut, "/v1/llm/custom-model", body); w.Code != want {
			t.Errorf("%s: %d, want %d", body, w.Code, want)
		}
	}
	old := setCustom
	setCustom = func(string, config.CustomModel) error { return errors.New("disk full") }
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/custom-model", `{"model":"m"}`)
	setCustom = old
	if w.Code != http.StatusInternalServerError {
		t.Errorf("a failed write: %d %s", w.Code, w.Body.String())
	}
}

func TestCustomModelNeedsAFile(t *testing.T) {
	llmEnv(t, "")
	svc := &modelsService{}
	svc.cfg = config.Default()
	svc.cfg.CustomModel = config.CustomModel{Model: "mine"}
	srv := newTestServer(t, svc)
	if v := decodeLLM(t, get(t, srv, "/v1/llm", testToken)); v.Custom.Model != "mine" {
		t.Errorf("without a file, the session's own answers: %+v", v.Custom)
	}
	if w := llmCall(t, srv, http.MethodPut, "/v1/llm/custom-model", `{"model":"m"}`); w.Code != http.StatusConflict {
		t.Errorf("no file to save in: %d", w.Code)
	}
}

func TestCustomProviderNeedsAnAddress(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := modelsServer(t, path)
	if w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/custom", `{"base_url":"","api_key":"tok-123"}`); w.Code !=
		http.StatusBadRequest {
		t.Errorf("no address: %d %s", w.Code, w.Body.String())
	}
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/custom",
		`{"base_url":"https://llm.example.com/v1","api_key":"tok-123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	p := providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), "custom")
	if p.Key != config.KeyStored || p.BaseURL != "https://llm.example.com/v1" {
		t.Errorf("custom provider: %+v", p)
	}
	if got := config.ProviderKeyFromEnv("custom"); got != "tok-123" {
		t.Errorf("the key lives under the custom variable: %q", got)
	}
}

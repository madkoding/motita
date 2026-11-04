package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// TestPresetForFollowsTheModel: every family is found from the ids its providers use, a narrower
// family wins over the one that contains it, and a model with no card keeps no preset.
func TestPresetForFollowsTheModel(t *testing.T) {
	for _, tc := range []struct{ provider, model, want string }{
		{"local", "qwen3-8b-gsq-q2_k", "qwen3"},
		{"ollama", "qwen3:8b", "qwen3"},
		{"openai", "Qwen/Qwen3-32B", "qwen3"},
		{"qwen", "qwen3-coder-plus", "qwen3-coder"},
		{"ollama", "qwen3-coder:30b", "qwen3-coder"},
		{"openai", "Qwen3-235B-A22B-Instruct-2507", "qwen3-instruct"},
		{"qwen", "qwen3-max", "qwen3-instruct"},
		{"ollama", "qwq:32b", "qwq"},
		{"ollama", "qwen2.5-coder:7b", "qwen2.5"},
		{"openai", "qwen2_5-7b", "qwen2.5"},
		{"ollama", "deepseek-r1:14b", "deepseek-r1"},
		{"openai", "deepseek-reasoner", "deepseek-r1"},
		{"ollama", "gemma3:12b", "gemma-3"},
		{"gemini", "gemma-3-27b-it", "gemma-3"},
		{"gemini", "models/gemini-3-pro-preview", "gemini-3"},
		{"ollama", "gpt-oss:20b", "gpt-oss"},
		{"openai", "moonshotai/kimi-k2-thinking", "kimi-k2-thinking"},
		{"openai", "kimi-k2-instruct", "kimi-k2"},
		{"ollama", "llama3.1:8b", "llama-3"},
		{"openai", "meta-llama/llama-3.3-70b-instruct", "llama-3"},
		{"ollama", "devstral:24b", "mistral-small"},
		{"openai", "mistral-small-3.2", "mistral-small"},
		{"gemini", "gemini-2.5-flash", ""},
		{"openai", "gpt-4o-mini", ""},
		{"anthropic", "qwen3-8b", ""},
		{"claude-code", "qwen3-8b", ""},
	} {
		if _, got := PresetFor(tc.provider, tc.model); got != tc.want {
			t.Errorf("PresetFor(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}

// TestPresetForLocalAddsPresencePenalty: only Motita's own quantised model gets it.
func TestPresetForLocalAddsPresencePenalty(t *testing.T) {
	local, _ := PresetFor(config.ProviderLocal, "qwen3-8b-gsq-q2_k")
	if local.PresencePenalty == nil || *local.PresencePenalty != localPresencePenalty {
		t.Errorf("local presence_penalty = %v", local.PresencePenalty)
	}
	if *local.Temperature != 0.6 || *local.TopP != 0.95 || *local.TopK != 20 || *local.MinP != 0 {
		t.Errorf("local preset = %+v", local)
	}
	other, _ := PresetFor("ollama", "qwen3:8b")
	if other.PresencePenalty != nil {
		t.Errorf("ollama presence_penalty = %v", *other.PresencePenalty)
	}
	if none, _ := PresetFor(config.ProviderLocal, "mystery"); none.PresencePenalty != nil {
		t.Error("a model with no preset must get no presence_penalty either")
	}
	// The table's values are its own: a client changing one must not change the next lookup.
	local.TopP = fp(0)
	if again, _ := PresetFor(config.ProviderLocal, "qwen3-8b"); *again.TopP != 0.95 {
		t.Error("PresetFor must hand out a copy")
	}
}

// TestSamplingPrecedence: the user's temperature wins, then the preset's, then the old default.
func TestSamplingPrecedence(t *testing.T) {
	for _, tc := range []struct {
		model string
		set   *float64
		want  float64
		topP  bool
	}{
		{"qwen3:8b", nil, 0.6, true},
		{"qwen3:8b", ptr(0.3), 0.3, true},
		{"gpt-4o-mini", nil, defaultTemperature, false},
		{"gpt-4o-mini", ptr(0.9), 0.9, false},
	} {
		c := &Client{cfg: config.LLM{Provider: "ollama", Model: tc.model, Temperature: tc.set}}
		s := c.sampling()
		if *s.Temperature != tc.want || (s.TopP != nil) != tc.topP {
			t.Errorf("%s (set %v): sampling = %+v", tc.model, tc.set, s)
		}
	}
}

// chatServer answers /chat/completions and keeps each body; refuse, when set, answers the first
// request with it instead.
func chatServer(t *testing.T, refuse string) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		n := len(bodies)
		mu.Unlock()
		if refuse != "" && n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, refuse)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), bodies...)
	}
}

func completeOnce(t *testing.T, cfg config.LLM) *Client {
	t.Helper()
	cfg.APIKey, cfg.MaxAttempts = "k", 1
	c, err := New(cfg, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestOpenAIBodyCarriesTheModelPreset: switching the model switches what the request samples with.
func TestOpenAIBodyCarriesTheModelPreset(t *testing.T) {
	srv, bodies := chatServer(t, "")
	completeOnce(t, config.LLM{Provider: config.ProviderLocal, Model: "qwen3-8b-gsq-q2_k", BaseURL: srv.URL})
	completeOnce(t, config.LLM{Provider: "ollama", Model: "gemma3:12b", BaseURL: srv.URL})
	completeOnce(t, config.LLM{Provider: "openai", Model: "gpt-4o-mini", BaseURL: srv.URL})
	got := bodies()
	want := []map[string]any{
		{"temperature": 0.6, "top_p": 0.95, "top_k": float64(20), "min_p": float64(0), "presence_penalty": 1.5},
		{"temperature": float64(1), "top_p": 0.95, "top_k": float64(64), "min_p": float64(0)},
		{"temperature": 0.2},
	}
	for i, w := range want {
		for _, k := range append([]string{"temperature"}, extraSamplingKeys...) {
			if got[i][k] != w[k] {
				t.Errorf("request %d (%v): %s = %v, want %v", i, got[i]["model"], k, got[i][k], w[k])
			}
		}
	}
}

// TestOfficialOpenAIGetsNoTopK: OpenAI's own API takes top_p but not top_k or min_p.
func TestOfficialOpenAIGetsNoTopK(t *testing.T) {
	c := &Client{cfg: config.LLM{Provider: "openai", Model: "gpt-oss-120b", BaseURL: "https://api.openai.com/v1"}}
	body := map[string]any{}
	c.addOpenAISampling(body, Sampling{TopP: fp(1), TopK: fp(20), MinP: fp(0)})
	if body["top_p"] != float64(1) || body["top_k"] != nil || body["min_p"] != nil {
		t.Errorf("official body = %v", body)
	}
	c.noSampling.Store(true)
	body = map[string]any{}
	c.addOpenAISampling(body, Sampling{TopP: fp(1)})
	if len(body) != 0 {
		t.Errorf("after a refusal nothing past temperature is sent, got %v", body)
	}
}

// TestSamplingRefusalIsAskedAgainWithout: a server that refuses top_k gets the request again with
// only the temperature, and the client stops sending the rest.
func TestSamplingRefusalIsAskedAgainWithout(t *testing.T) {
	srv, bodies := chatServer(t, `{"error":{"message":"Unrecognized request argument supplied: top_k"}}`)
	c := completeOnce(t, config.LLM{Provider: "openai", Model: "qwen3-32b", BaseURL: srv.URL})
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "again"}}); err != nil {
		t.Fatal(err)
	}
	got := bodies()
	if len(got) != 3 || got[0]["top_k"] == nil {
		t.Fatalf("bodies = %v", got)
	}
	for _, b := range got[1:] {
		for _, k := range extraSamplingKeys {
			if _, sent := b[k]; sent {
				t.Errorf("%s sent after the refusal: %v", k, b)
			}
		}
		if b["temperature"] != 0.6 {
			t.Errorf("the preset temperature must stay: %v", b)
		}
	}
	if !c.noSampling.Load() {
		t.Error("the refusal must be remembered")
	}
}

// TestRefusedSamplingLeavesOtherRefusals: only a 400 that names a field the request carries counts.
func TestRefusedSamplingLeavesOtherRefusals(t *testing.T) {
	c := &Client{log: logx.Global()}
	withTopK := []byte(`{"top_k":20}`)
	for name, tc := range map[string]struct {
		data   []byte
		status int
		answer string
	}{
		"not a 400":       {withTopK, http.StatusInternalServerError, "top_k"},
		"unreadable body": {[]byte("{"), http.StatusBadRequest, "top_k"},
		"names nothing":   {withTopK, http.StatusBadRequest, "model not found"},
		"names unsent":    {[]byte(`{"temperature":1}`), http.StatusBadRequest, "top_p"},
	} {
		data := tc.data
		if c.refusedSampling(&data, tc.status, []byte(tc.answer)) || string(data) != string(tc.data) {
			t.Errorf("%s: treated as a sampling refusal", name)
		}
	}
	if c.noSampling.Load() {
		t.Error("no refusal may be remembered")
	}
}

// TestGeminiCarriesTheModelPreset: Gemini 3 stays at 1.0, Gemma gets topP and topK, others 0.2.
func TestGeminiCarriesTheModelPreset(t *testing.T) {
	var mu sync.Mutex
	var configs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		configs = append(configs, b["generationConfig"].(map[string]any))
		mu.Unlock()
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	}))
	defer srv.Close()
	for _, model := range []string{"gemini-3-pro-preview", "gemma-3-27b-it", "gemini-2.5-flash"} {
		c, _ := New(config.LLM{Provider: "gemini", Model: model, APIKey: "k", BaseURL: srv.URL, MaxAttempts: 1}, nil)
		msgs := []Message{{Role: "user", Content: "hi"}}
		if _, err := c.Complete(context.Background(), msgs); err != nil {
			t.Fatal(err)
		}
		if _, err := c.CompleteTools(context.Background(), msgs, nil); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"map[maxOutputTokens:0 temperature:1]",
		"map[maxOutputTokens:0 temperature:1 topK:64 topP:0.95]",
		"map[maxOutputTokens:0 temperature:0.2]",
	}
	for i, w := range want {
		for _, got := range configs[2*i : 2*i+2] {
			if s := fmt.Sprint(got); !strings.EqualFold(s, w) {
				t.Errorf("generationConfig %d = %s, want %s", i, s, w)
			}
		}
	}
}

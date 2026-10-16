package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Ollama Cloud bills its models and an Ollama on the user's own machine does not, so the page
// shows the cloud price only while the endpoint is the cloud.
func TestOllamaPriceFollowsTheEndpoint(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	model := func(p llmProviderView) llmModelView {
		for _, m := range p.Models {
			if m.ID == "gpt-oss:120b" {
				return m
			}
		}
		t.Fatalf("gpt-oss:120b is not in %+v", p.Models)
		return llmModelView{}
	}
	cloud := model(providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), "ollama"))
	if cloud.Cost != "low" || cloud.PriceIn == 0 || cloud.PriceOut == 0 {
		t.Errorf("on Ollama Cloud the model is billed: %+v", cloud)
	}
	w := llmCall(t, srv, http.MethodPut, "/v1/llm/providers/ollama", `{"base_url":"http://box.lan:11434/v1"}`)
	var p llmProviderView
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if local := model(p); local.Cost != "local" || local.PriceIn != 0 || local.PriceOut != 0 {
		t.Errorf("on your own machine it is free: %+v", local)
	}
}

func TestOllamaCloudIsTheOllamaHost(t *testing.T) {
	for base, want := range map[string]bool{
		"https://ollama.com/v1":     true,
		"https://api.ollama.com/v1": true,
		"http://localhost:11434":    false,
		"http://box.lan:11434/v1":   false,
		"https://notollama.com/v1":  false,
		"http://[::1":               false,
	} {
		if got := ollamaCloud(base); got != want {
			t.Errorf("%s: got %v, want %v", base, got, want)
		}
	}
}

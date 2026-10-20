package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

func TestAcceptsTemperature(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-3-haiku-20240307":     true,
		"anthropic/claude-3-5-sonnet": true,
		"claude-2.1":                  true,
		"claude-instant-1.2":          true,
		"claude-opus-4-1":             false,
		"claude-sonnet-4-5":           false,
		"claude-fable-5-1":            false,
		"claude-opus-5-5":             false,
		"claude-sonnet-5-5":           false,
		"claude-haiku-5-5":            false,
		"":                            false,
	} {
		if got := acceptsTemperature(model); got != want {
			t.Errorf("acceptsTemperature(%q) = %v, want %v", model, got, want)
		}
	}
}

// TestAnthropicSamplingParams checks that no request carries a sampling parameter the model refuses.
func TestAnthropicSamplingParams(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()

	for model, wantTemp := range map[string]bool{"claude-sonnet-5-5": false, "claude-3-haiku-20240307": true} {
		c, _ := New(config.LLM{Provider: "anthropic", Model: model, APIKey: "k", BaseURL: srv.URL,
			Temperature: 0.2}, logx.Global())
		msgs := []Message{{Role: "user", Content: "hi"}}
		if _, err := c.Complete(context.Background(), msgs); err != nil {
			t.Fatal(err)
		}
		check := func(kind string) {
			if _, has := body["temperature"]; has != wantTemp {
				t.Errorf("%s %s: temperature sent = %v, want %v", model, kind, has, wantTemp)
			}
			for _, k := range []string{"top_p", "top_k"} {
				if _, has := body[k]; has {
					t.Errorf("%s %s: %s must not be sent", model, kind, k)
				}
			}
		}
		check("text")
		if _, err := c.CompleteTools(context.Background(), msgs, nil); err != nil {
			t.Fatal(err)
		}
		check("tools")
	}
}

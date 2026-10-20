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

const temperatureRefusal = `{"type":"error","error":{"type":"invalid_request_error",` +
	`"message":"` + "`temperature`" + ` is deprecated for this model"}}`

func ptr(f float64) *float64 { return &f }

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
		c, _ := New(config.LLM{Provider: "anthropic", Model: model, APIKey: "k", BaseURL: srv.URL}, logx.Global())
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

// TestAnthropicExplicitTemperature: a configured temperature is sent even to a current model.
func TestAnthropicExplicitTemperature(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()
	c, _ := New(config.LLM{Provider: "anthropic", Model: "claude-sonnet-5-5", APIKey: "k", BaseURL: srv.URL,
		Temperature: ptr(0)}, logx.Global())
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if v, has := body["temperature"]; !has || v != float64(0) {
		t.Errorf("explicit temperature 0 = %v (sent %v)", v, has)
	}
}

// TestAnthropicRetriesWithoutTemperature: a 400 that names temperature is asked again without it.
func TestAnthropicRetriesWithoutTemperature(t *testing.T) {
	var sent []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, has := body["temperature"]
		sent = append(sent, has)
		if has {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, temperatureRefusal)
			return
		}
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()
	c, _ := New(config.LLM{Provider: "anthropic", Model: "claude-future-9", APIKey: "k", BaseURL: srv.URL,
		Temperature: ptr(0.5), MaxAttempts: 1}, logx.Global())
	got, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil || got != "ok" {
		t.Fatalf("got %q, %v", got, err)
	}
	if len(sent) != 2 || !sent[0] || sent[1] {
		t.Errorf("temperature sent per request = %v, want [true false]", sent)
	}
	// The refusal is remembered: the next request does not try temperature again.
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "again"}}); err != nil ||
		len(sent) != 3 || sent[2] {
		t.Errorf("second request: %v, sent %v", err, sent)
	}
}

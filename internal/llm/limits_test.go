package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

func TestLimitFromRefusal(t *testing.T) {
	cases := []struct {
		name, msg string
		want      int
	}{
		{"openai", "max_tokens is too large: 65536. This model supports at most 16384 completion tokens, whereas you provided 65536.", 16384},
		{"anthropic", "max_tokens: 65536 > 32000, which is the maximum allowed number of output tokens for claude-x", 32000},
		{"context window is not the output limit", "This model's maximum context length is 8192 tokens. However, you requested 70000 tokens (4000 in the messages, 66000 in the completion).", 0},
		{"unrelated", "invalid api key 4096", 0},
	}
	for _, tc := range cases {
		if got := limitFromRefusal(65536, tc.msg); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestAnOutputLimitTheModelRefusesIsLearnedAndKept: the first request is refused with the
// model's limit; the same call is repeated within it, and the next call starts within it.
func TestAnOutputLimitTheModelRefusesIsLearnedAndKept(t *testing.T) {
	var asked []float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		n, _ := body["max_tokens"].(float64)
		asked = append(asked, n)
		if n > 16384 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"max_tokens is too large: 65536. This model supports at most 16384 completion tokens"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": "ok"}, "finish_reason": "stop"}}})
	}))
	defer srv.Close()

	c := textClient(t, srv)
	c.cfg = config.LLM{Provider: "openai", Model: "test", APIKey: "k", BaseURL: srv.URL, MaxTokens: 65536, MaxAttempts: 1, Timeout: c.cfg.Timeout}
	msgs := []Message{{Role: "user", Content: "hi"}}
	for i := 0; i < 2; i++ {
		if got, err := c.Complete(context.Background(), msgs); err != nil || got != "ok" {
			t.Fatalf("call %d: %q, %v", i, got, err)
		}
	}
	want := []float64{65536, 16384, 16384}
	if len(asked) != 3 || asked[0] != want[0] || asked[1] != want[1] || asked[2] != want[2] {
		t.Fatalf("limits asked = %v, want %v", asked, want)
	}
}

// TestLimitFromOllamaCloudRefusal: the message Ollama Cloud sends (measured by others against
// the live API), including a model tag whose digits must not be taken for the limit.
func TestLimitFromOllamaCloudRefusal(t *testing.T) {
	msg := "max_tokens (100000) exceeds model's maximum output tokens (65536) for model deepseek-v4-flash:0731"
	if got := limitFromRefusal(100000, msg); got != 65536 {
		t.Fatalf("got %d, want 65536", got)
	}
}

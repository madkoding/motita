package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// TestALocalOllamaIsToldToKeepTheModel: after a call, a self-hosted Ollama gets a request on its
// native API that keeps the model loaded for llm.keep_alive; other providers and keep_alive 0 none.
func TestALocalOllamaIsToldToKeepTheModel(t *testing.T) {
	var mu sync.Mutex
	var warmed []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/generate" {
			body := map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			warmed = append(warmed, body)
			mu.Unlock()
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	saved := goWarm
	goWarm = func(f func()) { f() }
	defer func() { goWarm = saved }()

	run := func(provider string, keep time.Duration) {
		t.Helper()
		c, err := New(config.LLM{Provider: provider, Model: "qwen3", APIKey: "k", BaseURL: srv.URL + "/v1/",
			KeepAlive: keep}, logx.Global())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
			t.Fatal(err)
		}
		_, _ = c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
		for range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		}
	}
	run("ollama", 30*time.Minute)
	if len(warmed) != 3 || warmed[0]["model"] != "qwen3" || warmed[0]["keep_alive"] != float64(1800) {
		t.Fatalf("each call must keep the model for 30 minutes: %v", warmed)
	}
	run("ollama", 0)
	run("openai", 30*time.Minute)
	if len(warmed) != 3 {
		t.Errorf("keep_alive 0 and other providers must send nothing: %v", warmed)
	}

}

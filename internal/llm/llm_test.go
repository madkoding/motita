package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
)

func init() {
	l, _ := logx.New(logx.Options{Level: logx.Error, Console: false})
	logx.Install(l)
}

func testClient(t *testing.T, provider, url string, cfg func(*config.LLM)) *Client {
	t.Helper()
	c := config.LLM{
		Provider:       provider,
		Model:          "test-model",
		APIKey:         "key",
		BaseURL:        url,
		MaxTokens:      100,
		Temperature:    0.1,
		Timeout:        5 * time.Second,
		MaxAttempts:    1,
		BackoffInitial: time.Millisecond,
		BackoffMax:     2 * time.Millisecond,
	}
	if cfg != nil {
		cfg(&c)
	}
	cli, err := New(c, logx.Global())
	if err != nil {
		t.Fatalf("could not create the client: %v", err)
	}
	return cli
}

// --- OpenAI -----------------------------------------------------------------

func TestOpenAIOK(t *testing.T) {
	var received map[string]any
	var header string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		header = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"test response"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := testClient(t, "openai", srv.URL, nil)
	text, err := c.Complete(context.Background(), []Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "hi"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if text != "test response" {
		t.Errorf("text = %q", text)
	}
	if header != "Bearer key" {
		t.Errorf("authorization header = %q", header)
	}
	if received["model"] != "test-model" {
		t.Errorf("model sent = %v", received["model"])
	}
	messages := received["messages"].([]any)
	if len(messages) != 2 {
		t.Errorf("messages sent = %d", len(messages))
	}
}

// --- Anthropic --------------------------------------------------------------

func TestAnthropicOK(t *testing.T) {
	var received map[string]any
	var keyHeader, versionHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		keyHeader = r.Header.Get("x-api-key")
		versionHeader = r.Header.Get("anthropic-version")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"anthropic response"}]}`)
	}))
	defer srv.Close()

	c := testClient(t, "anthropic", srv.URL, nil)
	text, err := c.Complete(context.Background(), []Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "hi"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if text != "anthropic response" {
		t.Errorf("text = %q", text)
	}
	if keyHeader != "key" {
		t.Errorf("x-api-key = %q", keyHeader)
	}
	if versionHeader == "" {
		t.Error("the anthropic-version header is missing")
	}
	// The system prompt goes separately, not as a message.
	if received["system"] != "system" {
		t.Errorf("system = %v", received["system"])
	}
	if len(received["messages"].([]any)) != 1 {
		t.Errorf("the messages must not include the system one: %v", received["messages"])
	}
}

// --- Gemini -----------------------------------------------------------------

func TestGeminiOK(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.String()
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"gemini response"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	c := testClient(t, "gemini", srv.URL, nil)
	text, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if text != "gemini response" {
		t.Errorf("text = %q", text)
	}
	if !strings.Contains(path, "generateContent") || !strings.Contains(path, "key=key") {
		t.Errorf("unexpected path: %q", path)
	}
}

// --- Retries ----------------------------------------------------------------

// TestRetriesOn500: server errors are retried with backoff.
func TestRetriesOn500(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"temporary failure"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"on the third attempt"}}]}`)
	}))
	defer srv.Close()

	c := testClient(t, "openai", srv.URL, func(c *config.LLM) {
		c.MaxAttempts = 5
	})
	text, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatalf("it should have succeeded on the third attempt: %v", err)
	}
	if text != "on the third attempt" {
		t.Errorf("text = %q", text)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d", attempts)
	}
}

// TestDoesNotRetryInvalidCredentials: a 429 is retried but a 401 is not —
// retrying an invalid credential is burning time and quota for nothing.
func TestDoesNotRetryInvalidCredentials(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer srv.Close()

	c := testClient(t, "openai", srv.URL, func(c *config.LLM) { c.MaxAttempts = 5 })
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err == nil {
		t.Fatal("an error was expected")
	}
	if attempts != 1 {
		t.Errorf("a 401 must not be retried: there were %d attempts", attempts)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("the error must mention the code: %v", err)
	}
}

func TestRetriesOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"too many requests"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	c := testClient(t, "openai", srv.URL, func(c *config.LLM) { c.MaxAttempts = 3 })
	text, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatalf("a 429 should be retried: %v", err)
	}
	if text != "ok" || attempts != 2 {
		t.Errorf("text=%q attempts=%d", text, attempts)
	}
}

func TestExhaustsAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "gateway down")
	}))
	defer srv.Close()

	c := testClient(t, "openai", srv.URL, func(c *config.LLM) { c.MaxAttempts = 3 })
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err == nil || !strings.Contains(err.Error(), "all 3 attempts were exhausted") {
		t.Fatalf("error = %v", err)
	}
}

// --- Edge cases -------------------------------------------------------------

func TestEmptyChoicesDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer srv.Close()

	c := testClient(t, "openai", srv.URL, nil)
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("empty choices must be an error, not a panic")
	}
}

func TestNonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html>bad gateway</html>")
	}))
	defer srv.Close()

	c := testClient(t, "openai", srv.URL, nil)
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("error = %v", err)
	}
}

func TestCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := testClient(t, "openai", srv.URL, func(c *config.LLM) { c.MaxAttempts = 5 })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := c.Complete(ctx, []Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("an error was expected because of the cancelled context")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the cancellation was not honoured: %s", elapsed)
	}
}

func TestConfigurationValidation(t *testing.T) {
	if _, err := New(config.LLM{Provider: "wizard", APIKey: "x"}, logx.Global()); err == nil {
		t.Error("an unknown provider must fail")
	}
	if _, err := New(config.LLM{Provider: "openai"}, logx.Global()); err == nil {
		t.Error("a missing key must fail")
	}
}

// --- Parsing the LLM's JSON -------------------------------------------------

// TestExtractJSON covers what models really return: markdown blocks, text
// around, and nested braces.
func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
		fails    bool
	}{
		{"clean json", `{"a":1}`, `{"a":1}`, false},
		{"markdown block", "Sure:\n```json\n{\"a\":1}\n```\nDone.", `{"a":1}`, false},
		{"text around", `My answer is {"a":{"b":2}} and that's it.`, `{"a":{"b":2}}`, false},
		{"with braces in string", `{"a":"text with } inside"}`, `{"a":"text with } inside"}`, false},
		{"with escapes", `{"a":"quote \" and brace } inside"}`, `{"a":"quote \" and brace } inside"}`, false},
		{"list", `[{"a":1},{"b":2}]`, `[{"a":1},{"b":2}]`, false},
		{"no json", "I'm sorry, I can't", "", true},
		{"truncated", `{"a":1`, "", true},
		{"empty", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := ExtractJSON(tc.input)
			if tc.fails {
				if err == nil {
					t.Fatalf("an error was expected and %s was returned", raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(raw) != tc.expected {
				t.Errorf("extracted = %s, expected %s", raw, tc.expected)
			}
		})
	}
}

func TestDecodeJSONIntoStruct(t *testing.T) {
	var dest struct {
		Plan []struct {
			Step    int    `json:"step"`
			Action  string `json:"action"`
			Command string `json:"command"`
		} `json:"plan"`
	}
	input := "Here you go:\n```json\n{\"plan\":[{\"step\":1,\"action\":\"list\",\"command\":\"ls -la\"}]}\n```"
	if err := DecodeJSON(input, &dest); err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(dest.Plan) != 1 || dest.Plan[0].Command != "ls -la" {
		t.Fatalf("decoded = %+v", dest)
	}
}

// --- model catalogue (/models and /api/tags) ---------------------------------

// TestListModelsPrefersTheOpenAICompatibleEndpoint: with a base URL that already
// ends in /v1, the catalogue lives at /v1/models. Asking /v1/api/tags is a 404
// (measured against Ollama Cloud), so it must not be the first attempt.
func TestListModelsPrefersTheOpenAICompatibleEndpoint(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"kimi-k2.6"},{"id":"glm-5.3"}]}`))
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), srv.URL+"/v1", "secret")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if !slices.Equal(models, []string{"kimi-k2.6", "glm-5.3"}) {
		t.Errorf("models = %v", models)
	}
	if len(asked) != 1 || asked[0] != "/v1/models" {
		t.Errorf("the first request must be /v1/models, got %v", asked)
	}
}

// TestListModelsFallsBackToOllamaNativeTags: a host that only implements the
// native endpoint still works, and the /v1 prefix is stripped so the URL is
// /api/tags and not /v1/api/tags.
func TestListModelsFallsBackToOllamaNativeTags(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.3"},{"name":"qwen3.5:397b"}]}`))
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), srv.URL+"/v1", "k")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if !slices.Equal(models, []string{"llama3.3", "qwen3.5:397b"}) {
		t.Errorf("models = %v", models)
	}
	if len(asked) != 2 || asked[1] != "/api/tags" {
		t.Errorf("expected the native endpoint as the second attempt, got %v", asked)
	}
	for _, p := range asked {
		if p == "/v1/api/tags" {
			t.Error("/v1/api/tags is a 404 and must never be requested")
		}
	}
}

// TestListModelsWithoutV1SuffixUsesNativeTags: a base URL with no /v1 uses
// /api/tags directly.
func TestListModelsWithoutV1SuffixUsesNativeTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:31b"}]}`))
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if !slices.Equal(models, []string{"gemma4:31b"}) {
		t.Errorf("models = %v", models)
	}
}

// TestListModelsSkipsEmptyNamesAndDuplicates: the two shapes can overlap, and a
// blank entry must not reach the menu.
func TestListModelsSkipsEmptyNamesAndDuplicates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"a"},{"id":""},{"id":"a"}],"models":[{"name":"a"},{"name":"b"}]}`))
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), srv.URL+"/v1", "")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if !slices.Equal(models, []string{"a", "b"}) {
		t.Errorf("models = %v, want [a b]", models)
	}
}

func TestListModelsReturnsErrorWhenNothingAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := ListModels(context.Background(), srv.URL+"/v1", "x")
	if err == nil {
		t.Fatal("expected an error when no endpoint lists models")
	}
}

func TestListModelsReturnsErrorOnInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := ListModels(context.Background(), srv.URL+"/v1", "x")
	if err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestListModelsRejectsAnEmptyBaseURL(t *testing.T) {
	_, err := ListModels(context.Background(), "   ", "k")
	if err == nil {
		t.Fatal("an empty base URL must be reported")
	}
}

func TestListModelsReturnsNetworkError(t *testing.T) {
	_, err := ListModels(context.Background(), "http://127.0.0.1:1/v1", "k")
	if err == nil {
		t.Fatal("expected network error")
	}
}

func TestListModelsReturnsRequestError(t *testing.T) {
	_, err := ListModels(context.Background(), "://not-a-url", "k")
	if err == nil {
		t.Fatal("expected request construction error")
	}
}

func TestListModelsCancelsWithContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ListModels(ctx, srv.URL+"/v1", "k")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// TestListOllamaModelsIsTheSameCatalogue: the named entry point the wizard uses
// is the catalogue query itself.
func TestListOllamaModelsIsTheSameCatalogue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"phi3"}]}`))
	}))
	defer srv.Close()

	models, err := ListOllamaModels(context.Background(), srv.URL+"/v1", "k")
	if err != nil {
		t.Fatalf("ListOllamaModels: %v", err)
	}
	if !slices.Equal(models, []string{"phi3"}) {
		t.Errorf("models = %v", models)
	}
}

func TestNewOllamaFillsDefaultBaseURL(t *testing.T) {
	cfg := config.LLM{Provider: "ollama", APIKey: "k", BaseURL: "", MaxAttempts: 1}
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.BaseURL != "https://ollama.com/v1" {
		t.Errorf("BaseURL = %q", c.cfg.BaseURL)
	}
}

func TestNewOllamaKeepsProvidedBaseURL(t *testing.T) {
	cfg := config.LLM{Provider: "ollama", APIKey: "k", BaseURL: "http://localhost:11434/v1", MaxAttempts: 1}
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("BaseURL = %q", c.cfg.BaseURL)
	}
}

// TestListModelsReportsAnEmptyCatalogue: both endpoints answering 200 with no
// models is a different failure from a network error and must be named.
func TestListModelsReportsAnEmptyCatalogue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"models":[]}`))
	}))
	defer srv.Close()

	_, err := ListModels(context.Background(), srv.URL+"/v1", "k")
	if err == nil {
		t.Fatal("an empty catalogue must be reported")
	}
	if !strings.Contains(err.Error(), "no models were listed") {
		t.Errorf("err = %v, want it to name the empty catalogue", err)
	}
}

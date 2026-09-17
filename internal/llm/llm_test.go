package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/oauth"
)

// rewriteTransport sends every request to one test server, keeping the path, so
// the hard-coded hosts of the login flows (api.github.com, chatgpt.com) can be
// answered locally.
type rewriteTransport struct{ target *url.URL }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host = rt.target.Scheme, rt.target.Host
	r2.Header.Set("X-Original-Host", r.URL.Host)
	return http.DefaultTransport.RoundTrip(r2)
}

func reroute(t *testing.T, c *Client, srv *httptest.Server) {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	c.http = &http.Client{Transport: rewriteTransport{u}, Timeout: 5 * time.Second}
	if c.login != nil {
		c.login.hc = c.http
	}
}

// withLogin stores a credential in a temporary auth directory for the test.
func withLogin(t *testing.T, cred oauth.Credential) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MOTITA_AUTH_DIR", dir)
	if cred.Provider != "" {
		if err := oauth.SaveCredential(dir, cred); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fakeJWT(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

// --- Copilot ----------------------------------------------------------------

// TestCopilotExchangesTheGitHubTokenAndSendsTheEditorHeaders: the Copilot API never
// accepts the GitHub token itself. It used to be sent as is, without the headers the
// API requires, so every Copilot request was refused.
func TestCopilotExchangesTheGitHubTokenAndSendsTheEditorHeaders(t *testing.T) {
	withLogin(t, oauth.Credential{})
	var exchanges int
	var chatAuth, integration, host string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			exchanges++
			if r.Header.Get("Authorization") != "token gho_abc" {
				t.Errorf("the exchange must use the GitHub token, got %q", r.Header.Get("Authorization"))
			}
			fmt.Fprintf(w, `{"token":"tid=session","expires_at":%d,"endpoints":{"api":"https://api.business.githubcopilot.com"}}`, time.Now().Add(30*time.Minute).Unix())
		case "/chat/completions":
			chatAuth = r.Header.Get("Authorization")
			integration = r.Header.Get("Copilot-Integration-Id")
			host = r.Header.Get("X-Original-Host")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c, err := New(config.LLM{Provider: "copilot", Model: "gpt-4.1", APIKey: "gho_abc", MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reroute(t, c, srv)
	for i := 0; i < 2; i++ {
		if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err != nil {
			t.Fatal(err)
		}
	}
	if chatAuth != "Bearer tid=session" || integration == "" {
		t.Errorf("chat auth = %q, integration = %q", chatAuth, integration)
	}
	if host != "api.business.githubcopilot.com" {
		t.Errorf("the plan's own endpoint must be used, got %q", host)
	}
	if exchanges != 1 {
		t.Errorf("a valid session token must be reused, exchanged %d times", exchanges)
	}
}

// TestStoredLoginIsRenewedOnceOn401: a token the server refuses is renewed and the
// request sent again, and the renewed credential is written back.
func TestStoredLoginIsRenewedOnceOn401(t *testing.T) {
	dir := withLogin(t, oauth.Credential{
		Provider: "copilot", AccessToken: "old", RefreshToken: "gho_x",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://api.githubcopilot.com",
	})
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			fmt.Fprintf(w, `{"token":"new","expires_at":%d,"endpoints":{"api":"https://api.githubcopilot.com"}}`, time.Now().Add(time.Hour).Unix())
		case "/chat/completions":
			calls++
			if r.Header.Get("Authorization") == "Bearer old" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
		}
	}))
	defer srv.Close()

	c, err := New(config.LLM{Provider: "copilot", Model: "gpt-4.1", MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reroute(t, c, srv)
	if text, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err != nil || text != "ok" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want the refused one and the retry", calls)
	}
	saved, _ := oauth.LoadCredential(dir, "copilot")
	if saved.AccessToken != "new" {
		t.Errorf("the renewed token must be stored, got %q", saved.AccessToken)
	}
}

// --- Codex --------------------------------------------------------------------

const responsesStream = "event: response.output_text.delta\n" +
	`data: {"type":"response.output_text.delta","delta":"Hel"}` + "\n\n" +
	`data: {"type":"response.output_text.delta","delta":"lo"}` + "\n\n" +
	`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a\"}"}}` + "\n\n" +
	`data: {"type":"response.completed","response":{"status":"completed"}}` + "\n\n"

// TestCodexWithAKeyUsesTheResponsesAPI: the codex models are not served by
// /chat/completions, which is where they used to be sent.
func TestCodexWithAKeyUsesTheResponsesAPI(t *testing.T) {
	withLogin(t, oauth.Credential{})
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, responsesStream)
	}))
	defer srv.Close()

	c, err := New(config.LLM{Provider: "codex", Model: "gpt-5-codex", APIKey: "sk", BaseURL: srv.URL, MaxTokens: 100, MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := []Tool{NewTool("read_file", "reads", ObjectSchema(map[string]any{"path": StringProperty("p")}, "path"))}
	reply, err := c.CompleteTools(context.Background(), []Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "read a"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c0", Type: "function", Function: FunctionCall{Name: "read_file", Arguments: json.RawMessage(`"{\"path\":\"b\"}"`)}}}},
		{Role: "tool", ToolCallID: "c0", Content: "B"},
	}, tools)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/responses" {
		t.Errorf("path = %q", path)
	}
	if reply.Content != "Hello" || len(reply.Calls) != 1 || reply.Calls[0].ID != "call_1" {
		t.Errorf("reply = %+v", reply)
	}
	var args map[string]string
	if err := reply.Calls[0].Function.DecodeArguments(&args); err != nil || args["path"] != "a" {
		t.Errorf("arguments = %s (%v)", reply.Calls[0].Function.Arguments, err)
	}
	if body["instructions"] != "be brief" || body["store"] != false || body["max_output_tokens"] != float64(100) {
		t.Errorf("body = %v", body)
	}
	input, _ := body["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input = %v", input)
	}
	call := input[1].(map[string]any)
	if call["type"] != "function_call" || call["arguments"] != `{"path":"b"}` {
		t.Errorf("the assistant call must be replayed with its arguments as text: %v", call)
	}
	if out := input[2].(map[string]any); out["type"] != "function_call_output" || out["call_id"] != "c0" {
		t.Errorf("tool output = %v", out)
	}
	params := body["tools"].([]any)[0].(map[string]any)["parameters"].(map[string]any)
	if params["type"] != "object" || params["required"] == nil {
		t.Errorf("the schema must be passed as is: %v", params)
	}
}

// TestCodexWithAChatGPTLoginGoesToTheChatGPTBackend: the login is billed to the
// ChatGPT plan, with the account in a header and no output cap.
func TestCodexWithAChatGPTLoginGoesToTheChatGPTBackend(t *testing.T) {
	withLogin(t, oauth.Credential{Provider: "codex", AccessToken: "at", RefreshToken: "rt", AccountID: "acct", ExpiresAt: time.Now().Add(time.Hour)})
	var hdr http.Header
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, responsesStream)
	}))
	defer srv.Close()

	c, err := New(config.LLM{Provider: "codex", Model: "gpt-5-codex", MaxTokens: 100, MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reroute(t, c, srv)
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("X-Original-Host") != "chatgpt.com" || hdr.Get("Authorization") != "Bearer at" || hdr.Get("Chatgpt-Account-Id") != "acct" {
		t.Errorf("headers = %v", hdr)
	}
	if _, capped := body["max_output_tokens"]; capped {
		t.Error("the ChatGPT backend refuses max_output_tokens")
	}
}

func TestCodexRefreshReadsTheAccountFromTheToken(t *testing.T) {
	access := fakeJWT(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-9"}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rt2","expires_in":3600}`, access)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	cred, err := oauth.CodexRefresh(context.Background(), &http.Client{Transport: rewriteTransport{u}}, oauth.Credential{RefreshToken: "rt"})
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccountID != "acct-9" || cred.RefreshToken != "rt2" || cred.ExpiresAt.IsZero() {
		t.Errorf("cred = %+v", cred)
	}
}

// --- Qwen ---------------------------------------------------------------------

func TestQwenKeyGoesToDashScopeAndLoginToItsResourceHost(t *testing.T) {
	withLogin(t, oauth.Credential{})
	var host, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, auth = r.Header.Get("X-Original-Host"), r.Header.Get("Authorization")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c, err := New(config.LLM{Provider: "qwen", Model: "qwen3-coder-plus", APIKey: "sk-dash", MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reroute(t, c, srv)
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if host != "dashscope-intl.aliyuncs.com" || auth != "Bearer sk-dash" {
		t.Errorf("key: host=%q auth=%q", host, auth)
	}

	withLogin(t, oauth.Credential{Provider: "qwen", AccessToken: "qa", RefreshToken: "qr", BaseURL: "https://portal.qwen.ai/v1", ExpiresAt: time.Now().Add(time.Hour)})
	c, err = New(config.LLM{Provider: "qwen", Model: "qwen3-coder-plus", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reroute(t, c, srv)
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if host != "portal.qwen.ai" || auth != "Bearer qa" {
		t.Errorf("login: host=%q auth=%q", host, auth)
	}
}

// --- Gemini ---------------------------------------------------------------------

func TestGeminiLoginSendsBearerAndProject(t *testing.T) {
	withLogin(t, oauth.Credential{Provider: "gemini", AccessToken: "ya29", RefreshToken: "r", ClientID: "cid", ProjectID: "proj", ExpiresAt: time.Now().Add(time.Hour)})
	var hdr http.Header
	var rawURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr, rawURL = r.Header.Clone(), r.URL.String()
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	}))
	defer srv.Close()
	c, err := New(config.LLM{Provider: "gemini", Model: "gemini-2.5-flash", BaseURL: srv.URL, MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Authorization") != "Bearer ya29" || hdr.Get("X-Goog-User-Project") != "proj" || strings.Contains(rawURL, "key=") {
		t.Errorf("headers=%v url=%s", hdr, rawURL)
	}
}

// TestGeminiToolsAreDeclaredAndAnsweredTheWayTheAPIRequires: the schema is the
// tool's own (not wrapped as "properties" again), a tool that takes nothing has no
// parameters, and a result names the FUNCTION and carries an object.
func TestGeminiToolsAreDeclaredAndAnsweredTheWayTheAPIRequires(t *testing.T) {
	withLogin(t, oauth.Credential{})
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read_file","args":{"path":"x"}},"thoughtSignature":"sig"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()
	c, _ := New(config.LLM{Provider: "gemini", Model: "gemini-2.5-pro", APIKey: "k", BaseURL: srv.URL, MaxAttempts: 1}, nil)
	tools := []Tool{
		NewTool("read_file", "reads", ObjectSchema(map[string]any{"path": StringProperty("p")}, "path")),
		NewTool("list_skills", "lists", ObjectSchema(map[string]any{})),
	}
	reply, err := c.CompleteTools(context.Background(), []Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "a", Function: FunctionCall{Name: "read_file", Arguments: json.RawMessage(`"{\"path\":\"1\"}"`)}},
			{ID: "b", Function: FunctionCall{Name: "list_skills", Arguments: json.RawMessage(`{}`)}},
		}},
		{Role: "tool", ToolCallID: "a", Content: "one"},
		{Role: "tool", ToolCallID: "b", Content: "two"},
	}, tools)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Calls) != 1 || reply.Calls[0].ID == "" || reply.Calls[0].signature != "sig" {
		t.Errorf("calls = %+v", reply.Calls)
	}
	decls := body["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)
	params := decls[0].(map[string]any)["parameters"].(map[string]any)
	if _, nested := params["properties"].(map[string]any)["properties"]; nested || params["required"] == nil {
		t.Errorf("schema = %v", params)
	}
	if _, has := decls[1].(map[string]any)["parameters"]; has {
		t.Error("a tool without parameters must declare none")
	}
	contents := body["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("parallel results must share one turn: %v", contents)
	}
	model := contents[1].(map[string]any)["parts"].([]any)
	if model[0].(map[string]any)["thoughtSignature"] == nil {
		t.Error("the first call of a turn must carry a thought signature")
	}
	args := model[0].(map[string]any)["functionCall"].(map[string]any)["args"].(map[string]any)
	if args["path"] != "1" {
		t.Errorf("stringified arguments must be sent as an object: %v", args)
	}
	results := contents[2].(map[string]any)["parts"].([]any)
	fr := results[1].(map[string]any)["functionResponse"].(map[string]any)
	if fr["name"] != "list_skills" {
		t.Errorf("a result must name the function, got %v", fr["name"])
	}
	if _, ok := fr["response"].(map[string]any); !ok {
		t.Errorf("a result must be an object: %v", fr["response"])
	}
}

// --- Anthropic ----------------------------------------------------------------

func TestAnthropicToolsAreDeclaredAndAnsweredTheWayTheAPIRequires(t *testing.T) {
	withLogin(t, oauth.Credential{})
	var body map[string]any
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("x-api-key")
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()
	c, _ := New(config.LLM{Provider: "anthropic", Model: "claude-sonnet-5-5", APIKey: "sk-ant", BaseURL: srv.URL, MaxAttempts: 1}, nil)
	tools := []Tool{NewTool("read_file", "reads", ObjectSchema(map[string]any{"path": StringProperty("p")}, "path"))}
	if _, err := c.CompleteTools(context.Background(), []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: "reading", ToolCalls: []ToolCall{
			{ID: "t1", Function: FunctionCall{Name: "read_file", Arguments: json.RawMessage(`"{\"path\":\"1\"}"`)}},
			{ID: "t2", Function: FunctionCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"2"}`)}},
		}},
		{Role: "tool", ToolCallID: "t1", Content: "one"},
		{Role: "tool", ToolCallID: "t2", Content: ""},
	}, tools); err != nil {
		t.Fatal(err)
	}
	if key != "sk-ant" {
		t.Errorf("x-api-key = %q", key)
	}
	schema := body["tools"].([]any)[0].(map[string]any)["input_schema"].(map[string]any)
	if _, nested := schema["properties"].(map[string]any)["properties"]; nested || schema["required"] == nil {
		t.Errorf("input_schema = %v", schema)
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("the two results must share one user turn: %v", msgs)
	}
	asst := msgs[1].(map[string]any)["content"].([]any)
	if in := asst[1].(map[string]any)["input"].(map[string]any); in["path"] != "1" {
		t.Errorf("a stringified input must be sent as an object: %v", in)
	}
	results := msgs[2].(map[string]any)["content"].([]any)
	for _, b := range results {
		blk := b.(map[string]any)
		if blk["type"] != "tool_result" {
			t.Errorf("a results turn must hold only tool_result blocks: %v", results)
		}
		if blk["content"] == "" {
			t.Error("a tool_result must not be empty")
		}
	}
}

// --- OpenAI ---------------------------------------------------------------------

func TestOpenAIReasoningModelsGetTheParametersTheyAccept(t *testing.T) {
	withLogin(t, oauth.Credential{})
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	for _, model := range []string{"gpt-5-mini", "gpt-4o-mini"} {
		c, _ := New(config.LLM{Provider: "openai", Model: model, APIKey: "k", BaseURL: srv.URL, MaxTokens: 50, Temperature: 0.2, MaxAttempts: 1}, nil)
		if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, has := bodies[0]["temperature"]; has || bodies[0]["max_completion_tokens"] != float64(50) || bodies[0]["max_tokens"] != nil {
		t.Errorf("gpt-5-mini body = %v", bodies[0])
	}
	if bodies[1]["temperature"] != 0.2 || bodies[1]["max_tokens"] != float64(50) {
		t.Errorf("gpt-4o-mini body (compatible host) = %v", bodies[1])
	}
}

// --- Keys and logins ------------------------------------------------------------

func TestSelfHostedOllamaNeedsNoKey(t *testing.T) {
	withLogin(t, oauth.Credential{})
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	c, err := New(config.LLM{Provider: "ollama", Model: "llama3", BaseURL: srv.URL + "/v1", MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatalf("a local Ollama must not need a key: %v", err)
	}
	c.cfg.BaseURL = srv.URL
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Errorf("no Authorization must be sent without a key, got %q", auth)
	}
	if _, err := New(config.LLM{Provider: "ollama", Model: "m"}, nil); err == nil {
		t.Error("Ollama Cloud without a key must be refused")
	}
}

func TestEveryProviderIsAccepted(t *testing.T) {
	withLogin(t, oauth.Credential{})
	for _, p := range config.Providers {
		if _, err := New(config.LLM{Provider: p, Model: "m", APIKey: "k"}, nil); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := New(config.LLM{Provider: "nope", Model: "m", APIKey: "k"}, nil); err == nil {
		t.Error("an unknown provider must be refused")
	}
	if _, err := New(config.LLM{Provider: "codex", Model: "m"}, nil); err == nil || !strings.Contains(err.Error(), "log in") {
		t.Errorf("a provider with a login must say so when nothing is configured: %v", err)
	}
}

func TestListModelsUsesEachProvidersOwnAuthentication(t *testing.T) {
	withLogin(t, oauth.Credential{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models" && r.Header.Get("x-api-key") == "ka":
			fmt.Fprint(w, `{"data":[{"id":"claude-x"}]}`)
		case r.URL.Path == "/v1beta/models" && r.Header.Get("x-goog-api-key") == "kg":
			fmt.Fprint(w, `{"models":[{"name":"models/gemini-x","supportedGenerationMethods":["generateContent"]},{"name":"models/embed","supportedGenerationMethods":["embedContent"]}]}`)
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, r.URL.String())
		}
	}))
	defer srv.Close()
	for _, tc := range []struct{ provider, key, want string }{
		{"anthropic", "ka", "claude-x"},
		{"gemini", "kg", "gemini-x"},
	} {
		c, _ := New(config.LLM{Provider: tc.provider, Model: "m", APIKey: tc.key, BaseURL: srv.URL}, nil)
		got, err := c.ListModels(context.Background())
		if err != nil || len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: %v %v", tc.provider, got, err)
		}
	}
}

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/oauth"
)

func TestDefaultBaseURLOfEveryProvider(t *testing.T) {
	for _, p := range config.Providers {
		got := DefaultBaseURL(p)
		if (got == "") != (p == "claude-code" || p == "custom") {
			t.Errorf("%s: %q", p, got)
		}
	}
}

// TestAnUnreadableLoginIsAnError: a corrupt credential file must not read as "no login".
func TestAnUnreadableLoginIsAnError(t *testing.T) {
	dir := withLogin(t, oauth.Credential{})
	_ = os.WriteFile(oauth.CredentialPath(dir, "qwen"), []byte("{"), 0o600)
	if _, err := New(config.LLM{Provider: "qwen", Model: "m"}, nil); err == nil {
		t.Error("a corrupt login must be reported")
	}
}

// TestALoginThatCannotBeRenewedFailsWithoutRetrying: every request path reports it.
func TestALoginThatCannotBeRenewedFailsWithoutRetrying(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()
	for _, provider := range []string{"copilot", "codex", "qwen", "gemini"} {
		withLogin(
			t,
			oauth.Credential{Provider: provider, AccessToken: "old", RefreshToken: "r", ExpiresAt: time.Now().Add(-time.Hour)},
		)
		c, err := New(config.LLM{Provider: provider, Model: "m", MaxAttempts: 3}, nil)
		if err != nil {
			t.Fatal(err)
		}
		reroute(t, c, srv)
		if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}); err == nil ||
			!strings.Contains(err.Error(), "could not be renewed") {
			t.Errorf("%s: Complete err = %v", provider, err)
		}
		if _, err := c.ListModels(context.Background()); err == nil {
			t.Errorf("%s: ListModels must fail", provider)
		}
	}
	withLogin(
		t,
		oauth.Credential{Provider: "codex", AccessToken: "old", RefreshToken: "r", ExpiresAt: time.Now().Add(-time.Hour)},
	)
	c, _ := New(config.LLM{Provider: "codex", Model: "m", MaxAttempts: 1}, nil)
	reroute(t, c, srv)
	if _, err := c.callResponsesStream(context.Background(), []Message{{Role: "user", Content: "x"}}, nil); err == nil {
		t.Error("the stream must not open with a dead login")
	}
}

func TestCredentialPrefersALoginAnotherProcessRenewed(t *testing.T) {
	dir := t.TempDir()
	_ = oauth.SaveCredential(
		dir,
		oauth.Credential{Provider: "qwen", AccessToken: "fresh", ExpiresAt: time.Now().Add(time.Hour)},
	)
	refreshed := false
	s := &loginState{provider: "qwen", dir: dir, now: time.Now,
		cred: oauth.Credential{Provider: "qwen", AccessToken: "stale", ExpiresAt: time.Now().Add(-time.Hour)},
		refresh: func(context.Context, oauth.HTTPClient, oauth.Credential) (oauth.Credential, error) {
			refreshed = true
			return oauth.Credential{}, nil
		}}
	c, err := s.credential(context.Background(), false)
	if err != nil || c.AccessToken != "fresh" || refreshed {
		t.Errorf("c=%+v err=%v refreshed=%v", c, err, refreshed)
	}
}

// TestClientsShareOneRenewalOfALogin: two clients of the same login that find it expired at once
// renew it once. A rotated refresh token is spent only once, so a second renewal would be refused
// and read as "log in again".
func TestClientsShareOneRenewalOfALogin(t *testing.T) {
	dir := t.TempDir()
	old := oauth.Credential{Provider: "codex", AccessToken: "a0", RefreshToken: "r0",
		ExpiresAt: time.Now().Add(-time.Hour)}
	if err := oauth.SaveCredential(dir, old); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	spent := map[string]bool{}
	gate := make(chan struct{})
	refresh := func(_ context.Context, _ oauth.HTTPClient, c oauth.Credential) (oauth.Credential, error) {
		<-gate
		mu.Lock()
		defer mu.Unlock()
		if spent[c.RefreshToken] {
			return oauth.Credential{}, fmt.Errorf("refresh_token_reused")
		}
		spent[c.RefreshToken] = true
		return oauth.Credential{Provider: "codex", AccessToken: "a1", RefreshToken: "r1",
			ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		s := &loginState{provider: "codex", dir: dir, cred: old, refresh: refresh, now: time.Now}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := s.credential(context.Background(), false)
			if err == nil && c.AccessToken != "a1" {
				err = fmt.Errorf("token %q", c.AccessToken)
			}
			errs[i] = err
		}()
	}
	time.Sleep(50 * time.Millisecond) // both clients reach the renewal before it answers
	close(gate)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || len(spent) != 1 {
		t.Errorf("errs = %v, renewals = %d", errs, len(spent))
	}
}

// TestARefusedRenewalTakesTheLoginAnotherProcessSaved: another process can spend the refresh token
// first. The renewal is then refused, and the login it saved is used instead of an error.
func TestARefusedRenewalTakesTheLoginAnotherProcessSaved(t *testing.T) {
	dir := t.TempDir()
	old := oauth.Credential{Provider: "qwen", AccessToken: "a0", RefreshToken: "r0", ExpiresAt: time.Now().Add(-time.Hour)}
	_ = oauth.SaveCredential(dir, old)
	s := &loginState{provider: "qwen", dir: dir, cred: old, now: time.Now,
		refresh: func(context.Context, oauth.HTTPClient, oauth.Credential) (oauth.Credential, error) {
			_ = oauth.SaveCredential(dir, oauth.Credential{Provider: "qwen", AccessToken: "other",
				ExpiresAt: time.Now().Add(time.Hour)})
			return oauth.Credential{}, fmt.Errorf("refresh_token_reused")
		}}
	c, err := s.credential(context.Background(), false)
	if err != nil || c.AccessToken != "other" {
		t.Errorf("c=%+v err=%v", c, err)
	}
}

// TestAShorterOutputLimitDoesNotRenewTheLogin: asking again within the model's output limit is
// not a refused login, so a valid login is not renewed for it. A 401 on that second request
// still renews it once.
func TestAShorterOutputLimitDoesNotRenewTheLogin(t *testing.T) {
	for _, expire := range []bool{false, true} {
		withLogin(t, oauth.Credential{Provider: "qwen", AccessToken: "a0", RefreshToken: "r0",
			ExpiresAt: time.Now().Add(time.Hour)})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := make([]byte, r.ContentLength)
			r.Body.Read(b)
			switch {
			case strings.Contains(string(b), `"max_tokens":65536`):
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"message":"max_tokens is too large: 65536. `+
					`This model supports at most 16384 completion tokens"}}`)
			case expire && r.Header.Get("Authorization") == "Bearer a0":
				w.WriteHeader(http.StatusUnauthorized)
			default:
				fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			}
		}))
		c, err := New(config.LLM{Provider: "qwen", Model: "m", MaxTokens: 65536, MaxAttempts: 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		reroute(t, c, srv)
		renewals := 0
		c.login.refresh = func(context.Context, oauth.HTTPClient, oauth.Credential) (oauth.Credential, error) {
			renewals++
			return oauth.Credential{Provider: "qwen", AccessToken: "a1", RefreshToken: "r1",
				ExpiresAt: time.Now().Add(time.Hour)}, nil
		}
		got, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}})
		want := 0
		if expire {
			want = 1
		}
		if err != nil || got != "ok" || renewals != want {
			t.Errorf("expire=%v: got %q err %v renewals %d, want %d", expire, got, err, renewals, want)
		}
		srv.Close()
	}
}

func TestCredentialReportsARenewalItCannotSave(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	_ = os.WriteFile(file, nil, 0o600)
	s := &loginState{provider: "qwen", dir: file, now: time.Now,
		refresh: func(context.Context, oauth.HTTPClient, oauth.Credential) (oauth.Credential, error) {
			return oauth.Credential{Provider: "qwen", AccessToken: "new"}, nil
		}}
	if _, err := s.credential(context.Background(), true); err == nil ||
		!strings.Contains(err.Error(), "could not be saved") {
		t.Errorf("err = %v", err)
	}
}

func TestListModelsEdges(t *testing.T) {
	withLogin(t, oauth.Credential{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[]}`)
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"gpt-x"}]}`)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	c, _ := New(config.LLM{Provider: "anthropic", Model: "m", APIKey: "k", BaseURL: srv.URL}, nil)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Error("an empty catalogue must be an error")
	}
	c, _ = New(config.LLM{Provider: "gemini", Model: "m", APIKey: "k", BaseURL: srv.URL}, nil)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Error("a refused listing must be an error")
	}
	c, _ = New(config.LLM{Provider: "openai", Model: "m", APIKey: "k", BaseURL: srv.URL}, nil)
	if got, err := c.ListModels(context.Background()); err != nil || got[0] != "gpt-x" {
		t.Errorf("openai: %v %v", got, err)
	}
	c, _ = New(config.LLM{Provider: "anthropic", Model: "m", APIKey: "k", BaseURL: "://bad"}, nil)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Error("a bad URL must be an error")
	}
	c, _ = New(config.LLM{Provider: "anthropic", Model: "m", APIKey: "k", BaseURL: "http://127.0.0.1:1"}, nil)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Error("an unreachable host must be an error")
	}
	withLogin(
		t,
		oauth.Credential{Provider: "codex", AccessToken: "a", AccountID: "x", ExpiresAt: time.Now().Add(time.Hour)},
	)
	c, _ = New(config.LLM{Provider: "codex", Model: "m"}, nil)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Error("a ChatGPT login has no catalogue")
	}
}

// --- Responses stream -------------------------------------------------------------

func codexStream(t *testing.T, body string, header func(http.ResponseWriter)) *Client {
	t.Helper()
	withLogin(t, oauth.Credential{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if header != nil {
			header(w)
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	c, err := New(config.LLM{Provider: "codex", Model: "gpt-5-codex", APIKey: "k", BaseURL: srv.URL, MaxAttempts: 1,
		Reasoning: config.Reasoning{Enabled: true, Level: "high"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func drain(t *testing.T, c *Client) (text, thinking string, reply Reply, streamErr error) {
	t.Helper()
	ch, err := c.callResponsesStream(context.Background(), []Message{{Role: "user", Content: "x"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for chunk := range ch {
		switch chunk.Event {
		case StreamText:
			text += chunk.Text
		case StreamThinking:
			thinking += chunk.Text
		case StreamDone:
			reply = chunk.Reply
		case StreamError:
			streamErr = chunk.Error
		default:
			// Other events carry nothing this helper collects.
		}
	}
	return
}

func TestResponsesStreamEvents(t *testing.T) {
	body := ": comment\n" +
		"event: x\n" +
		"data:\n" +
		"data: not json\n" +
		`data: {"type":"response.output_text.delta","delta":""}` + "\n" +
		`data: {"type":"response.reasoning_summary_text.delta","delta":"thinking"}` + "\n" +
		`data: {"type":"response.reasoning_text.delta","delta":""}` + "\n" +
		`data: {"type":"response.output_item.done","item":{"type":"message"}}` + "\n" +
		`data: {"type":"response.output_text.delta","delta":"ok"}` + "\n" +
		"data: [DONE]\n"
	text, thinking, reply, err := drain(t, codexStream(t, body, nil))
	if err != nil || text != "ok" || thinking != "thinking" || reply.Content != "ok" {
		t.Errorf("text=%q thinking=%q reply=%+v err=%v (EOF after text is a finished reply)", text, thinking, reply, err)
	}
	if _, _, _, err := drain(t, codexStream(t, "data: [DONE]\n", nil)); err == nil {
		t.Error("a stream with nothing in it must be an error")
	}
}

func TestResponsesStreamEndings(t *testing.T) {
	cases := []struct {
		name, body string
		wantErr    string
		wantText   string
	}{
		{
			"incomplete with text",
			`data: {"type":"response.output_text.delta","delta":"part"}` + "\n" + `data: ` +
				`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}` + "\n",
			"",
			"part",
		},
		{
			"incomplete empty",
			`data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}` + "\n",
			"max_output_tokens",
			"",
		},
		{"incomplete no reason", `data: {"type":"response.incomplete","response":{}}` + "\n", "unknown", ""},
		{"failed", `data: {"type":"response.failed","response":{"error":{"message":"overloaded"}}}` + "\n", "overloaded", ""},
		{"failed bare", `data: {"type":"response.failed","response":{}}` + "\n", "the response failed", ""},
		{"error", `data: {"type":"error","code":"rate_limit","message":"slow"}` + "\n", "rate_limit slow", ""},
	}
	for _, tc := range cases {
		_, _, reply, err := drain(t, codexStream(t, tc.body, nil))
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err = %v", tc.name, err)
			}
			continue
		}
		if err != nil || reply.Content != tc.wantText {
			t.Errorf("%s: reply=%+v err=%v", tc.name, reply, err)
		}
	}
	// A body cut short by the transport is an error, not an end.
	_, _, _, err := drain(
		t,
		codexStream(t, `data: {"type":"response.output_text.delta","delta":"a"}`+"\n", func(w http.ResponseWriter) {
			w.Header().Set("Content-Length", "1000")
		}),
	)
	if err == nil {
		t.Error("a truncated body must be an error")
	}
}

func TestCodexTextPath(t *testing.T) {
	onlyCall := `data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"c","name":"f",` +
		`"arguments":"{}"}}` + "\n" +
		`data: {"type":"response.completed"}` + "\n"
	if _, err := codexStream(t, onlyCall, nil).Complete(
		context.Background(), []Message{{Role: "user", Content: "x"}},
	); err == nil {
		t.Error("a text phase answered with a call must be an error")
	}
	failed := `data: {"type":"error","message":"bad"}` + "\n"
	if _, err := codexStream(t, failed, nil).Complete(
		context.Background(), []Message{{Role: "user", Content: "x"}},
	); err == nil {
		t.Error("a failed stream must be an error")
	}
	empty := `data: {"type":"response.completed"}` + "\n"
	if _, err := codexStream(t, empty, nil).CompleteTools(
		context.Background(), []Message{{Role: "user", Content: "x"}}, nil,
	); err == nil {
		t.Error("a reply with nothing must be an error")
	}
	var got []string
	c := codexStream(
		t,
		`data: {"type":"response.output_text.delta","delta":"hi"}`+"\n"+`data: {"type":"response.completed"}`+"\n",
		nil,
	)
	for chunk := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "x"}}, nil) {
		got = append(got, chunk.Text)
	}
	if strings.Join(got, "") != "hi" {
		t.Errorf("stream = %v", got)
	}
	c = codexStream(t, "", nil)
	c.cfg.BaseURL = "http://127.0.0.1:1"
	if _, err := c.callResponses(context.Background(), []Message{{Role: "user", Content: "x"}}, nil); err == nil {
		t.Error("an unreachable host must be an error")
	}
}

func TestResponsesBodyReplaysTheConversation(t *testing.T) {
	c := codexStream(t, "", nil)
	body := c.responsesBody([]Message{
		{Role: "assistant", Content: "earlier answer"},
	}, nil, true)
	input := body["input"].([]map[string]any)
	if input[0]["role"] != "assistant" || body["reasoning"] == nil {
		t.Errorf("body = %v", body)
	}
	for raw, want := range map[string]string{
		"": "{}", `"{\"a\":1}"`: `{"a":1}`, `{"a":1}`: `{"a":1}`, `"unterminated`: `"unterminated`,
	} {
		if got := argumentsString(json.RawMessage(raw)); got != want {
			t.Errorf("argumentsString(%s) = %s", raw, got)
		}
	}
}

// --- smaller helpers --------------------------------------------------------------

func TestToolSchemaShapes(t *testing.T) {
	if s := toolSchema(Tool{}); s["type"] != "object" {
		t.Errorf("nil parameters: %v", s)
	}
	bare := toolSchema(Tool{Function: FunctionDef{Parameters: map[string]any{"path": StringProperty("p")}}})
	if bare["type"] != "object" || bare["properties"].(map[string]any)["path"] == nil {
		t.Errorf("bare properties: %v", bare)
	}
}

func TestIsReasoningModel(t *testing.T) {
	for model, want := range map[string]bool{
		"openai/gpt-5": true, "o3-mini": true, "gpt-5.2": true, "gpt-4o": false, "o1": true, "gpt-50": false,
	} {
		if got := isReasoningModel(model); got != want {
			t.Errorf("%s: %v", model, got)
		}
	}
}

func TestGeminiCallWithoutArgumentsGetsAnObject(t *testing.T) {
	withLogin(t, oauth.Credential{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"g1","name":"list_skills"}}]}}]}`)
	}))
	defer srv.Close()
	c, _ := New(config.LLM{Provider: "gemini", Model: "m", APIKey: "k", BaseURL: srv.URL, MaxAttempts: 1}, nil)
	reply, err := c.CompleteTools(
		context.Background(),
		[]Message{{Role: "user", Content: "x"}},
		[]Tool{NewTool("list_skills", "l", ObjectSchema(map[string]any{}))},
	)
	if err != nil || len(reply.Calls) != 1 || reply.Calls[0].ID != "g1" ||
		string(reply.Calls[0].Function.Arguments) != "{}" {
		t.Errorf("reply=%+v err=%v", reply, err)
	}
}

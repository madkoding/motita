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

func TestToolHelpers(t *testing.T) {
	tool := NewTool("add", "Adds two numbers", ObjectSchema(map[string]any{
		"a": IntegerProperty("first operand"),
		"b": IntegerProperty("second operand"),
	}, "a", "b"))

	if tool.Type != "function" {
		t.Errorf("tool type = %q", tool.Type)
	}
	if tool.Function.Name != "add" {
		t.Errorf("tool name = %q", tool.Function.Name)
	}
	if tool.Function.Description != "Adds two numbers" {
		t.Errorf("tool description = %q", tool.Function.Description)
	}
	required, ok := tool.Function.Parameters["required"].([]string)
	if !ok || len(required) != 2 || required[0] != "a" || required[1] != "b" {
		t.Errorf("required = %v", tool.Function.Parameters["required"])
	}

	if properties, ok := tool.Function.Parameters["properties"].(map[string]any); ok {
		if a, ok := properties["a"].(map[string]any); !ok || a["type"] != "integer" {
			t.Errorf("property a = %v", a)
		}
	} else {
		t.Errorf("properties missing")
	}
}

func TestStringAndIntegerProperties(t *testing.T) {
	str := StringProperty("a string")
	if str["type"] != "string" || str["description"] != "a string" {
		t.Errorf("string property = %v", str)
	}
	integer := IntegerProperty("an integer")
	if integer["type"] != "integer" || integer["description"] != "an integer" {
		t.Errorf("integer property = %v", integer)
	}
}

func TestObjectSchemaOptionalRequired(t *testing.T) {
	without := ObjectSchema(map[string]any{"x": StringProperty("x")})
	if _, ok := without["required"]; ok {
		t.Error("required must not be present when not requested")
	}
	with := ObjectSchema(map[string]any{"x": StringProperty("x")}, "x")
	if _, ok := with["required"]; !ok {
		t.Error("required must be present when requested")
	}
}

func TestReplyWantsTools(t *testing.T) {
	if (Reply{}).WantsTools() {
		t.Error("an empty reply must not want tools")
	}
	if !(Reply{Calls: []ToolCall{{ID: "1"}}}).WantsTools() {
		t.Error("a reply with calls must want tools")
	}
}

func TestDecodeArgumentsStringified(t *testing.T) {
	fc := FunctionCall{
		Name:      "add",
		Arguments: json.RawMessage(`"{\"a\":1,\"b\":2}"`),
	}
	var dest map[string]int
	if err := fc.DecodeArguments(&dest); err != nil {
		t.Fatalf("error: %v", err)
	}
	if dest["a"] != 1 || dest["b"] != 2 {
		t.Errorf("dest = %v", dest)
	}
}

func TestDecodeArgumentsObject(t *testing.T) {
	fc := FunctionCall{
		Name:      "add",
		Arguments: json.RawMessage(`{"a":3,"b":4}`),
	}
	var dest map[string]int
	if err := fc.DecodeArguments(&dest); err != nil {
		t.Fatalf("error: %v", err)
	}
	if dest["a"] != 3 || dest["b"] != 4 {
		t.Errorf("dest = %v", dest)
	}
}

func TestDecodeArgumentsWhitespaceAroundString(t *testing.T) {
	fc := FunctionCall{
		Arguments: json.RawMessage(`  "{\"x\":9}"  `),
	}
	var dest map[string]int
	if err := fc.DecodeArguments(&dest); err != nil || dest["x"] != 9 {
		t.Errorf("dest = %v err = %v", dest, err)
	}
}

func TestDecodeArgumentsWhitespaceObject(t *testing.T) {
	fc := FunctionCall{
		Arguments: json.RawMessage(`  {"x": 9}  `),
	}
	var dest map[string]int
	if err := fc.DecodeArguments(&dest); err != nil || dest["x"] != 9 {
		t.Errorf("dest = %v err = %v", dest, err)
	}
}

func TestDecodeArgumentsEmptyString(t *testing.T) {
	fc := FunctionCall{
		Arguments: json.RawMessage(`""`),
	}
	dest := map[string]any{"pre": "set"}
	if err := fc.DecodeArguments(&dest); err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(dest) != 1 || dest["pre"] != "set" {
		t.Errorf("dest must stay untouched: %v", dest)
	}
}

func TestDecodeArgumentsEmpty(t *testing.T) {
	fc := FunctionCall{Name: "noop"}
	dest := map[string]any{"pre": "set"}
	if err := fc.DecodeArguments(&dest); err != nil {
		t.Fatalf("empty arguments must not error: %v", err)
	}
	if len(dest) != 1 || dest["pre"] != "set" {
		t.Errorf("empty arguments must leave dest untouched: %v", dest)
	}
}

func TestDecodeArgumentsInvalidStringified(t *testing.T) {
	fc := FunctionCall{
		Arguments: json.RawMessage(`"not json"`),
	}
	var dest map[string]any
	if err := fc.DecodeArguments(&dest); err == nil {
		t.Error("invalid stringified JSON must error")
	}
}

func TestDecodeArgumentsInvalidObject(t *testing.T) {
	fc := FunctionCall{
		Arguments: json.RawMessage(`{broken`),
	}
	var dest map[string]any
	if err := fc.DecodeArguments(&dest); err == nil {
		t.Error("invalid object JSON must error")
	}
}

func TestDecodeArgumentsTypeMismatch(t *testing.T) {
	fc := FunctionCall{
		Arguments: json.RawMessage(`{"value":"not a number"}`),
	}
	var dest struct {
		Value int `json:"value"`
	}
	if err := fc.DecodeArguments(&dest); err == nil {
		t.Error("a type mismatch must error")
	}
}

// --- CompleteTools provider paths -------------------------------------------

func TestCompleteToolsOpenAI(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok","tool_calls":[{"id":"call_1","type":"function","function":{"name":"add","arguments":"{\"a\":1}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	reply, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, []Tool{
		NewTool("add", "add", ObjectSchema(map[string]any{"a": IntegerProperty("a")})),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if reply.Content != "ok" {
		t.Errorf("content = %q", reply.Content)
	}
	if len(reply.Calls) != 1 || reply.Calls[0].Function.Name != "add" {
		t.Errorf("calls = %v", reply.Calls)
	}
	if reply.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q", reply.FinishReason)
	}
	if received["tools"] == nil {
		t.Error("tools must be sent")
	}
	var args map[string]int
	if err := reply.Calls[0].Function.DecodeArguments(&args); err != nil || args["a"] != 1 {
		t.Errorf("arguments decoding failed: err=%v args=%v", err, args)
	}
}

func TestCompleteToolsOpenAIEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Error("empty choices must error")
	}
}

func TestCompleteToolsAnthropic(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		fmt.Fprint(w, `{"content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"call_1","name":"add","input":{"a":2}}]}`)
	}))
	defer srv.Close()

	c, _ := New(config.LLM{Provider: "anthropic", APIKey: "key", BaseURL: srv.URL}, logx.Global())
	c.sleep = func(time.Duration) {}
	reply, err := c.CompleteTools(context.Background(), []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}, []Tool{
		NewTool("add", "add", ObjectSchema(map[string]any{"a": IntegerProperty("a")})),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if reply.Content != "hello" {
		t.Errorf("content = %q", reply.Content)
	}
	if len(reply.Calls) != 1 || reply.Calls[0].ID != "call_1" {
		t.Errorf("calls = %v", reply.Calls)
	}
	var args map[string]int
	if err := reply.Calls[0].Function.DecodeArguments(&args); err != nil || args["a"] != 2 {
		t.Errorf("arguments decoding failed: err=%v args=%v", err, args)
	}
	if received["system"] != "sys" {
		t.Errorf("system = %v", received["system"])
	}
	if received["tools"] == nil {
		t.Error("tools must be sent")
	}
}

func TestCompleteToolsGemini(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"hi"},{"functionCall":{"name":"add","args":{"a":3}}}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	c, _ := New(config.LLM{Provider: "gemini", APIKey: "key", BaseURL: srv.URL, Model: "gemini-pro"}, logx.Global())
	c.sleep = func(time.Duration) {}
	reply, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, []Tool{
		NewTool("add", "add", ObjectSchema(map[string]any{"a": IntegerProperty("a")})),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if reply.Content != "hi" {
		t.Errorf("content = %q", reply.Content)
	}
	if len(reply.Calls) != 1 || reply.Calls[0].Function.Name != "add" {
		t.Errorf("calls = %v", reply.Calls)
	}
	var args map[string]int
	if err := reply.Calls[0].Function.DecodeArguments(&args); err != nil || args["a"] != 3 {
		t.Errorf("arguments decoding failed: err=%v args=%v", err, args)
	}
	if received["tools"] == nil {
		t.Error("tools must be sent")
	}
}

func TestCompleteToolsGeminiNoCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"candidates":[]}`)
	}))
	defer srv.Close()

	c, _ := New(config.LLM{Provider: "gemini", APIKey: "key", BaseURL: srv.URL, Model: "gemini-pro"}, logx.Global())
	c.sleep = func(time.Duration) {}
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Error("no candidates must error")
	}
}

func TestCompleteToolsRetryAndCancel(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	c.cfg.MaxAttempts = 3
	reply, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if reply.Content != "ok" || attempts != 2 {
		t.Errorf("content=%q attempts=%d", reply.Content, attempts)
	}
}

func TestCompleteToolsOpenAIUnreadableAndErrorInBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Error("unreadable response must error")
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"bad"}}`)
	}))
	defer srv2.Close()

	c2 := newTestClient(t, srv2.URL)
	if _, err := c2.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Errorf("err = %v", err)
	}
}

func TestCompleteToolsAnthropicToolAndEmptyBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()

	c, _ := New(config.LLM{Provider: "anthropic", APIKey: "key", BaseURL: srv.URL}, logx.Global())
	c.sleep = func(time.Duration) {}
	reply, err := c.CompleteTools(context.Background(), []Message{
		{Role: "system", Content: "first"},
		{Role: "system", Content: "second"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Function: FunctionCall{Name: "add", Arguments: []byte(`{"a":1}`)}}}},
		{Role: "tool", ToolCallID: "tc1", Content: "done"},
		{Role: "empty", Content: ""},
	}, []Tool{NewTool("add", "add", ObjectSchema(map[string]any{"a": IntegerProperty("a")}))})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if reply.Content != "ok" {
		t.Errorf("content = %q", reply.Content)
	}
}

func TestCompleteToolsGeminiSystemToolAndEmptyParts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	c, _ := New(config.LLM{Provider: "gemini", APIKey: "key", BaseURL: srv.URL, Model: "gemini-pro"}, logx.Global())
	c.sleep = func(time.Duration) {}
	reply, err := c.CompleteTools(context.Background(), []Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "add", Arguments: []byte(`{"a":1}`)}}}},
		{Role: "tool", ToolCallID: "tc1", Content: "done"},
		{Role: "empty", Content: ""},
	}, []Tool{NewTool("add", "add", ObjectSchema(map[string]any{"a": IntegerProperty("a")}))})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if reply.Content != "ok" {
		t.Errorf("content = %q", reply.Content)
	}
}
func TestCompleteToolsAnthropicUnreadableAndErrorInBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	c, _ := New(config.LLM{Provider: "anthropic", APIKey: "key", BaseURL: srv.URL}, logx.Global())
	c.sleep = func(time.Duration) {}
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Error("unreadable response must error")
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"overloaded"}}`)
	}))
	defer srv2.Close()

	c2, _ := New(config.LLM{Provider: "anthropic", APIKey: "key", BaseURL: srv2.URL}, logx.Global())
	c2.sleep = func(time.Duration) {}
	if _, err := c2.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil || !strings.Contains(err.Error(), "overloaded") {
		t.Errorf("err = %v", err)
	}
}

func TestCompleteToolsGeminiUnreadableAndErrorInBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	c, _ := New(config.LLM{Provider: "gemini", APIKey: "key", BaseURL: srv.URL, Model: "gemini-pro"}, logx.Global())
	c.sleep = func(time.Duration) {}
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Error("unreadable response must error")
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"quota exhausted"}}`)
	}))
	defer srv2.Close()

	c2, _ := New(config.LLM{Provider: "gemini", APIKey: "key", BaseURL: srv2.URL, Model: "gemini-pro"}, logx.Global())
	c2.sleep = func(time.Duration) {}
	if _, err := c2.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil || !strings.Contains(err.Error(), "quota exhausted") {
		t.Errorf("err = %v", err)
	}
}

func TestDecodeArgumentsWhitespaceOnly(t *testing.T) {
	fc := FunctionCall{
		Arguments: json.RawMessage(`   `),
	}
	dest := map[string]any{"pre": "set"}
	if err := fc.DecodeArguments(&dest); err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(dest) != 1 || dest["pre"] != "set" {
		t.Errorf("dest must stay untouched: %v", dest)
	}
}

func TestDecodeArgumentsInvalidStringifiedWithUnmarshalError(t *testing.T) {
	fc := FunctionCall{
		Arguments: json.RawMessage(`"not closed`),
	}
	var dest map[string]any
	if err := fc.DecodeArguments(&dest); err == nil {
		t.Error("malformed JSON string must error")
	}
}

func TestCompleteToolsAnthropicAndGeminiTransportError(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			c, _ := New(config.LLM{
				Provider: provider, APIKey: "key",
				BaseURL: "http://127.0.0.1:1", MaxAttempts: 1, Timeout: time.Second,
				Model: "gemini-pro",
			}, logx.Global())
			c.sleep = func(time.Duration) {}
			if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
				t.Error("a transport error must be reported")
			}
		})
	}
}

func TestCompleteToolsDoesNotRetryInvalidCredentials(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	c.cfg.MaxAttempts = 5
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Fatal("an error was expected")
	}
	if attempts != 1 {
		t.Errorf("a 401 must not be retried: attempts=%d", attempts)
	}
}

func TestCompleteToolsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	c.cfg.MaxAttempts = 5
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, err := c.CompleteTools(ctx, []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Fatal("an error was expected because of the cancelled context")
	}
}

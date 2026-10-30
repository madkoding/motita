package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/oauth"
)

var replySchema = json.RawMessage(`{"type":"object","properties":{"done":{"type":"boolean"}}}`)

// schemaServer answers every request with reply, after refusing the first `refuse` ones with
// status, and records each request body.
type schemaServer struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (s *schemaServer) start(t *testing.T, status, refuse int, reply string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		n := len(s.bodies)
		s.mu.Unlock()
		if n <= refuse {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"unknown field"}}`)
			return
		}
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (s *schemaServer) body(i int) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[i]
}

const openAIReply = `{"choices":[{"message":{"role":"assistant","content":"{\"done\":true}"},"finish_reason":"stop"}]}`

func TestOpenAIAsksForTheSchema(t *testing.T) {
	ctx := WithJSONSchema(context.Background(), replySchema)
	var s schemaServer
	c := testClient(t, "ollama", s.start(t, 200, 0, openAIReply), nil)
	if text, err := c.Complete(ctx, hi); err != nil || text != `{"done":true}` {
		t.Fatalf("Complete = %q, %v", text, err)
	}
	format, _ := s.body(0)["response_format"].(map[string]any)
	inner, _ := format["json_schema"].(map[string]any)
	if format["type"] != "json_schema" || inner["strict"] != false || inner["schema"] == nil {
		t.Errorf("response_format = %v", s.body(0)["response_format"])
	}

	// No schema in the context, or tools offered: no response_format.
	if _, err := c.Complete(context.Background(), hi); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CompleteTools(ctx, hi, []Tool{{Type: "function", Function: FunctionDef{Name: "x"}}}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if _, ok := s.body(i)["response_format"]; ok {
			t.Errorf("request %d carries a schema it should not", i)
		}
	}
}

func TestOpenAIStreamAsksForTheSchema(t *testing.T) {
	ctx := WithJSONSchema(context.Background(), replySchema)
	var s schemaServer
	c := testClient(t, "openai", s.start(t, 200, 0,
		`data: {"choices":[{"delta":{"content":"{}"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n"), nil)
	if text, err := c.CompleteStream(ctx, hi, func(string, bool) {}); err != nil || text != "{}" {
		t.Fatalf("CompleteStream = %q, %v", text, err)
	}
	if _, ok := s.body(0)["response_format"]; !ok || s.body(0)["stream"] != true {
		t.Errorf("body = %v", s.body(0))
	}
}

// TestARefusedSchemaIsDroppedOnce: a server that does not know the field refuses the request; it is
// asked once more without it, and not sent one again.
func TestARefusedSchemaIsDroppedOnce(t *testing.T) {
	ctx := WithJSONSchema(context.Background(), replySchema)
	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		var s schemaServer
		c := testClient(t, "qwen", s.start(t, status, 1, openAIReply), nil)
		if text, err := c.Complete(ctx, hi); err != nil || text != `{"done":true}` {
			t.Fatalf("%d: Complete = %q, %v", status, text, err)
		}
		if _, ok := s.body(1)["response_format"]; ok {
			t.Errorf("%d: the second request still carries the schema", status)
		}
		if _, err := c.Complete(ctx, hi); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.body(2)["response_format"]; ok || !c.noSchema.Load() {
			t.Errorf("%d: a refused schema is sent again", status)
		}
	}

	// A refusal of a request without a schema, or not a 400, is the error it always was.
	var s schemaServer
	c := testClient(t, "openai", s.start(t, http.StatusBadRequest, 1, openAIReply), nil)
	if _, err := c.Complete(context.Background(), hi); err == nil || len(s.bodies) != 1 {
		t.Errorf("err = %v after %d requests", err, len(s.bodies))
	}
	s = schemaServer{}
	c = testClient(t, "openai", s.start(t, http.StatusForbidden, 1, openAIReply), nil)
	if _, err := c.Complete(ctx, hi); err == nil || len(s.bodies) != 1 || c.noSchema.Load() {
		t.Errorf("err = %v after %d requests", err, len(s.bodies))
	}
	// Refused again without the schema: one retry, then the error.
	s = schemaServer{}
	c = testClient(t, "openai", s.start(t, http.StatusBadRequest, 5, openAIReply), nil)
	if _, err := c.Complete(ctx, hi); err == nil || len(s.bodies) != 2 {
		t.Errorf("err = %v after %d requests", err, len(s.bodies))
	}
}

func TestGeminiAsksForTheSchema(t *testing.T) {
	ctx := WithJSONSchema(context.Background(), replySchema)
	var s schemaServer
	c := testClient(t, "gemini", s.start(t, http.StatusBadRequest, 1,
		`{"candidates":[{"content":{"parts":[{"text":"{}"}]},"finishReason":"STOP"}]}`), nil)
	if text, err := c.Complete(ctx, hi); err != nil || text != "{}" {
		t.Fatalf("Complete = %q, %v", text, err)
	}
	first, _ := s.body(0)["generationConfig"].(map[string]any)
	if first["responseMimeType"] != "application/json" || first["responseJsonSchema"] == nil {
		t.Errorf("generationConfig = %v", first)
	}
	second, _ := s.body(1)["generationConfig"].(map[string]any)
	if _, ok := second["responseJsonSchema"]; ok || second["maxOutputTokens"] == nil {
		t.Errorf("the retry must keep the rest and drop the schema: %v", second)
	}
}

func TestResponsesAskForTheSchema(t *testing.T) {
	withLogin(t, oauth.Credential{})
	ctx := WithJSONSchema(context.Background(), replySchema)
	var s schemaServer
	url := s.start(t, 200, 0, `data: {"type":"response.output_text.delta","delta":"{}"}`+"\n"+
		`data: {"type":"response.completed"}`+"\n")
	c, err := New(config.LLM{Provider: "codex", Model: "gpt-5-codex", APIKey: "k", BaseURL: url, MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := c.Complete(ctx, hi); err != nil || text != "{}" {
		t.Fatalf("Complete = %q, %v", text, err)
	}
	text, _ := s.body(0)["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("text = %v", s.body(0)["text"])
	}
}

func TestDropSchema(t *testing.T) {
	if out, ok := dropSchema([]byte("not json")); ok || string(out) != "not json" {
		t.Errorf("dropSchema(not json) = %q, %v", out, ok)
	}
	if out, ok := dropSchema([]byte(`{"model":"m"}`)); ok || string(out) != `{"model":"m"}` {
		t.Errorf("dropSchema(no schema) = %q, %v", out, ok)
	}
	out, ok := dropSchema([]byte(`{"model":"m","text":{"format":{},"verbosity":"low"}}`))
	if !ok || string(out) != `{"model":"m","text":{"verbosity":"low"}}` {
		t.Errorf("dropSchema(text.format) = %s, %v", out, ok)
	}
}

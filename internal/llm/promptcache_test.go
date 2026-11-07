package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// cacheTransport answers every request with the next of answers (the last one repeats) and keeps
// each request body it saw.
type cacheTransport struct {
	answers []cacheAnswer
	bodies  []map[string]any
}

type cacheAnswer struct {
	status int
	body   string
}

func (t *cacheTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	t.bodies = append(t.bodies, body)
	a := t.answers[min(len(t.bodies)-1, len(t.answers)-1)]
	return &http.Response{StatusCode: a.status, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(a.body)), Request: r}, nil
}

const chatOK = `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`

func cacheClient(t *testing.T, provider, model, base string, answers ...cacheAnswer) (*Client, *cacheTransport) {
	t.Helper()
	if len(answers) == 0 {
		answers = []cacheAnswer{{200, chatOK}}
	}
	c, err := New(config.LLM{Provider: provider, Model: model, APIKey: "k", BaseURL: base}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	rt := &cacheTransport{answers: answers}
	c.WithTransport(rt)
	return c, rt
}

// partsOf is the content parts of message i of a recorded /chat/completions body.
func partsOf(body map[string]any, i int) []any {
	msgs, _ := body["messages"].([]any)
	msg, _ := msgs[i].(map[string]any)
	parts, _ := msg["content"].([]any)
	return parts
}

// contentOf is the plain-string content of message i, "" when it is a list of parts.
func contentOf(body map[string]any, i int) string {
	msgs, _ := body["messages"].([]any)
	msg, _ := msgs[i].(map[string]any)
	text, _ := msg["content"].(string)
	return text
}

func marked(part any) bool {
	p, _ := part.(map[string]any)
	_, ok := p["cache_control"]
	return ok
}

func TestPromptCacheKey(t *testing.T) {
	base := []Message{
		{Role: "system", Content: "rules"}, {Role: "system", Content: "summary"}, {Role: "user", Content: "a"},
	}
	key := promptCacheKey("gpt-5", base)
	if !strings.HasPrefix(key, "motita-") || len(key) != len("motita-")+24 {
		t.Fatalf("key = %q", key)
	}
	later := append(append([]Message{}, base...), Message{Role: "assistant", Content: "b"},
		Message{Role: "system", Content: "brief of this turn"})
	if promptCacheKey("gpt-5", later) != key {
		t.Error("what comes after the leading system messages changed the key")
	}
	if promptCacheKey("gpt-4.1", base) == key {
		t.Error("another model shares the key")
	}
	if promptCacheKey("gpt-5", []Message{{Role: "system", Content: "other"}}) == key {
		t.Error("another system prompt shares the key")
	}
	if got := promptCacheKey("gpt-5", []Message{{Role: "user", Content: "a"}}); got != "" {
		t.Errorf("no system message, key = %q", got)
	}
}

func TestOpenAIOwnAPIGetsTheCacheKey(t *testing.T) {
	msgs := []Message{{Role: "system", Content: "rules"}, {Role: "user", Content: "hi"}}
	c, rt := cacheClient(t, "openai", "gpt-5", "https://api.openai.com/v1")
	if _, err := c.Complete(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if rt.bodies[0]["prompt_cache_key"] != promptCacheKey("gpt-5", msgs) {
		t.Errorf("prompt_cache_key = %v", rt.bodies[0]["prompt_cache_key"])
	}
	if partsOf(rt.bodies[0], 0) != nil {
		t.Error("OpenAI was sent DashScope marks")
	}

	other, rt2 := cacheClient(t, "openai", "gpt-5", "https://llm.example.com/v1")
	if _, err := other.Complete(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if _, has := rt2.bodies[0]["prompt_cache_key"]; has {
		t.Error("a server that is not OpenAI's own was sent prompt_cache_key")
	}
}

func TestExplicitQwenCache(t *testing.T) {
	for _, tc := range []struct {
		provider, model, base string
		want                  bool
	}{
		{"qwen", "qwen-plus", "", true},
		{"qwen", "qwen3-coder-plus-2025-09-23", "", true},
		{"custom", "qwen/qwen3.6-plus", "https://dashscope.aliyuncs.com/compatible-mode/v1", true},
		{"qwen", "qwen-turbo", "", false},
		{"qwen", "qwen-plusplus", "", false},
		{"openai", "qwen-plus", "https://llm.example.com/v1", false},
		{"qwen", "qwen-plus", "http://[::1", false},
	} {
		c := &Client{cfg: config.LLM{Provider: tc.provider, Model: tc.model, BaseURL: tc.base}}
		if got := c.explicitQwenCache(); got != tc.want {
			t.Errorf("%s %s %s: %v, want %v", tc.provider, tc.model, tc.base, got, tc.want)
		}
	}
	c := &Client{cfg: config.LLM{Provider: "qwen", Model: "qwen-plus"}}
	c.noCache.Store(true)
	if c.explicitQwenCache() {
		t.Error("a provider that refused the marks is sent them again")
	}
}

func TestQwenMarksSystemStableStartAndLastTurn(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "INSTRUCTIONS task", Stable: len("INSTRUCTIONS ")},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "1", Type: "function", Function: FunctionCall{Name: "f"}}}},
		{Role: "tool", ToolCallID: "1", Content: "result"},
		{Role: "assistant", Content: "thinking aloud"},
	}
	c, rt := cacheClient(t, "qwen", "qwen-plus", "")
	if _, err := c.Complete(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	body := rt.bodies[0]
	if p := partsOf(body, 0); len(p) != 1 || !marked(p[0]) {
		t.Errorf("system = %v", p)
	}
	if p := partsOf(body, 1); len(p) != 2 || !marked(p[0]) || marked(p[1]) {
		t.Errorf("stable start = %v", p)
	}
	if p := partsOf(body, 3); len(p) != 1 || !marked(p[0]) {
		t.Errorf("last turn with text (the tool result) = %v", p)
	}
	if p := partsOf(body, 4); p != nil {
		t.Errorf("an assistant turn was marked: %v", p)
	}
}

func TestMarkQwenCacheEdges(t *testing.T) {
	img := []Image{{MediaType: "image/png", Data: []byte{1}}}
	in := []Message{
		{Role: "user", Content: "look", Stable: 2, Images: img}, // a picture: no split
		{Role: "user", Content: "   tail", Stable: 3},           // a blank head: no split
		{Role: "user", Content: "shot", Images: img},
	}
	out := toOpenAIMessages(in)
	markQwenCache(out, in)
	if s, ok := out[1].Content.(string); !ok || s != "   tail" {
		t.Errorf("a blank stable head was split: %v", out[1].Content)
	}
	last := out[2].Content.([]map[string]any)
	if _, ok := last[len(last)-1]["cache_control"]; !ok || last[0]["cache_control"] != nil {
		t.Errorf("the last part of a turn with a picture is not the marked one: %v", last)
	}

	// Nothing to mark: no system message, no stable start, no turn with text.
	none := []Message{{Role: "assistant", Content: "x"}}
	out = toOpenAIMessages(none)
	markQwenCache(out, none)
	if _, ok := out[0].Content.(string); !ok {
		t.Errorf("an assistant turn was marked: %v", out[0].Content)
	}
	if got := asParts(42); len(got) != 1 || got[0]["text"] != "" {
		t.Errorf("asParts of a non-text content = %v", got)
	}
}

func TestRefusedCacheMarksAreDroppedAndNotSentAgain(t *testing.T) {
	msgs := []Message{{Role: "system", Content: "rules"}, {Role: "user", Content: "hi"}}
	c, rt := cacheClient(t, "qwen", "qwen-plus", "",
		cacheAnswer{400, `{"error":{"message":"cache_control is not supported by this model"}}`},
		cacheAnswer{200, chatOK})
	if _, err := c.Complete(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if len(rt.bodies) != 2 || !marked(partsOf(rt.bodies[0], 0)[0]) || contentOf(rt.bodies[1], 0) != "rules" {
		t.Fatalf("bodies = %v", rt.bodies)
	}
	if _, err := c.Complete(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if partsOf(rt.bodies[2], 0) != nil {
		t.Errorf("the marks were sent again: %v", rt.bodies[2])
	}

	// The OpenAI key is dropped the same way.
	o, ort := cacheClient(t, "openai", "gpt-5", "https://api.openai.com/v1",
		cacheAnswer{400, `{"error":{"message":"Unrecognized request argument: prompt_cache_key"}}`},
		cacheAnswer{200, chatOK})
	if _, err := o.Complete(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if _, has := ort.bodies[1]["prompt_cache_key"]; has || !o.noCache.Load() {
		t.Errorf("the key was kept: %v", ort.bodies[1])
	}
}

func TestRefusalThatIsNotTheCacheKeepsTheMarks(t *testing.T) {
	c := &Client{log: logx.Global()}
	data := []byte(`{"prompt_cache_key":"k"}`)
	if c.refusedCache(&data, 500, []byte("cache")) || c.refusedCache(&data, 400, []byte("bad model")) {
		t.Error("a refusal that does not name the cache dropped it")
	}
	bad := []byte("not json")
	if c.refusedCache(&bad, 400, []byte("cache")) {
		t.Error("an unreadable body was rewritten")
	}
	plain := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	if c.refusedCache(&plain, 400, []byte("cache")) {
		t.Error("a body without cache fields was asked again")
	}
	if c.noCache.Load() {
		t.Error("noCache set without a cache refusal")
	}
}

func TestResponsesKeepsLaterSystemMessagesInPlace(t *testing.T) {
	c := &Client{cfg: config.LLM{Provider: "codex", Model: "gpt-5-codex"}}
	msgs := []Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "q"},
		{Role: "system", Content: "brief"},
	}
	body := c.responsesBody(msgs, nil, true)
	if body["instructions"] != "rules" {
		t.Errorf("instructions = %v", body["instructions"])
	}
	input := body["input"].([]map[string]any)
	if len(input) != 2 || input[1]["role"] != "developer" {
		t.Errorf("input = %v", input)
	}
	if body["prompt_cache_key"] != promptCacheKey("gpt-5-codex", msgs) {
		t.Errorf("prompt_cache_key = %v", body["prompt_cache_key"])
	}
	c.noCache.Store(true)
	if _, has := c.responsesBody(msgs, nil, true)["prompt_cache_key"]; has {
		t.Error("the key was sent after a refusal")
	}
}

func TestGeminiKeepsLaterSystemMessagesInTheConversation(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "answer"},
		{Role: "system", Content: "brief"},
		{Role: "user", Content: "second"},
	}
	for name, convert := range map[string]func([]Message) (string, []map[string]any){
		"text": geminiTextContents, "tools": toGeminiContents,
	} {
		system, contents := convert(msgs)
		if system != "rules\n" {
			t.Errorf("%s: system = %q", name, system)
		}
		if len(contents) != 3 {
			t.Fatalf("%s: contents = %v", name, contents)
		}
		last := contents[2]["parts"].([]map[string]any)
		if contents[2]["role"] != "user" || len(last) != 2 || last[0]["text"] != "brief" || last[1]["text"] != "second" {
			t.Errorf("%s: the brief is not with the turn it precedes: %v", name, contents[2])
		}
	}
}

// A provider that refuses the marks' shape without saying "cache" (a tool message whose content
// became a list of parts) is asked again without them, with the content back as plain strings.
// One such refusal does not switch the marks off; the second does.
func TestAnUnnamedRefusalOfTheMarksIsAskedAgainWithPlainContent(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "INSTRUCTIONS task", Stable: len("INSTRUCTIONS ")},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "1", Type: "function", Function: FunctionCall{Name: "f"}}}},
		{Role: "tool", ToolCallID: "1", Content: "result"},
	}
	bad := cacheAnswer{400, `{"error":{"message":"messages.3.content: expected a string"}}`}
	c, rt := cacheClient(t, "qwen", "qwen-plus", "", bad, cacheAnswer{200, chatOK}, bad, cacheAnswer{200, chatOK},
		cacheAnswer{200, chatOK})
	if _, err := c.Complete(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(rt.bodies[1], 3); got != "result" {
		t.Errorf("the tool message was not plain text again: %q", got)
	}
	if got := contentOf(rt.bodies[1], 1); got != "INSTRUCTIONS task" {
		t.Errorf("the split first turn was not joined again: %q", got)
	}
	if c.noCache.Load() {
		t.Fatal("one refusal that does not name the cache switched the marks off")
	}
	if _, err := c.Complete(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if !c.noCache.Load() {
		t.Fatal("the second refusal must switch the marks off")
	}
	if _, err := c.Complete(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if partsOf(rt.bodies[4], 3) != nil || contentOf(rt.bodies[4], 3) != "result" {
		t.Errorf("the marks were sent again: %v", rt.bodies[4])
	}
}

// Parts that are not all text (a picture) stay a list when their marks are dropped.
func TestTextOfPartsNeedsEveryPartToBeText(t *testing.T) {
	a := map[string]any{"type": "text", "text": "a"}
	b := map[string]any{"type": "text", "text": "b"}
	if got, ok := textOfParts([]any{a, b}); !ok || got != "ab" {
		t.Errorf("text parts = %q %v", got, ok)
	}
	for _, parts := range [][]any{
		{map[string]any{"type": "image_url"}},
		{map[string]any{"type": "text"}},
	} {
		if _, ok := textOfParts(parts); ok {
			t.Errorf("%v was joined as text", parts)
		}
	}
	image := `{"type":"image_url","cache_control":{}}`
	data := []byte(`{"messages":[{"role":"user","content":[` + image + `,{"type":"text","text":"x"}]}]}`)
	out, ok := dropCache(data)
	if !ok || !strings.Contains(string(out), `"image_url"`) || strings.Contains(string(out), "cache_control") ||
		strings.Contains(string(out), `"content":"x"`) {
		t.Errorf("dropCache = %s %v", out, ok)
	}
}

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// cacheMarks are the cache marks of a request body's blocks, by message, as "m<i>b<j>".
func cacheMarks(t *testing.T, body map[string]any) []string {
	t.Helper()
	var marks []string
	for i, m := range body["messages"].([]any) {
		for j, b := range m.(map[string]any)["content"].([]any) {
			if _, has := b.(map[string]any)["cache_control"]; has {
				marks = append(marks, fmt.Sprintf("m%db%d", i, j))
			}
		}
	}
	return marks
}

// long is text past every model's cache minimum, so its marks are kept.
var long = strings.Repeat("instructions that repeat on every call. ", 400)

// captureAnthropic is a Messages API stand-in that keeps the last request body.
func captureAnthropic(t *testing.T, model string) (*Client, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	t.Cleanup(srv.Close)
	c, _ := New(config.LLM{Provider: "anthropic", Model: model, APIKey: "k", BaseURL: srv.URL}, logx.Global())
	return c, &body
}

var shellTool = []Tool{{Type: "function", Function: FunctionDef{Name: "shell", Description: "run"}}}

// TestAnthropicKeepsTheStableStartAndTheConversation: a turn with a stable start is sent as two
// blocks, the first marked to be kept; a conversation with tools has its last block marked.
func TestAnthropicKeepsTheStableStartAndTheConversation(t *testing.T) {
	c, got := captureAnthropic(t, "claude-sonnet-5-5")
	turn := Message{Role: "user", Content: long + "## TASK\nround 3", Stable: len(long)}
	if _, err := c.Complete(context.Background(), []Message{turn}); err != nil {
		t.Fatal(err)
	}
	body := *got
	blocks := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["text"] != long {
		t.Fatalf("the stable start must be its own block: %v", blocks)
	}
	if marks := cacheMarks(t, body); len(marks) != 1 || marks[0] != "m0b0" {
		t.Errorf("only the stable start may be marked in a text call: %v", marks)
	}

	conv := []Message{
		{Role: "user", Content: "do it"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: long},
	}
	if _, err := c.CompleteTools(context.Background(), conv, shellTool); err != nil {
		t.Fatal(err)
	}
	if marks := cacheMarks(t, *got); len(marks) != 1 || marks[0] != "m2b0" {
		t.Errorf("the last block of a conversation with tools must be marked: %v", marks)
	}
}

// TestAnthropicAnchorsTheFirstSystemMessage: the first system message is sent as its own block,
// marked, so tools and instructions are read from the cache even when what follows changes; the
// turn's other system messages follow it unmarked.
func TestAnthropicAnchorsTheFirstSystemMessage(t *testing.T) {
	c, got := captureAnthropic(t, "claude-opus-5-5")
	msgs := []Message{
		{Role: "system", Content: long},
		{Role: "system", Content: "recalled this turn"},
		{Role: "system", Content: "  "},
		{Role: "user", Content: "hi"},
	}
	if _, err := c.CompleteTools(context.Background(), msgs, shellTool); err != nil {
		t.Fatal(err)
	}
	system := (*got)["system"].([]any)
	if len(system) != 2 {
		t.Fatalf("the system prompt must be the anchor and the rest: %v", system)
	}
	first, second := system[0].(map[string]any), system[1].(map[string]any)
	if first["text"] != long || first["cache_control"] == nil {
		t.Errorf("the first system message must be marked: %v", first)
	}
	if second["text"] != "recalled this turn" || second["cache_control"] != nil {
		t.Errorf("the turn's own system message must follow unmarked: %v", second)
	}
	if marks := cacheMarks(t, *got); len(marks) != 1 {
		t.Errorf("the conversation end is still marked: %v", marks)
	}

	// One system message only: one block, no second one.
	if _, err := c.Complete(context.Background(), []Message{msgs[0], msgs[3]}); err != nil {
		t.Fatal(err)
	}
	if system := (*got)["system"].([]any); len(system) != 1 {
		t.Errorf("one system message is one block: %v", system)
	}
}

// TestAnthropicAnchorsTheToolsWithoutASystemPrompt: with no system prompt, the last tool carries
// the anchor when the tool list alone is long enough.
func TestAnthropicAnchorsTheToolsWithoutASystemPrompt(t *testing.T) {
	c, got := captureAnthropic(t, "claude-opus-5-5")
	tools := []Tool{
		{Type: "function", Function: FunctionDef{Name: "a", Description: long}},
		{Type: "function", Function: FunctionDef{Name: "b", Description: "short"}},
	}
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, tools); err != nil {
		t.Fatal(err)
	}
	sent := (*got)["tools"].([]any)
	if sent[0].(map[string]any)["cache_control"] != nil || sent[1].(map[string]any)["cache_control"] == nil {
		t.Errorf("only the last tool carries the anchor: %v", sent)
	}
	if _, has := (*got)["system"]; has {
		t.Error("no system prompt must be sent")
	}
}

// TestAnthropicDropsMarksTooShortToCache: below the model's minimum nothing is marked, and the
// system prompt stays a plain string.
func TestAnthropicDropsMarksTooShortToCache(t *testing.T) {
	c, got := captureAnthropic(t, "claude-haiku-4-5")
	msgs := []Message{{Role: "system", Content: "short rules"}, {Role: "system", Content: "more"},
		{Role: "user", Content: "INSTRUCTIONS\n## TASK", Stable: len("INSTRUCTIONS\n")}}
	if _, err := c.CompleteTools(context.Background(), msgs, shellTool); err != nil {
		t.Fatal(err)
	}
	if (*got)["system"] != "short rules\n\nmore" {
		t.Errorf("a short system prompt stays one string: %v", (*got)["system"])
	}
	if marks := cacheMarks(t, *got); len(marks) != 0 {
		t.Errorf("no mark may be placed on a prefix too short to cache: %v", marks)
	}
	if _, err := c.CompleteTools(context.Background(), msgs[2:], shellTool); err != nil {
		t.Fatal(err)
	}
	if tools := (*got)["tools"].([]any); tools[0].(map[string]any)["cache_control"] != nil {
		t.Errorf("a short tool list carries no anchor: %v", tools)
	}
}

// TestFitMessageMarksKeepsTheLatestWithinTheLimit: past the slots left, the earliest marks go.
func TestFitMessageMarksKeepsTheLatestWithinTheLimit(t *testing.T) {
	var conv []map[string]any
	for range 6 {
		conv = append(conv, map[string]any{"role": "user", "content": []map[string]any{
			{"type": "text", "text": long, "cache_control": ephemeral}}})
	}
	fitMessageMarks(conv, 0, 10, 3)
	for i, turn := range conv {
		_, has := turn["content"].([]map[string]any)[0]["cache_control"]
		if has != (i >= 3) {
			t.Errorf("turn %d marked = %v: only the latest three may stay", i, has)
		}
	}
}

// TestCacheMinimumByModel: each family's minimum, through gateway and cloud prefixes.
func TestCacheMinimumByModel(t *testing.T) {
	for model, want := range map[string]int{
		"claude-opus-5-5":                     512,
		"anthropic/claude-sonnet-5-5":         512,
		"us.anthropic.claude-haiku-5-5":       512,
		"claude-fable-5-1":                    512,
		"claude-sonnet-5":                     1024,
		"claude-opus-4-8":                     1024,
		"claude-opus-4-7":                     2048,
		"claude-opus-4-5@20251101":            4096,
		"anthropic.claude-haiku-4-5-20251001": 4096,
		"claude-3-5-haiku-latest":             2048,
		"some-future-model":                   1024,
	} {
		if got := cacheMinimum(model); got != want {
			t.Errorf("%s: minimum %d, want %d", model, got, want)
		}
	}
}

// TestAnthropicTextBlocksNeverSplitIntoABlankBlock: a stable start that is the whole turn, none,
// or one that would leave a blank half is sent as one plain block.
func TestAnthropicTextBlocksNeverSplitIntoABlankBlock(t *testing.T) {
	for _, m := range []Message{
		{Content: "all of it", Stable: len("all of it")},
		{Content: "none", Stable: 0},
		{Content: "   \nrest", Stable: 3},
		{Content: "head\n   ", Stable: 5},
	} {
		blocks := anthropicTextBlocks(m)
		if len(blocks) != 1 || blocks[0]["cache_control"] != nil || blocks[0]["text"] != m.Content {
			t.Errorf("%q (stable %d): got %v", m.Content, m.Stable, blocks)
		}
	}
	if anthropicTextBlocks(Message{Content: "  "}) != nil {
		t.Error("a blank turn has no text block")
	}
	cacheConversation(nil) // nothing to mark, and no panic
}

// TestTheClientSpeaksHTTP2: the calls of a run share one connection where the provider allows it.
func TestTheClientSpeaksHTTP2(t *testing.T) {
	c, err := New(config.LLM{Provider: "openai", Model: "m", APIKey: "k"}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	if tr, ok := c.http.Transport.(*http.Transport); !ok || !tr.ForceAttemptHTTP2 {
		t.Error("the client's transport must attempt HTTP/2")
	}
}

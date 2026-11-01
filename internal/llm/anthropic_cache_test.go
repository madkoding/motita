package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// TestAnthropicKeepsTheStableStartAndTheConversation: a turn with a stable start is sent as two
// blocks, the first marked to be kept; a conversation with tools has its last block marked.
func TestAnthropicKeepsTheStableStartAndTheConversation(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()
	c, _ := New(config.LLM{Provider: "anthropic", Model: "claude-sonnet-5-5", APIKey: "k", BaseURL: srv.URL},
		logx.Global())

	turn := Message{Role: "user", Content: "INSTRUCTIONS\n## TASK\nround 3", Stable: len("INSTRUCTIONS\n")}
	if _, err := c.Complete(context.Background(), []Message{turn}); err != nil {
		t.Fatal(err)
	}
	blocks := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["text"] != "INSTRUCTIONS\n" {
		t.Fatalf("the stable start must be its own block: %v", blocks)
	}
	if got := cacheMarks(t, body); len(got) != 1 || got[0] != "m0b0" {
		t.Errorf("only the stable start may be marked in a text call: %v", got)
	}

	conv := []Message{{Role: "user", Content: "do it"}, {Role: "assistant", Content: "ok"}, {Role: "user", Content: "more"}}
	if _, err := c.CompleteTools(context.Background(), conv, []Tool{{Type: "function",
		Function: FunctionDef{Name: "shell", Description: "run"}}}); err != nil {
		t.Fatal(err)
	}
	if got := cacheMarks(t, body); len(got) != 1 || got[0] != "m2b0" {
		t.Errorf("the last block of a conversation with tools must be marked: %v", got)
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

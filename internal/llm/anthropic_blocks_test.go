package llm

import "testing"

// TestAnthropicTextConversationNeverSendsEmptyText: the Messages API answers HTTP 400 "text
// content blocks must be non-empty", for a turn or for the system prompt.
func TestAnthropicTextConversationNeverSendsEmptyText(t *testing.T) {
	system, conv := anthropicTextConversation([]Message{
		{Role: "system", Content: "  "},
		{Role: "system", Content: "rules"},
		{Role: "user", Content: ""},
		{Role: "assistant", Content: " \n"},
		{Role: "user", Content: "hi", Images: []Image{shot}},
	})
	if system != "rules" {
		t.Fatalf("system = %q", system)
	}
	for i, turn := range conv {
		for _, b := range turn["content"].([]map[string]any) {
			if b["type"] == "text" && nonEmpty(b["text"].(string)) != b["text"] {
				t.Fatalf("turn %d has an empty text block: %v", i, b)
			}
		}
	}
	if len(conv) != 3 || len(conv[2]["content"].([]map[string]any)) != 2 {
		t.Fatalf("unexpected conversation: %v", conv)
	}
}

// TestAnthropicToolsSystemSkipsBlank: a blank system message leaves no system prompt.
func TestAnthropicToolsSystemSkipsBlank(t *testing.T) {
	system, _ := toAnthropicMessages([]Message{{Role: "system", Content: ""}, {Role: "user", Content: "x"}})
	if system != "" {
		t.Fatalf("system = %q", system)
	}
}

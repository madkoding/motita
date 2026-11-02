package llm

import (
	"encoding/json"
	"strings"
)

// THE PROMPT CACHE. Anthropic re-reads every prompt from the start unless it is told which part
// it may keep, and an execution round sends the same ~6000 tokens of instructions every time:
// paid in full, and waited for in full before the first token of the answer. The prefix order is
// tools -> system -> messages, and up to four marks (the API's limit) say what to keep:
//
//   - the anchor (anchorCache): the end of the first system message, or of the tool list when
//     there is none. Tools and base instructions are the same in every round of a run and, in plan
//     mode, in every turn, so a later call reads them even when the conversation changed;
//   - the stable start of a turn (Message.Stable): the instructions before the task, the same in
//     every round of a run, so round two onwards reads them from the cache;
//   - the end of a conversation with tools (cacheConversation): each turn of a direct run repeats
//     the whole conversation before it, which the next turn then finds already kept.
//
// A mark whose prefix is shorter than the model's minimum is dropped (fitMessageMarks): the API
// would not cache it anyway, and it would only take one of the four slots. Writing the cache costs
// 1.25x the input price and reading it 0.1x, so a mark is only put where the bytes before it are
// sent again; the text path does not mark its end, which is unique to each call.

// maxCacheMarks is how many cache_control marks the Messages API accepts in one request.
const maxCacheMarks = 4

// bytesPerToken turns a prefix's bytes into a GENEROUS token estimate (English runs ~4 bytes a
// token, code ~3): a prefix is judged too short to cache only when it surely is.
const bytesPerToken = 3

// ephemeral is the cache mark of a content block: the default five-minute lifetime, which every
// read renews.
var ephemeral = map[string]any{"type": "ephemeral"}

// cacheMinimums is the shortest prefix, in tokens, each model family caches. The minimum is not
// monotonic across generations (4096 on Opus 4.5/4.6 and Haiku 4.5, 512 on the newest models).
// No prefix here is a prefix of another, so the order does not matter.
var cacheMinimums = []struct {
	prefix string
	tokens int
}{
	{"claude-opus-5", 512}, // Opus 5 and 5.5
	{"claude-sonnet-5-5", 512},
	{"claude-haiku-5", 512},
	{"claude-fable", 512},
	{"claude-mythos-5", 512},
	{"claude-mythos-preview", 2048},
	{"claude-opus-4-7", 2048},
	{"claude-opus-4-6", 4096},
	{"claude-opus-4-5", 4096},
	{"claude-haiku-4-5", 4096},
	{"claude-3-5-haiku", 2048},
	{"claude-3-haiku", 2048},
}

// defaultCacheMinimum is the minimum of every other model: Opus 4/4.1/4.8, Sonnet 4 to 5, and a
// model released after this table was written.
const defaultCacheMinimum = 1024

// bareModel is a model id without the gateway or cloud prefix around it: "anthropic/claude-x" on
// a gateway, "us.anthropic.claude-x" on Bedrock.
func bareModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if i := strings.LastIndex(m, "anthropic."); i >= 0 {
		m = m[i+len("anthropic."):]
	}
	return m
}

// cacheMinimum is the shortest prefix, in tokens, the model caches.
func cacheMinimum(model string) int {
	m := bareModel(model)
	for _, c := range cacheMinimums {
		if strings.HasPrefix(m, c.prefix) {
			return c.tokens
		}
	}
	return defaultCacheMinimum
}

// placeCacheMarks finishes a Messages request body with its cache marks: the anchor, then the
// marks already on the conversation's blocks, kept only when their prefix is long enough to be
// cached and never more than the API allows.
func (c *Client) placeCacheMarks(body map[string]any, messages []Message) {
	least := cacheMinimum(c.cfg.Model) * bytesPerToken
	before, used := anchorCache(body, systemParts(messages), least)
	if conversation, ok := body["messages"].([]map[string]any); ok {
		fitMessageMarks(conversation, before, least, maxCacheMarks-used)
	}
}

// systemParts are the non-blank system messages, in order: what joinSystem puts in the prompt.
func systemParts(messages []Message) []string {
	var parts []string
	for _, m := range messages {
		if m.Role == "system" && strings.TrimSpace(m.Content) != "" {
			parts = append(parts, m.Content)
		}
	}
	return parts
}

// anchorCache marks the end of the instructions every call of a run repeats: the first system
// message, sent as its own block, or the last tool when there is no system prompt. It returns the
// bytes before the conversation and how many marks it placed.
func anchorCache(body map[string]any, systems []string, least int) (int, int) {
	tools, _ := body["tools"].([]map[string]any)
	size := 0
	if len(tools) > 0 {
		size = jsonSize(tools)
	}
	if len(systems) == 0 {
		if len(tools) == 0 || size < least {
			return size, 0
		}
		tools[len(tools)-1]["cache_control"] = ephemeral
		return size, 1
	}
	rest := strings.Join(systems[1:], "\n\n")
	if size+len(systems[0]) < least {
		return size + len(systems[0]) + len(rest), 0
	}
	blocks := []map[string]any{{"type": "text", "text": systems[0], "cache_control": ephemeral}}
	if rest != "" {
		// A turn's own system messages (plan mode's recall) change between turns; kept apart, they
		// leave the instructions before them readable from the cache.
		blocks = append(blocks, map[string]any{"type": "text", "text": rest})
	}
	body["system"] = blocks
	return size + len(systems[0]) + len(rest), 1
}

// fitMessageMarks drops the marks of the conversation whose prefix (before bytes ahead of it,
// then every block up to the marked one) is too short to be cached, then the earliest ones while
// more than slots remain: the latest mark covers the most.
func fitMessageMarks(conversation []map[string]any, before, least, slots int) {
	var marked []map[string]any
	size := before
	for _, turn := range conversation {
		blocks, _ := turn["content"].([]map[string]any)
		for _, b := range blocks {
			size += jsonSize(b)
			if _, has := b["cache_control"]; !has {
				continue
			}
			if size < least {
				delete(b, "cache_control")
				continue
			}
			marked = append(marked, b)
		}
	}
	for ; len(marked) > slots; marked = marked[1:] {
		delete(marked[0], "cache_control")
	}
}

// jsonSize is how many bytes v takes in the request.
func jsonSize(v any) int {
	data, _ := json.Marshal(v)
	return len(data)
}

// anthropicTextBlocks are a turn's text blocks: one, or two when the turn has a stable start, the
// first marked to be kept. A turn without text has none.
func anthropicTextBlocks(m Message) []map[string]any {
	if strings.TrimSpace(m.Content) == "" {
		return nil
	}
	head, tail := m.Content, ""
	if m.Stable > 0 && m.Stable < len(m.Content) {
		head, tail = m.Content[:m.Stable], m.Content[m.Stable:]
	}
	if m.Stable <= 0 || strings.TrimSpace(head) == "" || strings.TrimSpace(tail) == "" {
		// Nothing stable to keep, or a split that would leave a blank block the API refuses.
		return []map[string]any{{"type": "text", "text": m.Content}}
	}
	return []map[string]any{
		{"type": "text", "text": head, "cache_control": ephemeral},
		{"type": "text", "text": tail},
	}
}

// cacheConversation marks the last block of a conversation with tools to be kept.
func cacheConversation(conversation []map[string]any) {
	if n := len(conversation); n > 0 {
		blocks := conversation[n-1]["content"].([]map[string]any)
		blocks[len(blocks)-1]["cache_control"] = ephemeral
	}
}

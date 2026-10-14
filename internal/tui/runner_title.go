package tui

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
)

// The title of a conversation, asked of the model and cleaned from its reply.

// GenerateTitle asks the model for a short descriptive title for the conversation.
// It uses a lightweight single-shot call (no tools, no agent loop) with a tight
// timeout. On any error or timeout it falls back to a truncated version of the
// first user message, so the caller always gets something usable.
func (r *AppRunner) GenerateTitle(ctx context.Context, firstUserMessage string) string {
	_, engine, err := r.engineFor(config.TaskSummary)
	if err != nil {
		return fallbackTitle(firstUserMessage)
	}
	prompt := "Generate a short, descriptive title (at most 6 words, no quotes, no trailing " +
		"punctuation) for a conversation that starts with this user message:\n\n" + firstUserMessage +
		"\n\nReply with ONLY the title, nothing else."
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	title, err := engine.Complete(tctx, []llm.Message{
		{Role: "system", Content: "You are a title generator. Respond with only a short title, no quotes, " +
			"no punctuation at the end. Use the same language as the user's message."},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return fallbackTitle(firstUserMessage)
	}
	title = titleFromReply(title)
	if title == "" {
		return fallbackTitle(firstUserMessage)
	}
	// Clean up: strip quotes, trailing punctuation, collapse whitespace, cap length.
	title = strings.Trim(title, "\"'`.,;:!?")
	title = strings.Join(strings.Fields(title), " ")
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:59]) + "…"
	}
	return title
}

// titleFromReply is the title in what the model answered, or "" when there is none.
//
// A model does not always answer with only a title, whatever it was asked: a reasoning model
// opens with a <think> block, another answers in JSON, another adds a line of explanation.
// Measured: a session was named `{"actions":[{"command":"printf ...` in the sidebar. The think
// block is dropped, a JSON reply gives its "title" field or nothing, and only the first line
// of prose counts.
func titleFromReply(reply string) string {
	for {
		start := strings.Index(reply, "<think>")
		if start < 0 {
			break
		}
		end := strings.Index(reply[start:], "</think>")
		if end < 0 {
			reply = reply[:start]
			break
		}
		reply = reply[:start] + reply[start+end+len("</think>"):]
	}
	reply = strings.TrimSpace(reply)
	if strings.HasPrefix(reply, "{") || strings.HasPrefix(reply, "[") {
		var shaped struct {
			Title string `json:"title"`
		}
		if json.Unmarshal([]byte(reply), &shaped) != nil {
			return ""
		}
		return strings.TrimSpace(shaped.Title)
	}
	line, _, _ := strings.Cut(reply, "\n")
	return strings.TrimSpace(line)
}

// fallbackTitle collapses whitespace and truncates the first user message to a
// short label. It is used when the LLM call fails or times out.
func fallbackTitle(text string) string {
	fields := strings.Fields(text)
	title := strings.Join(fields, " ")
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:59]) + "…"
	}
	return title
}

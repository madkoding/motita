package tui

import (
	"context"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
)

// SummarizeText asks the model for a short summary of a conversation, in the language it is
// written in. The notes scan uses it for long sessions. The caller drops an empty answer.
func (r *AppRunner) SummarizeText(ctx context.Context, text string) (string, error) {
	_, engine, err := r.engineFor(config.TaskSummary)
	if err != nil {
		return "", err
	}
	out, err := engine.Complete(ctx, []llm.Message{
		{Role: "system", Content: "You summarise conversations between a person and a coding agent for a " +
			"personal knowledge base. Reply with at most 8 short Markdown bullets: what was asked, what was " +
			"decided or done, and what is still open. Use the language of the conversation. Do not invent " +
			"anything that is not in the text."},
		{Role: "user", Content: text},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

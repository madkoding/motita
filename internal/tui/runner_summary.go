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

// DescribeProject asks the model to explain how a project fits together from the facts read out of
// its folder. The notes scan uses it; the caller drops an empty answer.
func (r *AppRunner) DescribeProject(ctx context.Context, text string) (string, error) {
	_, engine, err := r.engineFor(config.TaskSummary)
	if err != nil {
		return "", err
	}
	out, err := engine.Complete(ctx, []llm.Message{
		{Role: "system", Content: "You explain the architecture of a software project for a personal " +
			"knowledge base, from facts read out of its folder. Reply in Markdown with three short " +
			"sections: Modules (what each main module is for), Connections (which module uses which, and " +
			"why) and Flows (how a request or piece of data travels through the main paths). Use only what " +
			"the facts and the README say; when something is not there, say it is unknown. Do not invent " +
			"modules, files or behaviour."},
		{Role: "user", Content: text},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

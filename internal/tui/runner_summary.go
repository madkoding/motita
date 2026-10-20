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

// DescribeModule asks the model to explain one folder of a project, for its note in the project's
// manual, from its files, its API and excerpts of its source. The caller drops an empty answer.
func (r *AppRunner) DescribeModule(ctx context.Context, text string) (string, error) {
	_, engine, err := r.engineFor(config.TaskSummary)
	if err != nil {
		return "", err
	}
	out, err := engine.Complete(ctx, []llm.Message{
		{Role: "system", Content: "You explain one folder of a software project for a chapter of its manual, " +
			"read by someone new to the project, from its files, its exported API, what it uses, what uses " +
			"it and excerpts of its source. Write plain prose in Markdown without headings: start with one " +
			"sentence that says what the folder is for, then two to four short paragraphs on how it works, " +
			"how it fits with the folders it uses and that use it, and anything to watch out for. Name the " +
			"main files or types only when it helps. Use only what the text shows; say when something is " +
			"unknown. Do not invent files, names or behaviour."},
		{Role: "user", Content: text},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

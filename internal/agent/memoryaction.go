package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/knowledge"
	"github.com/madkoding/motita/internal/memory"
	"github.com/madkoding/motita/internal/redact"
)

// memoryAction answers the memory_* actions over the person's memory: search and read change
// nothing, write creates an entry and never replaces one, append adds to the end of one.
func (rr *roundRun) memoryAction(kind string, action Command) bool {
	var reply string
	switch kind {
	case "recall", "remember":
		reply = rr.a.knowledgeReply(rr.ctx, kind, action.Command)
	case "memory_search", "memory_read", "memory_write", "memory_append":
		reply = rr.a.memoryReply(kind, action.Command)
	default:
		return false
	}
	if line, ok := memory.SavedLine(reply); ok {
		rr.a.report("%s", line)
	}
	fmt.Fprintf(&rr.sb, "[%s] %s\n%s\n", kind, action.Description, reply)
	rr.a.log.Info(rr.prefix+"memory action", "kind", kind, "command", truncate(firstLineOf(action.Command), 120))
	return true
}

// memoryReply is the text the model gets back. A write or an append takes "entry :: the Markdown",
// the same shape as save_skill.
func (a *Agent) memoryReply(kind, command string) string {
	if a.memory == nil {
		return "Error: there is no memory in this session."
	}
	switch kind {
	case "memory_search":
		return truncateMiddle(a.memory.SearchText(command), actionOutputChars)
	case "memory_read":
		return truncateMiddle(a.memory.ReadText(command), actionOutputChars)
	default:
		// memory_write and memory_append carry a body: handled below
	}
	ref, body, ok := strings.Cut(command, "::")
	if !ok {
		return "Error: " + kind + " expects \"entry :: the Markdown\"."
	}
	if kind == "memory_write" {
		return a.memory.WriteText(strings.TrimSpace(ref), redact.String(strings.TrimSpace(body)))
	}
	return a.memory.AppendText(strings.TrimSpace(ref), redact.String(strings.TrimSpace(body)))
}

// knowledgeReply answers recall and remember, the two actions over everything the agent knows (see
// internal/knowledge). The command may open with "in:<place>"; a recall then takes the words to
// look for, or "open: <entry or procedure>"; a remember takes "entry :: the Markdown" (only the
// Markdown for a project lesson).
func (a *Agent) knowledgeReply(ctx context.Context, kind, command string) string {
	in, rest := placeOf(command)
	b := a.knowledgeBase(ctx)
	if kind == "recall" {
		if open, ok := cutPrefixFold(rest, "open:"); ok {
			return truncateMiddle(b.Recall(in, "", strings.TrimSpace(open)), actionOutputChars)
		}
		return truncateMiddle(b.Recall(in, rest, ""), actionOutputChars)
	}
	entry, body, ok := strings.Cut(rest, "::")
	if !ok {
		entry, body = "", rest
	}
	return b.Remember(in, entry, body)
}

// placeOf splits a leading "in:<place>" off a command.
func placeOf(command string) (string, string) {
	command = strings.TrimSpace(command)
	rest, ok := cutPrefixFold(command, "in:")
	if !ok {
		return "", command
	}
	place, after, _ := strings.Cut(strings.TrimSpace(rest), " ")
	return place, strings.TrimSpace(after)
}

// knowledgeBase is what this run can reach.
func (a *Agent) knowledgeBase(ctx context.Context) knowledge.Base {
	b := knowledge.Base{Memory: a.memory, Workspace: a.cfg.Agent.WorkspaceDir}
	if a.library != nil {
		b.Library = agentLibrary{a}
	}
	if a.recall != nil {
		b.Conversation = func(query string) string {
			block, _ := a.recall(ctx, query, 0)
			return block
		}
	}
	return b
}

// agentLibrary is the procedure library as this run reaches it, with its bookkeeping.
type agentLibrary struct{ a *Agent }

func (l agentLibrary) Search(query string) string { return l.a.searchSkills(query) }
func (l agentLibrary) Read(name string) string    { return l.a.readSkill(name) }
func (l agentLibrary) Save(name, body string) string {
	return l.a.saveSkill(name + " :: " + body)
}

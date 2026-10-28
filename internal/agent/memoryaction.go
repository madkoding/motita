package agent

import (
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/redact"
)

// memoryAction answers the memory_* actions over the person's memory: search and read change
// nothing, write creates an entry and never replaces one, append adds to the end of one.
func (rr *roundRun) memoryAction(kind string, action Command) bool {
	switch kind {
	case "memory_search", "memory_read", "memory_write", "memory_append":
	default:
		return false
	}
	fmt.Fprintf(&rr.sb, "[%s] %s\n%s\n", kind, action.Description, rr.a.memoryReply(kind, action.Command))
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
		return truncateMiddle(a.memory.SearchText(command), indexOutputChars)
	case "memory_read":
		return truncateMiddle(a.memory.ReadText(command), indexOutputChars)
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

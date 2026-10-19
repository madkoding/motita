package agent

import (
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/redact"
)

// noteAction answers the note_* actions over the person's note vault: search and read change
// nothing, write creates a note and never replaces one, append adds to the end of one.
func (rr *roundRun) noteAction(kind string, action Command) bool {
	switch kind {
	case "note_search", "note_read", "note_write", "note_append":
	default:
		return false
	}
	fmt.Fprintf(&rr.sb, "[%s] %s\n%s\n", kind, action.Description, rr.a.noteReply(kind, action.Command))
	rr.a.log.Info(rr.prefix+"note action", "kind", kind, "command", truncate(firstLineOf(action.Command), 120))
	return true
}

// noteReply is the text the model gets back. A write or an append takes "note :: the Markdown",
// the same shape as save_skill.
func (a *Agent) noteReply(kind, command string) string {
	if a.notes == nil {
		return "Error: there is no note vault in this session."
	}
	switch kind {
	case "note_search":
		return truncateMiddle(a.notes.SearchText(command), indexOutputChars)
	case "note_read":
		return truncateMiddle(a.notes.ReadText(command), indexOutputChars)
	}
	ref, body, ok := strings.Cut(command, "::")
	if !ok {
		return "Error: " + kind + " expects \"note :: the Markdown\"."
	}
	if kind == "note_write" {
		return a.notes.WriteText(strings.TrimSpace(ref), redact.String(strings.TrimSpace(body)))
	}
	return a.notes.AppendText(strings.TrimSpace(ref), redact.String(strings.TrimSpace(body)))
}

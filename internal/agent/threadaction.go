package agent

import (
	"fmt"
	"strings"
)

// threadAction is the thread action of a project's main session. A thread is where one KIND of work
// goes on in a worktree of its own, so that red boxes and white boxes are built in parallel without
// touching each other's files. The model decides whether a request belongs to a thread that exists
// or deserves a new one; what the action does is carried out by the gateway.
func (rr *roundRun) threadAction(kind string, action Command) bool {
	if kind != "thread" {
		return false
	}
	reply := "Error: this session cannot open threads."
	if rr.a.threads != nil {
		reply = rr.a.threads(action.Command)
	}
	fmt.Fprintf(&rr.sb, "[%s] %s\n%s\n", kind, action.Description, truncateMiddle(reply, actionOutputChars))
	rr.a.log.Info(rr.prefix+"thread action", "command", truncate(strings.TrimSpace(firstLineOf(action.Command)), 120))
	return true
}

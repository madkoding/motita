package gateway

import (
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/approvals"
)

// approvalsFile is the file, under the session directory, of the commands a person said to
// always allow in a project.
const approvalsFile = "approvals.json"

// openApprovalStore opens the remembered approvals beside the sessions. Without a session
// directory they live in memory and last as long as the gateway; a file that cannot be read is
// reported and replaced by an empty store, so one bad file never makes every command ask again
// forever without a word.
func (s *Server) openApprovalStore() {
	path := ""
	if dir := strings.TrimSpace(s.opts.SessionDir); dir != "" {
		path = filepath.Join(dir, approvalsFile)
	}
	st, err := approvals.Open(path)
	if err != nil {
		s.warn("could not read the remembered approvals; starting without them", "error", err.Error())
		st, _ = approvals.Open("")
	}
	s.approvals = st
}

// approvalScope is what an "always allow" covers: the project of the session, or, for a session
// outside any project, its own workspace.
func approvalScope(c *conversation) string {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.projectID != "" {
		return "project:" + c.projectID
	}
	return "workspace:" + c.workspace
}

// alwaysAllowed says whether the person already allowed this shape of command here.
func (s *Server) alwaysAllowed(c *conversation, rule, command string) bool {
	keys, ok := approvals.KeysOf(rule, command)
	return ok && s.approvals.AllowedAll(approvalScope(c), keys)
}

// headerAlwaysAllow tells a client what became of an "always allow": "kept" when this gateway keeps
// the shape of the command allowed, "none" when the line has no shape to keep. A gateway from before
// the choice existed sends no such header, which is how a page tells that the choice went nowhere.
const headerAlwaysAllow = "Motita-Always-Allow"

// rememberApproval keeps this shape of command allowed here from now on, and says what the client
// is told about it (the value of headerAlwaysAllow).
func (s *Server) rememberApproval(c *conversation, rule, command string) string {
	keys, ok := approvals.KeysOf(rule, command)
	if !ok {
		return "none"
	}
	if err := s.approvals.RememberAll(approvalScope(c), keys); err != nil {
		s.warn("could not write the remembered approvals", "error", err.Error())
	}
	return "kept"
}

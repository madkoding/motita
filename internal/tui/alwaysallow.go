package tui

import (
	"path/filepath"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/approvals"
	"github.com/madkoding/motita/internal/config"
)

// approvalsPath is where the terminal keeps the shapes of command the person said to always
// allow: beside the configuration, in the motita home. A variable so a test keeps its decisions
// in a directory of its own; "" (no home at all) keeps them for this process only.
var approvalsPath = func() string {
	if dir := config.Dir(); dir != "" {
		return filepath.Join(dir, "approvals.json")
	}
	return ""
}

// approvalStore opens the remembered decisions once. A file that cannot be read starts empty, so
// one bad file never makes every command ask again forever: the person says "always" once more
// and the file is written afresh.
func (t *TUI) approvalStore() *approvals.Store {
	t.rememberedOnce.Do(func() {
		st, err := approvals.Open(approvalsPath())
		if err != nil {
			st, _ = approvals.Open("")
		}
		t.remembered = st
	})
	return t.remembered
}

// approvalScope is what an "always allow" covers in the terminal: the folder the commands run in,
// which is the same scope the gateway gives a session outside any project on that folder.
func (t *TUI) approvalScope() string {
	dir := t.Runner.Config().Agent.WorkspaceDir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return "workspace:" + dir
}

// alwaysAllowed says whether the person already said "always" to this shape of command here.
func (t *TUI) alwaysAllowed(req agent.ApprovalRequest) bool {
	keys, ok := approvals.KeysOf(req.Rule, req.Command)
	return ok && t.approvalStore().AllowedAll(t.approvalScope(), keys)
}

// rememberApproval keeps this shape of command allowed here from now on, and says whether there
// was a shape to remember: a line with no program, or no rule, is approved once as a plain yes.
// A file that cannot be written still remembers for this process.
func (t *TUI) rememberApproval(req agent.ApprovalRequest) bool {
	keys, ok := approvals.KeysOf(req.Rule, req.Command)
	if !ok {
		return false
	}
	_ = t.approvalStore().RememberAll(t.approvalScope(), keys)
	return true
}

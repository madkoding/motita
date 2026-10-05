package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

// shellRunner is what a session's service offers to run a line a person typed. It is optional:
// a service that cannot (a client of another gateway) is answered with "not implemented" rather
// than every Service having to carry a method most never use.
type shellRunner interface {
	RunShell(ctx context.Context, command string, approver agent.Approver) (string, int, error)
}

// shellTimeout bounds one typed command, whatever the sandbox's own limit is.
const shellTimeout = 5 * time.Minute

// shellResult is the answer to a typed command.
type shellResult struct {
	Command string `json:"command"`
	Output  string `json:"output"`
	Exit    int    `json:"exit"`
	// Error is why the command did not run or did not succeed: refused by the policy, declined,
	// or failed to start. It is empty for a command that ran, whatever its exit code.
	Error string `json:"error,omitempty"`
	// NeedsApproval means the policy would ask before running this line. Nothing ran: the client
	// shows the question and, when the person says yes, sends the same line with approved set.
	NeedsApproval bool   `json:"needs_approval,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// handleShell runs a command line typed by the user, in the session's workspace, under the
// policy the agent's own commands obey: what the policy forbids stays forbidden, what it would
// ask about comes back as needs_approval, and approved=true (or "allow all commands for this
// session") is the person saying yes. It does not run while the agent is working: the two would
// be writing into the same workspace.
func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command  string `json:"command"`
		Approved bool   `json:"approved"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	command := strings.TrimSpace(body.Command)
	if command == "" {
		writeError(w, http.StatusBadRequest, "the command is empty")
		return
	}
	c := convOf(r)
	if c.merged {
		writeError(w, http.StatusConflict, "this session has already been integrated into the project; continue in a new session to keep working")
		return
	}
	sh, ok := c.svc.(shellRunner)
	if !ok {
		writeError(w, http.StatusNotImplemented, "this session cannot run shell commands")
		return
	}
	if c.isRunning() {
		writeError(w, http.StatusConflict, "the agent is working in this session: stop it or wait for it to finish before running a command")
		return
	}

	res := shellResult{Command: command}
	approver := func(_ context.Context, req agent.ApprovalRequest) (bool, error) {
		if body.Approved || c.autoApproving() {
			return true, nil
		}
		res.NeedsApproval = true
		res.Reason = req.Reason
		return false, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), shellTimeout)
	defer cancel()
	out, exit, err := sh.RunShell(ctx, command, approver)
	if res.NeedsApproval {
		writeJSON(w, http.StatusOK, res)
		return
	}
	res.Output, res.Exit = out, exit
	if err != nil {
		res.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, res)
}

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// shellProbe stands in for the policy: "ask ..." needs approval, "deny ..." is refused, anything
// else prints its own text.
type shellProbe struct {
	fakeService
}

func (p *shellProbe) RunShell(ctx context.Context, command string, approver agent.Approver) (string, int, error) {
	switch {
	case command == "deny":
		return "[refused: never]\n", 1, errors.New("command was refused: never")
	case command == "ask":
		ok, _ := approver(ctx, agent.ApprovalRequest{Command: command, Reason: "it writes"})
		if !ok {
			return "[not approved]\n", 1, errors.New("declined")
		}
		return "ran after approval\n", 0, nil
	}
	return "out of " + command + "\n", 0, nil
}

func runShell(t *testing.T, srv *Server, id, body string) (int, shellResult) {
	t.Helper()
	w := post(t, srv, "/v1/sessions/"+id+"/shell", body, testToken)
	var res shellResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	return w.Code, res
}

func TestShellRunsATypedCommandAndReportsIt(t *testing.T) {
	srv := newTestServer(t, &shellProbe{}, func(o *Options) {
		o.SessionDir = t.TempDir()
		o.NewService = func() (Service, error) { return &shellProbe{}, nil }
	})
	a := newSessionFor(t, srv)

	code, res := runShell(t, srv, a.ID, `{"command":"  ls  "}`)
	if code != http.StatusOK || res.Output != "out of ls\n" || res.Exit != 0 || res.Error != "" || res.Command != "ls" {
		t.Fatalf("a plain command: %d %+v", code, res)
	}

	if code, res = runShell(t, srv, a.ID, `{"command":"deny"}`); code != http.StatusOK || res.Error == "" || res.Exit != 1 {
		t.Errorf("a refused command must say so: %d %+v", code, res)
	}

	// The policy would ask: nothing runs and the client is told why.
	if code, res = runShell(t, srv, a.ID, `{"command":"ask"}`); !res.NeedsApproval || res.Reason != "it writes" || res.Output != "" || code != http.StatusOK {
		t.Errorf("a command that needs approval must not run: %d %+v", code, res)
	}
	// The person said yes.
	if _, res = runShell(t, srv, a.ID, `{"command":"ask","approved":true}`); res.NeedsApproval || res.Output != "ran after approval\n" {
		t.Errorf("an approved command must run: %+v", res)
	}
	// "Allow all commands for this session" answers the question too.
	srv.conversationOf(a.ID).setAutoApprove(true)
	if _, res = runShell(t, srv, a.ID, `{"command":"ask"}`); res.NeedsApproval || res.Output != "ran after approval\n" {
		t.Errorf("allow-all must approve: %+v", res)
	}
}

func TestShellRefusesWhatItCannotServe(t *testing.T) {
	srv := newTestServer(t, &shellProbe{}, func(o *Options) {
		o.SessionDir = t.TempDir()
		o.NewService = func() (Service, error) { return &shellProbe{}, nil }
	})
	a := newSessionFor(t, srv)
	if code, _ := runShell(t, srv, a.ID, `not json`); code != http.StatusBadRequest {
		t.Errorf("a broken body: %d", code)
	}
	if code, _ := runShell(t, srv, a.ID, `{"command":"   "}`); code != http.StatusBadRequest {
		t.Errorf("an empty command: %d", code)
	}
	c := srv.conversationOf(a.ID)
	c.stateMu.Lock()
	c.running = true
	c.stateMu.Unlock()
	if code, _ := runShell(t, srv, a.ID, `{"command":"ls"}`); code != http.StatusConflict {
		t.Errorf("while the agent works: %d", code)
	}
	c.stateMu.Lock()
	c.running = false
	c.stateMu.Unlock()
	c.merged = true
	if code, _ := runShell(t, srv, a.ID, `{"command":"ls"}`); code != http.StatusConflict {
		t.Errorf("an integrated session: %d", code)
	}
}

func TestShellIsNotImplementedForAServiceWithoutIt(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.SessionDir = t.TempDir()
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	a := newSessionFor(t, srv)
	if code, _ := runShell(t, srv, a.ID, `{"command":"ls"}`); code != http.StatusNotImplemented {
		t.Errorf("a service that cannot run shell commands: %d", code)
	}
}

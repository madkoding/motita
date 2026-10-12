package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/policy"
)

// --- Final action: every kind -----------------------------------------------

// TestFinalActionAPI: the HTTP request is made and a 2xx is a success.
func TestFinalActionAPI(t *testing.T) {
	var received map[string]any
	var method string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusCreated)
	}))
	defer target.Close()

	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{"plan":[]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"description":"notify","command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "api", URL: target.URL, Method: http.MethodPost}
	})

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Pass {
		t.Fatalf("it should pass: %s", result.Reason)
	}
	if !strings.Contains(result.FinalAction, "HTTP 201") {
		t.Errorf("final action = %q", result.FinalAction)
	}
	if method != http.MethodPost {
		t.Errorf("method = %q", method)
	}
	if received["status"] != "pass" {
		t.Errorf("body = %v", received)
	}
}

// TestFinalActionAPIStatusError: a 5xx in the final action is a failure.
func TestFinalActionAPIStatusError(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{"plan":[]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.MaxRetries = 0
		c.FinalAction = config.FinalAction{Kind: "api", URL: target.URL}
	})

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err == nil {
		t.Error("a 500 in the final action must fail the task")
	}
	if result.Pass {
		t.Error("PASS cannot be declared")
	}
	if !strings.Contains(result.Reason, "HTTP 500") {
		t.Errorf("reason = %q", result.Reason)
	}
}

// TestFinalActionAPIUnreachable: a network error in the final action.
func TestFinalActionAPIUnreachable(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{"plan":[]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.MaxRetries = 0
		c.FinalAction = config.FinalAction{Kind: "api", URL: "http://127.0.0.1:1/does-not-exist"}
	})

	if err := e.agent.Run(context.Background()); err == nil {
		t.Error("an unreachable target must fail the task")
	}
}

// TestUnknownFinalAction: an invalid final action kind is reported.
func TestUnknownFinalAction(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			// Assigned directly (config validation would reject it earlier).
			c.FinalAction = config.FinalAction{Kind: "telepathy"}
		},
	)

	description, err := e.agent.runFinalAction(context.Background(), Command{}, "")
	if err == nil {
		t.Errorf("an unknown final action kind must error (description: %q)", description)
	}
}

// TestFinalActionCommandWithoutCommand: there is nothing to run and it is not an
// error.
func TestFinalActionCommandWithoutCommand(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "command", Command: "", Args: nil}
		},
	)

	description, err := e.agent.runFinalAction(context.Background(), Command{Command: ""}, "")
	if err != nil {
		t.Errorf("with no command it must not be an error: %v", err)
	}
	if description != "none" {
		t.Errorf("description = %q", description)
	}
}

// TestFinalActionWithTheModelCommand: the LLM may propose the final command. A write inside the
// workspace is one the policy allows, so it runs without asking.
func TestFinalActionWithTheModelCommand(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "command", Command: "sh"}
		},
	)
	marker := filepath.Join(e.dir, "model.txt")

	description, err := e.agent.runFinalAction(context.Background(),
		Command{Kind: "command", Command: "echo done > " + marker}, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the model's command must run: %v", err)
	}
	if !strings.Contains(description, "exit=0") {
		t.Errorf("description = %q", description)
	}
}

// TestTheModelsFinalActionIsApprovedLikeAnyAction: a final command the MODEL wrote is not the
// operator's configuration, so a line the policy would ask about is asked about, and with
// nobody to ask (or a no) it does not run. The operator's own configured command keeps running.
func TestTheModelsFinalActionIsApprovedLikeAnyAction(t *testing.T) {
	var commands []string
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "command", Command: "sh"}
			c.Agent.Policy.Enforce = true
		},
	)
	e.agent.ExecCommand = fakeExecutor(&commands, nil)
	line := "git push --force origin main"
	if p := e.agent.planRequest(line); p.Verdict != policy.Ask {
		t.Fatalf("the test needs a line the policy asks about, got %s", p.Verdict)
	}

	// Nobody to ask: refused, nothing runs.
	if _, err := e.agent.runFinalAction(context.Background(), Command{Command: line}, ""); err == nil ||
		!strings.Contains(err.Error(), "nobody to ask") {
		t.Errorf("with no approver the model's final action must be refused: %v", err)
	}
	// A no: refused, nothing runs.
	var asked []string
	e.agent.SetApprover(func(_ context.Context, req ApprovalRequest) (bool, error) {
		asked = append(asked, req.Command)
		return len(asked) > 1, nil
	})
	if _, err := e.agent.runFinalAction(context.Background(), Command{Command: line}, ""); err == nil ||
		!strings.Contains(err.Error(), "not approved") {
		t.Errorf("a refused final action must not run: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("an unapproved final action ran: %v", commands)
	}
	// A yes: it runs.
	if _, err := e.agent.runFinalAction(context.Background(), Command{Command: line}, ""); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || asked[0] != line || len(commands) != 1 || !strings.Contains(commands[0], line) {
		t.Errorf("asked %v, ran %v", asked, commands)
	}

	// The operator's configured command is not asked about.
	e.agent.cfg.FinalAction = config.FinalAction{Kind: "command", Command: "git", Args: []string{"push", "--force"}}
	e.agent.SetApprover(nil)
	if _, err := e.agent.runFinalAction(context.Background(), Command{}, ""); err != nil {
		t.Errorf("the operator's final action must keep running without a prompt: %v", err)
	}
}

// fakeExecutor replaces the agent's command execution: it records every command
// and returns the given exit code. It makes it possible to test the final
// action's contract (which commands run and how they are interpreted) without
// depending on git existing, on the repository's configuration or on its editor:
// with the real git this test used to hang waiting for input and took minutes.
func fakeExecutor(log *[]string, codes map[string]int) func(context.Context, execx.Request) (string, bool, int, error) {
	return func(_ context.Context, p execx.Request) (string, bool, int, error) {
		line := p.Command + " " + strings.Join(p.Args, " ")
		*log = append(*log, line)
		for fragment, code := range codes {
			if strings.Contains(line, fragment) {
				return "simulated output", false, code, nil
			}
		}
		return "", false, 0, nil
	}
}

// TestFinalActionGitCommitRunsAddAndCommit checks exactly which commands are
// launched and that the commit message carries the substituted task.
func TestFinalActionGitCommitRunsAddAndCommit(t *testing.T) {
	var commands []string
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "chore(agent): {{task}}"}
		},
	)
	e.agent.ExecCommand = fakeExecutor(&commands, nil)

	description, err := e.agent.runFinalAction(context.Background(), Command{Description: "fix the parser"}, "")
	if err != nil {
		t.Fatalf("git_commit failed: %v (%s)", err, description)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %v, expected 2 (add and commit)", commands)
	}
	if !strings.Contains(commands[0], "git add -A") {
		t.Errorf("first = %q", commands[0])
	}
	if !strings.Contains(commands[1], "git commit -m") || !strings.Contains(commands[1], "fix the parser") {
		t.Errorf("the commit must carry the substituted task: %q", commands[1])
	}
	if !strings.Contains(description, "git_commit") {
		t.Errorf("description = %q", description)
	}
}

// TestFinalActionGitCommitWithFailure: if one step returns an error, the task can
// NOT be taken as completed and the sequence stops.
func TestFinalActionGitCommitWithFailure(t *testing.T) {
	var commands []string
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "x"}
		},
	)
	e.agent.ExecCommand = fakeExecutor(&commands, map[string]int{"git add": 128})

	_, err := e.agent.runFinalAction(context.Background(), Command{}, "")
	if err == nil {
		t.Fatal("a failed git add must return an error")
	}
	if !strings.Contains(err.Error(), "128") {
		t.Errorf("the error must mention the real code: %v", err)
	}
	if len(commands) != 1 {
		t.Errorf("it must not continue after the failure: %v", commands)
	}
}

// TestFinalActionWithoutExecutor: with no executor configured it reports instead
// of panicking.
func TestFinalActionWithoutExecutor(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "command", Command: "true"}
		},
	)
	e.agent.ExecCommand = nil

	if _, err := e.agent.runFinalAction(context.Background(), Command{}, ""); err == nil {
		t.Error("with no executor it must return an error")
	}
	if _, err := e.agent.runActions(context.Background(), []Command{{Command: "true"}}, ""); err == nil {
		t.Error("with no executor, runActions must report too")
	}
}

// TestEmptyFinalAction: kind none does nothing and is not an error.
func TestEmptyFinalAction(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: ""}
		},
	)
	description, err := e.agent.runFinalAction(context.Background(), Command{}, "")
	if err != nil || description != "none" {
		t.Errorf("description=%q err=%v", description, err)
	}
}

// --- Escalation -------------------------------------------------------------

// TestEscalateWithoutCommandDoesNothing.
func TestEscalateWithoutCommandDoesNothing(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.Agent.OnFailure = config.OnFailure{Kind: "command", Command: "  "}
		},
	)
	// It must not panic nor run anything.
	e.agent.escalate(context.Background(), "")
}

// TestEscalateFailedCommand: an escalation that cannot run does not break.
func TestEscalateFailedCommand(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.Agent.OnFailure = config.OnFailure{Kind: "command", Command: "this-command-does-not-exist-ever"}
		},
	)
	e.agent.escalate(context.Background(), "")
}

// TestEscalateRunsTheCommand: the escalation leaves a trace on the system.
func TestEscalateRunsTheCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "escalated.txt")
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.Agent.OnFailure = config.OnFailure{Kind: "command", Command: "echo escalated > " + marker}
		},
	)

	e.agent.escalate(context.Background(), "")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the escalation must run: %v", err)
	}
}

// TestRunFinalActionWithFailedCommand: a failing final command returns the
// description AND the error, so the reason is visible.
func TestRunFinalActionWithFailedCommand(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "command", Command: "sh", Args: []string{"-c", "exit 5"}}
		},
	)

	description, err := e.agent.runFinalAction(context.Background(), Command{}, "")
	if err == nil {
		t.Fatal("a non-zero code is a failure")
	}
	if !strings.Contains(description, "exit=5") {
		t.Errorf("the description must carry the real code: %q", description)
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("the error must mention the code: %v", err)
	}
}

// TestFinalActionCommandWithNothingToRun: final_action.kind=command with no
// configured command and nothing proposed by the model does nothing.
func TestFinalActionCommandWithNothingToRun(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "command", Command: ""}
		},
	)

	description, err := e.agent.runFinalAction(context.Background(), Command{Command: "   "}, "")
	if err != nil {
		t.Errorf("with no command it must not be an error: %v", err)
	}
	if description != "none" {
		t.Errorf("description = %q", description)
	}
}

// TestFinalActionWithAnInvalidMethod: a final action (API) with a method that
// http.NewRequest rejects must be reported as an invalid request.
func TestFinalActionWithAnInvalidMethod(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			// A control character in the method makes the request invalid.
			c.FinalAction = config.FinalAction{Kind: "api", URL: "http://127.0.0.1:1/done", Method: "BAD\nMETHOD"}
		},
	)

	description, err := e.agent.runFinalAction(context.Background(), Command{}, "")
	if err == nil {
		t.Error("an invalid method must be an error")
	}
	if !strings.Contains(description, "error") {
		t.Errorf("description = %q", description)
	}
}

// TestGitCommitWithNoMessageFallsBackToADefault: an empty commit message must not
// produce `git commit -m ”`.
func TestGitCommitWithNoMessageFallsBackToADefault(t *testing.T) {
	var commands []string
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "   "}
		},
	)
	e.agent.ExecCommand = fakeExecutor(&commands, nil)

	if _, err := e.agent.runFinalAction(context.Background(), Command{}, ""); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %v", commands)
	}
	if !strings.Contains(commands[1], "chore(agent): validated changes") {
		t.Errorf("the default message must be used: %q", commands[1])
	}
}

// TestGitCommitStopsOnANonZeroExitWithoutAnError: a step that exits non-zero (but
// reports no execution error) must stop the sequence.
func TestGitCommitStopsOnANonZeroExitWithoutAnError(t *testing.T) {
	var commands []string
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "x"}
		},
	)
	// exit 1 with no error: the executor reports success but a non-zero code.
	e.agent.ExecCommand = func(_ context.Context, p execx.Request) (string, bool, int, error) {
		line := p.Command + " " + strings.Join(p.Args, " ")
		commands = append(commands, line)
		return "", false, 1, nil
	}

	description, err := e.agent.runFinalAction(context.Background(), Command{}, "")
	if err == nil {
		t.Fatal("a non-zero exit must be a failure")
	}
	if !strings.Contains(description, "exit=1") {
		t.Errorf("description = %q", description)
	}
	if len(commands) != 1 {
		t.Errorf("it must stop after the failure: %v", commands)
	}
}

// TestGitCommitReportsAnExecutionError: when the executor itself fails, the error
// must be reported with the accumulated output.
func TestGitCommitReportsAnExecutionError(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) {
			c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "x"}
		},
	)
	calls := 0
	e.agent.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		calls++
		return "simulated output", false, 0, fmt.Errorf("the executor refused")
	}

	_, err := e.agent.runFinalAction(context.Background(), Command{}, "")
	if err == nil {
		t.Fatal("an executor failure must be reported")
	}
	if !strings.Contains(err.Error(), "git_commit") {
		t.Errorf("the error must name the step: %v", err)
	}
	if calls != 1 {
		t.Errorf("it must stop after the first failure: %d calls", calls)
	}
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/anchor"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/task"
)

// execxRequest builds a shell request for the test sandbox.
func execxRequest(command string) execx.Request {
	return execx.Request{Command: "/bin/sh", Args: []string{"-c", command}, Timeout: 20 * time.Second}
}

// textTask wraps a text as a task (for the test sources).
func textTask(text string) task.Task {
	return task.Task{Description: text, Origin: "test"}
}

// phaseServer answers the three phases with JSON controlled by the test.
type phaseServer struct {
	analysis      string
	plan          string
	action        string
	analysisError bool
	// analysisUnclear makes the analysis report that it could not read the request, with a
	// question and an assumption — the shape the agent must turn into a question rather than a
	// refusal.
	analysisUnclear bool
	// analysisUnclearNoAssumption is the same but with nothing to fall back on, so there is no
	// reading to proceed with.
	analysisUnclearNoAssumption bool
	// analysisUnclearNoSummary is the same as analysisUnclear but with the summary left empty:
	// there is an assumption to act on, and nothing else to describe the work.
	analysisUnclearNoSummary bool
	// analysisAssumptionOnly proposes a reading but names no question.
	analysisAssumptionOnly bool
	planError              bool
	actionError            bool
	httpError              bool
	calls                  *int
}

func (s phaseServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.calls != nil {
			*s.calls++
		}
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}

		if s.httpError {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"simulated failure"}}`)
			return
		}

		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			switch {
			case s.analysisError:
				content = "this is not JSON"
			case s.analysisUnclear:
				content = `{"understandable": false, "summary": "", "success_criteria": [],
					"risks": ["two readings are possible"], "needs_subtasks": false,
					"question": "¿Quieres que revise el disco o los logs?",
					"assumption": "asumo que quieres el estado del disco"}`
			case s.analysisAssumptionOnly:
				content = `{"understandable": false, "summary": "", "success_criteria": [],
					"risks": ["ambiguo"], "needs_subtasks": false,
					"question": "", "assumption": "asumo que quieres revisar el disco"}`
			case s.analysisUnclearNoSummary:
				content = `{"understandable": false, "summary": "", "success_criteria": [],
					"risks": ["ambiguo"], "needs_subtasks": false,
					"question": "¿qué quieres?", "assumption": "asumo el estado del disco"}`
			case s.analysisUnclearNoAssumption:
				content = `{"understandable": false, "summary": "", "success_criteria": [],
					"risks": ["nothing to go on"], "needs_subtasks": false,
					"question": "¿Qué quieres que haga?", "assumption": ""}`
			default:
				content = s.analysis
			}
		case strings.Contains(text, "## ACTION PLAN"):
			if s.planError {
				content = "not this either"
			} else {
				content = s.plan
			}
		default:
			if s.actionError {
				content = "nor this"
			} else {
				content = s.action
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}
}

// TestAnalysisPhaseWithInvalidResponse: an analysis that is not valid JSON is OUR problem, not
// the user's, so the agent asks a question instead of reporting a parse error at them.
//
// The parse error goes to the LOG — it is what a developer needs — while the user gets a question
// they can answer. Telling a user "the LLM's analysis was not valid JSON" asks them to fix
// something they did not do.
func TestAnalysisPhaseWithInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(phaseServer{analysisError: true}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	// A malformed analysis produces a QUESTION, so the run does not fail: it is waiting for the
	// user, which is a different outcome and must not exit non-zero.
	_ = e.agent.Run(context.Background())
	if result == nil {
		t.Fatal("no result was reported")
	}
	// It asks rather than reporting a parse failure, and it is not a failure at all.
	if !result.NeedsInput {
		t.Errorf("an unparseable analysis must become a question, got %+v", result)
	}
	if result.Question == "" {
		t.Error("the user must be given something to answer")
	}
	if strings.Contains(result.Question, "JSON") {
		t.Errorf("the question must not talk about JSON: %q", result.Question)
	}
}

// TestPlanPhaseWithInvalidResponse: an unreadable plan does not abort: it carries
// on with an empty plan (the plan is guidance, not binding).
func TestPlanPhaseWithInvalidResponse(t *testing.T) {
	server := phaseServer{
		analysis:  `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		planError: true,
		action:    `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("an unreadable plan must not stop the work: %v", err)
	}
	if result == nil || !result.Pass {
		t.Errorf("result = %+v", result)
	}
}

// TestActionPhaseWithInvalidResponse: with no action there is nothing to run; the
// task fails with the reason.
func TestActionPhaseWithInvalidResponse(t *testing.T) {
	server := phaseServer{
		analysis:    `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:        `{"plan":[]}`,
		actionError: true,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err == nil {
		t.Error("it should fail")
	}
	if result.Pass {
		t.Error("it cannot pass without an action")
	}
	if !strings.Contains(result.Reason, "action") {
		t.Errorf("reason = %q", result.Reason)
	}
}

// TestActionPhaseWithoutActions: the JSON is valid but carries no actions.
func TestActionPhaseWithoutActions(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{"plan":[]}`,
		action:   `{"reasoning":"I do nothing","actions":[]}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	e.agent.Run(context.Background())
	if result.Pass {
		t.Error("with no actions there is nothing to validate")
	}
}

// TestNetworkFailureInAnalysis: an LLM failure in the first phase is recorded.
func TestNetworkFailureInAnalysis(t *testing.T) {
	srv := httptest.NewServer(phaseServer{httpError: true}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	e.agent.Run(context.Background())
	if result == nil || result.Pass {
		t.Fatalf("result = %+v", result)
	}
}

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
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		// Assigned directly (config validation would reject it earlier).
		c.FinalAction = config.FinalAction{Kind: "telepathy"}
	})

	description, err := e.agent.runFinalAction(context.Background(), Command{}, "")
	if err == nil {
		t.Errorf("an unknown final action kind must error (description: %q)", description)
	}
}

// TestFinalActionCommandWithoutCommand: there is nothing to run and it is not an
// error.
func TestFinalActionCommandWithoutCommand(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "command", Command: "", Args: nil}
	})

	description, err := e.agent.runFinalAction(context.Background(), Command{Command: ""}, "")
	if err != nil {
		t.Errorf("with no command it must not be an error: %v", err)
	}
	if description != "none" {
		t.Errorf("description = %q", description)
	}
}

// TestFinalActionWithTheModelCommand: the LLM may propose the final command.
func TestFinalActionWithTheModelCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "model.txt")
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "command", Command: "sh"}
	})

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
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "agent: {{task}}"}
	})
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
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "x"}
	})
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
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "command", Command: "true"}
	})
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
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: ""}
	})
	description, err := e.agent.runFinalAction(context.Background(), Command{}, "")
	if err != nil || description != "none" {
		t.Errorf("description=%q err=%v", description, err)
	}
}

// --- Escalation -------------------------------------------------------------

// TestEscalateWithoutCommandDoesNothing.
func TestEscalateWithoutCommandDoesNothing(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.OnFailure = config.OnFailure{Kind: "command", Command: "  "}
	})
	// It must not panic nor run anything.
	e.agent.escalate(context.Background(), "")
}

// TestEscalateFailedCommand: an escalation that cannot run does not break.
func TestEscalateFailedCommand(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.OnFailure = config.OnFailure{Kind: "command", Command: "this-command-does-not-exist-ever"}
	})
	e.agent.escalate(context.Background(), "")
}

// TestEscalateRunsTheCommand: the escalation leaves a trace on the system.
func TestEscalateRunsTheCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "escalated.txt")
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.OnFailure = config.OnFailure{Kind: "command", Command: "echo escalated > " + marker}
	})

	e.agent.escalate(context.Background(), "")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the escalation must run: %v", err)
	}
}

// --- Misc: shellQuote, truncate, describeRules ------------------------------

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"simple":       "'simple'",
		"with space":   "'with space'",
		"with 'quote'": `'with '\''quote'\'''`,
		"":             "''",
	}
	for input, expected := range cases {
		if got := shellQuote(input); got != expected {
			t.Errorf("shellQuote(%q) = %q, expected %q", input, got, expected)
		}
	}
}

func TestTruncateText(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate = %q", got)
	}
	got := truncate("this text is longer than the limit", 10)
	if len(got) > 15 || !strings.HasSuffix(got, "...") {
		t.Errorf("truncate = %q", got)
	}
}

// TestDescribeRulesWithEveryField: the LLM must know exactly what it is measured
// against, including the extra checks.
func TestDescribeRulesWithEveryField(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{
		Kind:         "command",
		Command:      "make",
		Args:         []string{"test"},
		ExpectExit:   0,
		ExpectOutput: "OK$",
		Checks: []config.Check{
			{Name: "lint", Command: "go", Args: []string{"vet", "./..."}, ExpectExit: 0},
		},
	}, nil)

	text := e.agent.describeRules()
	for _, part := range []string{"make test", "exit code 0", "OK$", "go vet ./..."} {
		if !strings.Contains(text, part) {
			t.Errorf("the rules must include %q: %s", part, text)
		}
	}
}

// TestDescribeRulesWithoutAnchor: with no anchor it explains there will be no
// PASS.
func TestDescribeRulesWithoutAnchor(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "none"}, nil)
	text := e.agent.describeRules()
	if !strings.Contains(text, "anchor.kind=none") {
		t.Errorf("text = %q", text)
	}
}

// TestBaseVariablesWithContext: the task's context is exposed as variables.
func TestBaseVariablesWithContext(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	vars := e.agent.baseVariables(task.Task{
		Description: "do something",
		Origin:      "test",
		Context:     map[string]string{"zone": "south", "shift": "night"},
	})
	if vars["task"] != "do something" || vars["origin"] != "test" {
		t.Errorf("vars = %v", vars)
	}
	if vars["context_zone"] != "south" || vars["context_shift"] != "night" {
		t.Errorf("the context was not exposed: %v", vars)
	}
}

// TestDescribeEmptyPlan.
func TestDescribeEmptyPlan(t *testing.T) {
	if got := describePlan(Plan{}); !strings.Contains(got, "no plan") {
		t.Errorf("describePlan = %q", got)
	}
}

// TestDescribeFullPlan.
func TestDescribeFullPlan(t *testing.T) {
	text := describePlan(Plan{
		Steps: []Step{
			{Number: 1, Action: "first", Command: "ls"},
			{Number: 2, Action: "second"},
		},
		ExpectedResult: "everything ready",
	})
	for _, part := range []string{"1. first", "ls", "2. second", "everything ready"} {
		if !strings.Contains(text, part) {
			t.Errorf("missing %q in %q", part, text)
		}
	}
}

// TestTemplateWithUnfilledVariable: it warns but the work continues.
func TestTemplateWithUnfilledVariable(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{"plan":[]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		// The marker identifying the phase is kept for the simulated server.
		c.Prompts.Analyze.User = "## ANALYSIS OF THE TASK: {{task}} {{variable_that_does_not_exist}}"
	})

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("a variable with no value must not abort: %v", err)
	}
}

// TestRunActionsWithEmptyCommand: a descriptive action runs nothing.
func TestRunActionsWithEmptyCommand(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	output, err := e.agent.runActions(context.Background(), []Command{
		{Kind: "command", Description: "just describing", Command: "   "},
	}, "")
	if err != nil {
		t.Errorf("an action with no command must not fail: %v", err)
	}
	if output != "" {
		t.Errorf("output = %q", output)
	}
}

// TestRunActionsWithFailure: a command that cannot run is reported.
func TestRunActionsWithFailure(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	output, err := e.agent.runActions(context.Background(), []Command{
		{Command: "this-command-never-exists"},
	}, "")
	if err == nil {
		t.Error("a non-existent command must report an error")
	}
	if !strings.Contains(output, "exit=") {
		t.Errorf("the output must record the attempt: %q", output)
	}
}

// TestAnUnknownActionKindIsRefusedRatherThanExecuted is the safety property of the dispatch.
//
// A model that names an operation this mode does not have — plan mode's `read_file` is the one
// the shipped skill names, and the skill is served to both modes — arrives with its ARGUMENT in
// the command field. Falling through to the command path runs that text as a program, so a path
// becomes an executable. The kind is refused by name instead.
//
// Both halves are asserted: the argument is not executed, and the model is told which kinds do
// exist, because a refusal it cannot act on just spends the turn.
func TestAnUnknownActionKindIsRefusedRatherThanExecuted(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	// A program that leaves a mark if it is executed, named like the tool the skill advertises.
	dir := t.TempDir()
	marker := filepath.Join(dir, "EXECUTED")
	script := filepath.Join(dir, "read_file")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	output, err := e.agent.runActions(context.Background(), []Command{
		{Kind: "read_file", Description: "read the file", Command: script},
	}, "")
	if err == nil {
		t.Error("an unknown kind must be reported as an error")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the unknown kind's argument was EXECUTED as a shell command")
	}
	if !strings.Contains(output, "not an action this mode has") {
		t.Errorf("the refusal must name the problem: %q", output)
	}
	// The model has to be able to recover from it, so the message lists what it may use.
	for _, kind := range []string{"command", "read_skill", "save_skill"} {
		if !strings.Contains(output, kind) {
			t.Errorf("the refusal must name %q among the kinds that exist: %q", kind, output)
		}
	}
}

// TestAnUnknownKindWithNothingToRunIsIgnored: the refusal is for a kind that carries text the
// model meant as arguments. A kind with no payload at all has nothing to refuse — it is the
// descriptive step the command path already ignores, and inventing an error for it would fail a
// turn over nothing.
func TestAnUnknownKindWithNothingToRunIsIgnored(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	output, err := e.agent.runActions(context.Background(), []Command{{Kind: "teleport"}}, "")
	if err != nil {
		t.Errorf("a kind with nothing to run must not fail the turn: %v", err)
	}
	if output != "" {
		t.Errorf("output = %q, want nothing to have happened", output)
	}
}

// TestSummariseFailureWithError: the summary includes the execution error.
func TestSummariseFailureWithError(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	text := e.agent.summariseFailure(
		Action{Actions: []Command{{Command: "ls"}}},
		"attempt output",
		anchor.Result{Pass: false, Reason: "failed"},
		fmt.Errorf("simulated error"),
	)
	for _, part := range []string{"ls", "attempt output", "simulated error", "validation"} {
		if !strings.Contains(text, part) {
			t.Errorf("missing %q in %q", part, text)
		}
	}
}

// TestNewAgentWithoutLogger: the agent is built with minimal dependencies.
func TestNewAgentWithoutLogger(t *testing.T) {
	a := New(config.Default(), nil, nil, nil, nil)
	if a == nil {
		t.Fatal("the agent must not be nil")
	}
	if a.log == nil {
		t.Error("it must use the global logger when none is passed in")
	}
}

// TestSourceThatAlwaysFailsGivesUp: real bug found while measuring coverage.
//
// A source that fails forever (a directory that disappeared, an unreachable API)
// used to spin the loop at full speed, burning CPU and log entries and never
// returning: the test hung for minutes until the package was killed. The agent
// now gives up after a few consecutive failures and returns an error, which is
// what cron or systemd need in order to notice.
func TestSourceThatAlwaysFailsGivesUp(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	source := &failingSource{}
	e.agent.source = source

	start := time.Now()
	err := e.agent.Run(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a source that always fails must give up with an error")
	}
	if !strings.Contains(err.Error(), "in a row") {
		t.Errorf("the error must explain that it gives up: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("it gave up too late (%s): runaway loop?", elapsed)
	}
	// It must try a bounded number of times, not loop forever.
	if source.calls != 5 {
		t.Errorf("source calls = %d, expected 5 (maxConsecutive)", source.calls)
	}
}

// TestSourceThatFailsAndRecovers: a transient failure must not give up.
func TestSourceThatFailsAndRecovers(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{"plan":[]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.source = &recoveringSource{initialFailures: 2}

	processed := 0
	e.agent.Observer = func(TaskResult) { processed++ }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("a transient failure must not give up: %v", err)
	}
	if processed != 1 {
		t.Errorf("tasks processed = %d, expected 1", processed)
	}
}

type failingSource struct {
	calls int
}

func (f *failingSource) Next(context.Context) (task.Task, error) {
	f.calls++
	return task.Task{}, fmt.Errorf("the source is not available")
}
func (f *failingSource) Close() error     { return nil }
func (f *failingSource) Describe() string { return "with-error" }

type recoveringSource struct {
	initialFailures int
	calls           int
}

func (f *recoveringSource) Next(context.Context) (task.Task, error) {
	f.calls++
	if f.calls <= f.initialFailures {
		return task.Task{}, fmt.Errorf("transient failure %d", f.calls)
	}
	if f.calls > f.initialFailures+1 {
		return task.Task{}, io.EOF
	}
	return textTask("task after the failure"), nil
}
func (f *recoveringSource) Close() error     { return nil }
func (f *recoveringSource) Describe() string { return "recovers" }

// TestRunIgnoresEmptyTasks: a blank description is not processed.
func TestRunIgnoresEmptyTasks(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{"plan":[]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.source = &emptySource{}

	if err := e.agent.Run(context.Background()); err != nil {
		t.Errorf("there must be no tasks to fail: %v", err)
	}
}

type emptySource struct{ delivered bool }

func (f *emptySource) Next(context.Context) (task.Task, error) {
	if f.delivered {
		return task.Task{}, io.EOF
	}
	f.delivered = true
	return task.Task{Description: "   "}, nil
}
func (f *emptySource) Close() error     { return nil }
func (f *emptySource) Describe() string { return "empty" }

// TestRunProcessesSeveralTasks: with no limit it processes every available task.
func TestRunProcessesSeveralTasks(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{"plan":[]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.source = &listSource{tasks: []string{"first", "second"}}

	processed := 0
	e.agent.Observer = func(TaskResult) { processed++ }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if processed != 2 {
		t.Errorf("tasks processed = %d, expected 2", processed)
	}
}

type listSource struct {
	tasks []string
	index int
}

func (f *listSource) Next(context.Context) (task.Task, error) {
	if f.index >= len(f.tasks) {
		return task.Task{}, io.EOF
	}
	t := textTask(f.tasks[f.index])
	f.index++
	return t, nil
}
func (f *listSource) Close() error     { return nil }
func (f *listSource) Describe() string { return "list" }

// TestPlanPhaseWithNoPlan: the plan phase's error path leaves an empty plan and
// the flow continues (the plan is guidance).
func TestPlanPhaseWithNoPlan(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Pass {
		t.Errorf("an empty plan must not stop the work: %s", result.Reason)
	}
}

// TestSplitReachesTheLimit: when the analysis keeps asking for subtasks but the
// configured depth is exhausted, the task continues as a single one instead of
// recursing forever.
func TestSplitReachesTheLimit(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":true}`,
		plan:     `{"plan":[],"subtasks":["another subtask"]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		// Depth 0: the very first split is refused and the task runs as one.
		c.Agent.SubtaskDepth = 0
	})

	start := time.Now()
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("it took too long: runaway recursion?")
	}
}

// TestRunActionsWithTruncatedOutput: the sandbox signals truncation and the agent
// must report it, because the model reads that output.
func TestRunActionsWithTruncatedOutput(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	e.agent.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		return "partial output", true, 0, nil
	}

	output, err := e.agent.runActions(context.Background(), []Command{{Command: "generate-a-lot"}}, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(output, "truncated") {
		t.Errorf("the output must warn about the truncation: %q", output)
	}
}

// TestCancellationDuringSubtasks: cancelling while subtasks run stops the work
// instead of finishing the whole list.
func TestCancellationDuringSubtasks(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":true}`,
		plan:     `{"plan":[],"subtasks":["a","b","c"]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.SubtaskDepth = 1
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := 0
	e.agent.Observer = func(TaskResult) {
		first++
		if first >= 1 {
			cancel()
		}
	}

	start := time.Now()
	e.agent.Run(ctx)
	if time.Since(start) > 15*time.Second {
		t.Errorf("the cancellation did not stop the work: %s", time.Since(start))
	}
}

// TestRunFinalActionWithFailedCommand: a failing final command returns the
// description AND the error, so the reason is visible.
func TestRunFinalActionWithFailedCommand(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "command", Command: "sh", Args: []string{"-c", "exit 5"}}
	})

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
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "command", Command: ""}
	})

	description, err := e.agent.runFinalAction(context.Background(), Command{Command: "   "}, "")
	if err != nil {
		t.Errorf("with no command it must not be an error: %v", err)
	}
	if description != "none" {
		t.Errorf("description = %q", description)
	}
}

// --- Test helpers ------------------------------------------------------------

var _ = logx.Global
var _ = execxRequest

// --- Remaining branches -----------------------------------------------------

// TestRunStopsWhenTheContextIsCancelledDuringASourceFailure: a cancellation that
// arrives at the same moment as a source error must end the run cleanly, not as
// a failure.
func TestRunStopsWhenTheContextIsCancelledDuringASourceFailure(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	// The source fails only after the context has been cancelled.
	e.agent.source = &failingSourceWithCancel{cancel: cancel}
	e.agent.cfg.Agent.MaxRetries = 0

	if err := e.agent.Run(ctx); err != nil {
		t.Errorf("a cancellation must end the run cleanly, got %v", err)
	}
}

type failingSourceWithCancel struct {
	cancel context.CancelFunc
	done   bool
}

func (f *failingSourceWithCancel) Next(context.Context) (task.Task, error) {
	if !f.done {
		f.done = true
		f.cancel() // cancels while the source is already failing
		return task.Task{}, fmt.Errorf("the source broke at the same time")
	}
	return task.Task{}, io.EOF
}
func (f *failingSourceWithCancel) Close() error     { return nil }
func (f *failingSourceWithCancel) Describe() string { return "cancels-while-failing" }

// TestSubtaskCancelledMidway: cancelling while a subtask is running must stop the
// parent task with an explicit reason instead of continuing down the list.
func TestSubtaskCancelledMidway(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":true}`,
		plan:     `{"plan":[],"subtasks":["a","b","c","d"]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.SubtaskDepth = 1
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := 0
	e.agent.Observer = func(r TaskResult) {
		seen++
		if seen >= 1 {
			cancel()
		}
	}

	if err := e.agent.Run(ctx); err != nil {
		t.Fatalf("a cancellation must not be reported as a failure: %v", err)
	}
	if seen < 1 {
		t.Error("at least one subtask must have been processed")
	}
}

// TestPlanPhaseWithNoAnalysisDetails: an analysis carrying neither summary nor
// criteria nor risks must still produce a usable plan request.
func TestPlanPhaseWithNoAnalysisDetails(t *testing.T) {
	server := phaseServer{
		// No summary, no success_criteria, no risks.
		analysis: `{"understandable":true,"needs_subtasks":false}`,
		plan:     `{"plan":[{"step":1,"action":"do it","command":"true"}]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !result.Pass {
		t.Errorf("a bare analysis must not stop the work: %s", result.Reason)
	}
}

// TestActionPhaseReportsATransportFailure: when the LLM cannot be reached while
// asking for the action, the task must fail with that reason (and escalate).
func TestActionPhaseReportsATransportFailure(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		if strings.Contains(text, "## ACTION") {
			// The analysis and the plan answer normally; the execute phase fails
			// every time, so the retries end up reporting the phase.
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"engine down"}}`)
			return
		}
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"x","needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[]}`
		case strings.Contains(text, "## ACTION"):
			content = `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`
		default:
			content = "{}"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.LLM.MaxAttempts = 1
	})
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	err := e.agent.Run(context.Background())
	if err == nil {
		t.Fatal("a failing engine must make the task fail")
	}
	if result == nil || result.Pass {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Reason, "could not obtain the action") {
		t.Errorf("the reason must name the failing phase: %q", result.Reason)
	}
}

// TestPlanPhaseReportsATransportFailure: a failure while planning leaves an empty
// plan and the flow continues (the plan is guidance, not a contract).
func TestPlanPhaseReportsATransportFailure(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		if strings.Contains(text, "## ACTION PLAN") {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"planner down"}}`)
			return
		}
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"x","needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[]}`
		default:
			content = `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.LLM.MaxAttempts = 1
	})
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("a failed plan must not abort the task: %v", err)
	}
	if !result.Pass {
		t.Errorf("the task should still pass: %s", result.Reason)
	}
}

// TestFinalActionWithAnInvalidMethod: a final action (API) with a method that
// http.NewRequest rejects must be reported as an invalid request.
func TestFinalActionWithAnInvalidMethod(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		// A control character in the method makes the request invalid.
		c.FinalAction = config.FinalAction{Kind: "api", URL: "http://127.0.0.1:1/done", Method: "BAD\nMETHOD"}
	})

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
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "   "}
	})
	e.agent.ExecCommand = fakeExecutor(&commands, nil)

	if _, err := e.agent.runFinalAction(context.Background(), Command{}, ""); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %v", commands)
	}
	if !strings.Contains(commands[1], "agent: validated changes") {
		t.Errorf("the default message must be used: %q", commands[1])
	}
}

// TestGitCommitStopsOnANonZeroExitWithoutAnError: a step that exits non-zero (but
// reports no execution error) must stop the sequence.
func TestGitCommitStopsOnANonZeroExitWithoutAnError(t *testing.T) {
	var commands []string
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "x"}
	})
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

// TestSubtaskListWithBlankEntries: blank entries in the subtask list are skipped
// and do not count as subtasks (they would otherwise be reported as failures).
func TestSubtaskListWithBlankEntries(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":true}`,
		plan:     `{"plan":[],"subtasks":["   ","","real subtask"]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.SubtaskDepth = 1
	})
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result.Subtasks != 1 {
		t.Errorf("subtasks = %d, the blank ones must be skipped", result.Subtasks)
	}
	if !result.Pass {
		t.Errorf("it should pass: %s", result.Reason)
	}
}

// TestSubtaskThatFailsIsReportedInTheReason: when one of several subtasks fails,
// the reason must say how many passed (that is what a person needs to know).
func TestSubtaskThatFailsIsReportedInTheReason(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":true}`,
		plan:     `{"plan":[],"subtasks":["first","second"]}`,
		// The subtask's action never satisfies the anchor (which requires the
		// file the action does not create).
		action: `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", "test -s never-created.txt"}, Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.SubtaskDepth = 1
		c.Agent.MaxRetries = 0
	})

	var results []TaskResult
	e.agent.Observer = func(r TaskResult) { results = append(results, r) }

	if err := e.agent.Run(context.Background()); err == nil {
		t.Error("failing subtasks must make the process fail")
	}
	last := results[len(results)-1]
	if last.Pass {
		t.Error("it cannot pass with failing subtasks")
	}
	if !strings.Contains(last.Reason, "only 0 of 2") {
		t.Errorf("the reason must count the passing subtasks: %q", last.Reason)
	}
}

// TestPlanWithSuccessCriteriaAndRisks: the criteria and risks collected during
// the analysis must reach the plan prompt (they are what makes the plan concrete).
func TestPlanWithSuccessCriteriaAndRisks(t *testing.T) {
	var planPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"the summary","success_criteria":["criterion-one"],"risks":["risk-one"],"needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			planPrompt = text
			content = `{"plan":[]}`
		default:
			content = `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"the summary", "criterion-one", "risk-one"} {
		if !strings.Contains(planPrompt, part) {
			t.Errorf("the plan prompt must carry %q", part)
		}
	}
}

// TestExecutePromptCarriesTheAttemptNumber: the phase that proposes the action
// must know which attempt it is on, because that changes the wording of the
// correction handed to the model.
func TestExecutePromptCarriesTheAttemptNumber(t *testing.T) {
	var (
		executePrompt string
		calls         int
		mu            sync.Mutex
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		mu.Lock()
		defer mu.Unlock()
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"x","needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[]}`
		case strings.Contains(text, "## FINAL ANSWER"):
			content = `{"summary":"test"}`
		default:
			calls++
			executePrompt = text
			// The command is a no-op that always satisfies the anchor: what is
			// checked here is the prompt, not the action.
			content = `{"reasoning":"try","actions":[{"command":"true"}],"final_action":{"command":""}}`
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(executePrompt, "attempt") {
		t.Errorf("the execute prompt must carry the attempt number")
	}
}

// TestGitCommitReportsAnExecutionError: when the executor itself fails, the error
// must be reported with the accumulated output.
func TestGitCommitReportsAnExecutionError(t *testing.T) {
	e := mount(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.FinalAction = config.FinalAction{Kind: "git_commit", CommitMessage: "x"}
	})
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

// TestAnUnclearRequestBecomesAQuestion: the agent must ASK when it cannot interpret the request,
// not refuse and throw the turn away.
//
// A user mistypes, abbreviates, and leaves out what they consider obvious. A request the model can
// only half interpret is incomplete information, not a failure — and there is a user on the other
// end who can complete it in three words. Refusing makes them write the whole thing again.
func TestAnUnclearRequestBecomesAQuestion(t *testing.T) {
	srv := httptest.NewServer(phaseServer{analysisUnclear: true}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	_ = e.agent.Run(context.Background())

	if result == nil {
		t.Fatal("no result was reported")
	}
	if !result.NeedsInput {
		t.Fatalf("an unclear request must ask, got %+v", result)
	}
	if result.Question == "" {
		t.Error("the user must be given something to answer")
	}
	if result.Assumption == "" {
		t.Error("the assumption must travel with the question, so the user can confirm in one word")
	}
	// The reason is NOT a failure message: the interface prints it as a question.
	if strings.HasPrefix(result.Reason, "the task was declared not understandable") {
		t.Errorf("the old refusal wording is back: %q", result.Reason)
	}
}

// TestWithNoUserTheAgentProceedsOnItsAssumption: a batch run has nobody to ask, so asking would
// stall forever. The agent acts on the reading it proposed, which is what an engineer does when
// the ticket is thin — and both are better than refusing.
func TestWithNoUserTheAgentProceedsOnItsAssumption(t *testing.T) {
	srv := httptest.NewServer(phaseServer{
		analysisUnclear: true,
		plan:            `{"plan":[],"expected_result":"st"}`,
		action:          `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":"true"}}`,
	}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = false // a piped task, a cron job: no user at the other end

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	_ = e.agent.Run(context.Background())

	if result == nil {
		t.Fatal("no result was reported")
	}
	// It did NOT stop to ask: it carried on through the loop with the assumption as its reading.
	if result.NeedsInput {
		t.Errorf("with nobody to ask the agent must proceed, not wait: %+v", result)
	}
	// And it says which reading it took. A result produced under an assumption must carry that
	// assumption: otherwise a reasonable reading looks like a wrong answer to whoever reads it
	// later, with nothing to explain the difference.
	if result.Assumption == "" {
		t.Errorf("a run that proceeded on an assumption must say so: %+v", result)
	}
}

// TestAnUnclearRequestWithNoAssumptionStillAsks: if the model cannot even propose a reading,
// there is nothing to proceed on, so the question is returned rather than a guess.
func TestAnUnclearRequestWithNoAssumptionStillAsks(t *testing.T) {
	srv := httptest.NewServer(phaseServer{analysisUnclearNoAssumption: true}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = false

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	_ = e.agent.Run(context.Background())

	if result == nil {
		t.Fatal("no result was reported")
	}
	if !result.NeedsInput {
		t.Errorf("with no assumption to proceed on, the agent must ask: %+v", result)
	}
}

// TestAnAssumptionWithNoQuestionStillAsks: the model may propose a reading without naming a
// question — it knows what it would do, and forgets to ask. Asking is still right: the user can
// confirm or correct that reading in one word, which is far cheaper than acting on a guess in
// silence.
func TestAnAssumptionWithNoQuestionStillAsks(t *testing.T) {
	srv := httptest.NewServer(phaseServer{
		analysisAssumptionOnly: true,
		plan:                   `{"plan":[],"expected_result":"st"}`,
		action:                 `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":"true"}}`,
	}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	_ = e.agent.Run(context.Background())

	if result == nil {
		t.Fatal("no result was reported")
	}
	if !result.NeedsInput {
		t.Fatalf("an assumption with no question must still ask: %+v", result)
	}
	if result.Question == "" {
		t.Error("a question must be derived so the user has something to answer")
	}
	if result.Assumption == "" {
		t.Error("the assumption must travel with the question")
	}
}

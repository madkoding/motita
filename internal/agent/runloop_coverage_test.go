package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/task"
)

// --- Misc: shellQuote, truncate, describeRules ------------------------------

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"simple":       "'simple'",
		"with space":   "'with space'",
		"with 'quote'": `'with '\''quote'\'''`,
		"":             "''",
	}
	for input, expected := range cases {
		if got := execx.Quote("/bin/sh", input); got != expected {
			t.Errorf("Quote(%q) = %q, expected %q", input, got, expected)
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
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "none"},
		nil,
	)
	text := e.agent.describeRules()
	if !strings.Contains(text, "anchor.kind=none") {
		t.Errorf("text = %q", text)
	}
}

// TestBaseVariablesWithContext: the task's context is exposed as variables.
func TestBaseVariablesWithContext(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

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
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

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
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

	// The failure this test is about is the one runActions REPORTS as an error: an action that
	// cannot run at all. A non-zero exit is not one — the agent records `[exit=N]` and hands
	// the output back to the model, which is the whole retry loop. It used to be an unknown
	// program name, which only reached this path because the policy ran unclassifiable
	// programs in silence; a refused action is the same shape without depending on that hole.
	output, err := e.agent.runActions(context.Background(), []Command{
		{Command: "programa-que-nadie-clasifico"},
	}, "")
	if err == nil {
		t.Error("an action that could not run must report an error")
	}
	if !strings.Contains(output, "[not approved:") {
		t.Errorf("the output must record why it did not run: %q", output)
	}
}

// TestARealCommandRecordsItsExitCode: the other half of the contract above, so the two can
// never be confused. A command that runs and fails is NOT an error of the run — its exit code
// travels back to the model, which is what lets the model fix the actual problem.
func TestARealCommandRecordsItsExitCode(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

	output, err := e.agent.runActions(context.Background(), []Command{{Command: "false"}}, "")
	if err != nil {
		t.Errorf("a non-zero exit is reported to the model, not as an error of the run: %v", err)
	}
	if !strings.Contains(output, "[exit=1]") {
		t.Errorf("the output must carry the real exit code: %q", output)
	}
}

// TestAnUnknownProgramIsNotRunWithoutApproval: the safety property of the default policy.
//
// A program nobody has classified is a program whose effect nobody can predict, so it is not
// run in silence — it needs the user's approval. In a run with nobody to ask (this one) that
// means it does NOT run, and the output says why. The assertion that matters is the marker
// file: if the program ran, it exists.
func TestAnUnknownProgramIsNotRunWithoutApproval(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

	dir := t.TempDir()
	marker := filepath.Join(dir, "RAN")
	program := filepath.Join(dir, "programa-que-nadie-clasifico")
	if err := os.WriteFile(program, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	output, err := e.agent.runActions(context.Background(), []Command{{Command: program}}, "")
	if err == nil {
		t.Error("an unclassifiable program with nobody to ask must report an error")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("an unclassifiable program was EXECUTED without approval")
	}
	if !strings.Contains(output, "approval") {
		t.Errorf("the output must say approval was what was missing: %q", output)
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
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

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
	for _, kind := range []string{"command", "recall", "remember"} {
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
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

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
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

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
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

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
	if source.calls != maxConsecutive {
		t.Errorf("source calls = %d, expected %d (maxConsecutive)", source.calls, maxConsecutive)
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

// TestRunActionsWithTruncatedOutput: the sandbox signals truncation and the agent
// must report it, because the model reads that output.
func TestRunActionsWithTruncatedOutput(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

	e.agent.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		return "partial output", true, 0, nil
	}

	// A KNOWN program: this test is about truncated output, and it must not also depend on the
	// policy letting an unknown binary through. `cat` is a reader, so it runs and reaches the
	// stub above.
	output, err := e.agent.runActions(context.Background(), []Command{{Command: "cat generate-a-lot"}}, "")
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

// --- Remaining branches -----------------------------------------------------

// TestRunStopsWhenTheContextIsCancelledDuringASourceFailure: a cancellation that
// arrives at the same moment as a source error must end the run cleanly, not as
// a failure.
func TestRunStopsWhenTheContextIsCancelledDuringASourceFailure(t *testing.T) {
	e := mount(
		t,
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		nil,
	)

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

	e := mount(
		t,
		srv,
		config.Anchor{
			Kind:    "command",
			Command: "sh",
			Args:    []string{"-c", "test -s never-created.txt"},
			Timeout: 5 * time.Second,
		},
		func(c *config.Config) {
			c.Agent.SubtaskDepth = 1
			c.Agent.MaxRetries = 0
		},
	)

	var results []TaskResult
	e.agent.Observer = func(r TaskResult) { results = append(results, r) }

	if err := e.agent.Run(context.Background()); err == nil {
		t.Error("failing subtasks must make the process fail")
	}
	last := results[len(results)-1]
	if last.Pass {
		t.Error("it cannot pass with failing subtasks")
	}
	// The reason names WHICH subtask failed, and says the one after it never started: it
	// builds on the first, and running it on a failed base would spend its whole budget.
	if !strings.Contains(last.Reason, "subtask 1 of 2") || !strings.Contains(last.Reason, "not started") {
		t.Errorf("the reason must name the failing subtask and the skipped ones: %q", last.Reason)
	}
	if last.Subtasks != 1 {
		t.Errorf("subtasks started = %d, want 1: the second must not run after the first failed", last.Subtasks)
	}
}

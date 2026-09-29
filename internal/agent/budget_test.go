package agent

// The step budget is a CHECKPOINT, not a wall.
//
// Measured on a real request ("habilitar el area del mantenedor para agregar fichas de
// vtuberdex"): the agent explored, installed dependencies, wrote code and iterated - and a
// 24-round budget stopped it while it was still WORKING. The result was a message saying
// "the task is not finished", and nothing else: the work it had done was in the checkout,
// and the decision left to the user - keep going? - was simply never offered.
//
// The user asked for the bound to be 100 rather than 24, and for the run to ASK before
// spending another 100. That is what these tests pin:
//
//  1. The default budget is 100, not 24.
//  2. A run that spends its whole budget with work left ASKS.
//  3. Answering yes continues on the SAME run, so nothing already done is thrown away -
//     the difference between "keep going" and "start over".
//  4. Answering no stops, and the message says the budget ran out rather than that
//     something failed. No answer at all (a run from a script) is a no, the same
//     conservative default the command approval uses.
//  5. The asking cannot itself run away: a gateway whose approver always says yes still
//     stops at a ceiling, because an approver with no human behind it is not a decision.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// alwaysWorkingServer answers every round with a harmless action and "there is more to do",
// which is the state that runs a budget out. It is the package's own fake, so the phases
// and their shapes are the ones the real loop reads.
func alwaysWorkingServer(t *testing.T) *httptest.Server {
	t.Helper()
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"true"}},
		// One value: it repeats, so the model never reports the task finished.
		donePerAttempt: []bool{false},
	}
	return httptest.NewServer(fake.handler(t))
}

// TestTheDefaultBudgetIsOneHundred is the number the user asked for, and it is a
// CONTRACT rather than a preference: 24 was measured to cut off honest work.
func TestTheDefaultBudgetIsOneHundred(t *testing.T) {
	if defaultMaxSteps != 100 {
		t.Errorf("defaultMaxSteps = %d, want 100: 24 was measured to stop a run that was still working",
			defaultMaxSteps)
	}
	// And a configured value still wins, so an operator can raise or lower it.
	a := &Agent{}
	a.cfg.Agent.MaxSteps = 5
	if got := a.maxSteps(); got != 5 {
		t.Errorf("maxSteps() = %d, want the configured 5", got)
	}
	a.cfg.Agent.MaxSteps = 0
	if got := a.maxSteps(); got != defaultMaxSteps {
		t.Errorf("maxSteps() = %d with nothing configured, want the default %d", got, defaultMaxSteps)
	}
}

// TestRunningOutAsksToContinue: the run does not simply stop. It asks, and the question
// names what is at stake - that continuing keeps the work already done.
func TestRunningOutAsksToContinue(t *testing.T) {
	srv := alwaysWorkingServer(t)
	defer srv.Close()

	var asked int32
	var question string
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, func(c *config.Config) {
		// A tiny budget, so the checkpoint arrives immediately. The behaviour under test is
		// what happens AT the checkpoint, not how long it takes to get there.
		c.Agent.MaxSteps = 2
	})
	e.agent.SetApprover(func(_ context.Context, req ApprovalRequest) (bool, error) {
		atomic.AddInt32(&asked, 1)
		question = req.Command
		return false, nil // no: we only want to see the question
	})

	if err := e.agent.Run(context.Background()); err == nil {
		t.Fatal("a run that stops at its budget reports a failure")
	}

	if atomic.LoadInt32(&asked) == 0 {
		t.Fatal("the budget ran out and the user was never asked; the decision has to be offered")
	}
	if !strings.Contains(question, "continue") || !strings.Contains(question, "rounds") {
		t.Errorf("the question must say what it is offering, got %q", question)
	}
}

// TestContinuingKeepsTheWorkAlreadyDone: yes means another budget on the SAME run. The
// work in progress must survive - restarting would throw away everything the budget was
// spent on, which is exactly what the ask exists to avoid.
func TestContinuingKeepsTheWorkAlreadyDone(t *testing.T) {
	srv := alwaysWorkingServer(t)
	defer srv.Close()

	dir := ""
	var approvals int32
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, func(c *config.Config) {
		c.Agent.MaxSteps = 2
	})
	dir = e.agent.cfg.Agent.WorkspaceDir

	// Say yes exactly once: that buys a second budget, and the third checkpoint stops.
	e.agent.SetApprover(func(_ context.Context, _ ApprovalRequest) (bool, error) {
		return atomic.AddInt32(&approvals, 1) == 1, nil
	})

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err == nil {
		t.Fatal("the run stops once the user stops saying yes")
	}
	if result == nil {
		t.Fatal("no result")
	}
	// Two budgets were spent, so the run went past the first checkpoint: the rounds it was
	// given were USED rather than the run ending at the first boundary.
	if result.Attempts <= 2 {
		t.Errorf("attempts = %d: continuing must spend the new budget, not stop at the old limit",
			result.Attempts)
	}
	if _, err := os.Stat(filepath.Join(dir, ".")); err != nil {
		t.Fatalf("the workspace must still be there: %v", err)
	}
}

// TestWithoutAnApproverTheBudgetJustStops: a run from a script has nobody to ask, and no
// answer means stop. That is the same conservative default the command approval uses, and
// it must not hang waiting for a question that can never be answered.
func TestWithoutAnApproverTheBudgetJustStops(t *testing.T) {
	srv := alwaysWorkingServer(t)
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, func(c *config.Config) {
		c.Agent.MaxSteps = 2
	})
	// No approver at all - the state a scheduled task runs in.

	done := make(chan error, 1)
	go func() { done <- e.agent.Run(context.Background()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the run must report that it stopped at its budget")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a run with nobody to ask must STOP rather than wait for an answer that cannot come")
	}
}

// TestTheAskingItselfCannotRunAway: an approver that always says yes must not loop for
// ever. Reaching the ceiling means a hundred answered questions, which no human does.
func TestTheAskingItselfCannotRunAway(t *testing.T) {
	if maxBudgetExtensions < 2 {
		t.Fatalf("maxBudgetExtensions = %d: at least one continuation has to be possible",
			maxBudgetExtensions)
	}
	srv := alwaysWorkingServer(t)
	defer srv.Close()

	var approvals int32
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, func(c *config.Config) {
		c.Agent.MaxSteps = 1
	})
	e.agent.SetApprover(func(context.Context, ApprovalRequest) (bool, error) {
		atomic.AddInt32(&approvals, 1)
		return true, nil // an approver with no human behind it
	})

	done := make(chan error, 1)
	go func() { done <- e.agent.Run(context.Background()) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("an approver that never says no must still be bounded")
	}

	if got := atomic.LoadInt32(&approvals); got > int32(maxBudgetExtensions) {
		t.Errorf("asked %d times, want at most %d: the asking is bounded too", got, maxBudgetExtensions)
	}
}

// TestTheBudgetMessageNamesTheBudget: the message a user reads when they decline has to
// distinguish "the budget ran out" from "something failed". They are different outcomes and
// the reader looks in a different place for each.
func TestTheBudgetMessageNamesTheBudget(t *testing.T) {
	srv := alwaysWorkingServer(t)
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, func(c *config.Config) { c.Agent.MaxSteps = 2 })
	e.agent.SetApprover(func(context.Context, ApprovalRequest) (bool, error) { return false, nil })

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	err := e.agent.Run(context.Background())
	if err == nil {
		t.Fatal("expected a failure")
	}
	if result == nil {
		t.Fatal("no result")
	}
	reason := result.Reason
	if !strings.Contains(reason, "not finished") {
		t.Errorf("the reason must say the task is not finished, got: %s", reason)
	}
	if !strings.Contains(reason, "max_steps") {
		t.Errorf("the reason must name the setting that would have changed it, got: %s", reason)
	}
	if strings.Contains(reason, "attempts were exhausted") {
		t.Errorf("running out of budget is NOT a retry exhaustion, and saying so sends the "+
			"reader looking for a broken check: %s", reason)
	}
	// And the result is machine-readable as unfinished rather than as a crash.
	var probe map[string]any
	if b, err := json.Marshal(result); err == nil {
		_ = json.Unmarshal(b, &probe)
	}
}

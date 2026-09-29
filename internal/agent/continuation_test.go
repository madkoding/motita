package agent

// The loop must be a loop of PROGRESS, not only a loop of RETRIES.
//
// Measured on a real run in a real project: the plan declared 11 steps, the agent proposed
// ONE batch of four actions, the anchor passed — and the task was reported complete in
// twelve seconds with ten steps untouched.
//
// The anchor was not at fault. It validates the STATE OF THE PROJECT (does the gate still
// pass?), never how much of the plan was carried out, so on a healthy repository it passes
// before any work has happened. The defect was in the loop: it iterated only when something
// FAILED, and returned the moment the anchor was happy. A task that needed twenty
// determinations got one.
//
// The fix these tests pin is that the model can say "there is more to do", and the loop
// honours it. A round in which the model reports work left is not a failure — it is work —
// so it must not be counted against max_retries, and the run must not stop.

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// TestTheAgentContinuesWhileTheModelReportsWorkLeft: the core of the fix. Three rounds are
// proposed; the anchor passes after EVERY one (it only checks that the project is healthy);
// the model reports the task finished on the third. All three rounds must run.
//
// With the defect the first round passes the anchor and the loop returns, so two of the
// three files never exist — which is what makes this test fail for the right reason.
func TestTheAgentContinuesWhileTheModelReportsWorkLeft(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{
			{"echo one > one.txt"},
			{"echo two > two.txt"},
			{"echo three > three.txt"},
		},
		// The model reports work left twice and finishes on the third round.
		donePerAttempt: []bool{false, false, true},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "exit 0"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, func(c *config.Config) { c.Agent.MaxSteps = 10 })

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("the agent returned an error: %v", err)
	}
	if result == nil {
		t.Fatal("the task result was not captured")
	}
	if !result.Pass {
		t.Fatalf("PASS was expected once the model reported the task finished, reason: %s", result.Reason)
	}

	// The proof: every round's work is on disk. One file means the loop stopped early.
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		if _, err := os.Stat(filepath.Join(e.dir, name)); err != nil {
			t.Errorf("round's work is missing (%s): the loop did not continue: %v", name, err)
		}
	}
	if result.Attempts != 3 {
		t.Errorf("rounds = %d, expected 3: the loop must run until the model reports the task finished", result.Attempts)
	}
}

// TestTheAgentStopsWhenTheModelReportsTheTaskFinished: a model that reports the task
// finished on the first round stops there. This is what keeps a small task from spending
// the whole step budget.
func TestTheAgentStopsWhenTheModelReportsTheTaskFinished(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"echo hello > only.txt"}},
		donePerAttempt:    []bool{true},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "exit 0"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, func(c *config.Config) { c.Agent.MaxSteps = 10 })

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil || !result.Pass {
		t.Fatalf("expected PASS: %+v", result)
	}
	if result.Attempts != 1 {
		t.Errorf("rounds = %d, expected 1: a model that reports the task finished must not be asked again", result.Attempts)
	}
}

// TestAModelThatNeverFinishesIsBoundedAndSaysSo: a model that reports work left forever
// must not run forever, and the run must NOT declare PASS — it must say it ran out of
// rounds, which is a different failure from running out of retries.
func TestAModelThatNeverFinishesIsBoundedAndSaysSo(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"true"}},
		// One value repeated for every round: the model never reports the task finished.
		donePerAttempt: []bool{false},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "exit 0"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, func(c *config.Config) { c.Agent.MaxSteps = 3 })

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	err := e.agent.Run(context.Background())
	if err == nil {
		t.Error("a task that never finishes must make the agent finish with an error")
	}
	if result == nil {
		t.Fatal("no result")
	}
	if result.Pass {
		t.Fatal("it must never declare PASS when the work is still unfinished")
	}
	if result.Attempts != 3 {
		t.Errorf("rounds = %d, expected the bound of 3", result.Attempts)
	}
	// The message must name the CAUSE. "attempts exhausted without passing validation" is
	// the retry message and would send the reader to look for a broken check.
	if !strings.Contains(result.Reason, "3") {
		t.Errorf("the reason should name the bound that was reached, got: %s", result.Reason)
	}
	if strings.Contains(result.Reason, "without passing validation") {
		t.Errorf("the reason must not report a retry exhaustion when the anchor never failed, got: %s", result.Reason)
	}
}

// TestARejectedRoundStillCountsAgainstTheRetries: the two counters mean different things
// and both must keep working. A round the anchor REJECTS is what max_retries bounds.
func TestARejectedRoundStillCountsAgainstTheRetries(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"echo wrong"}},
		donePerAttempt:    []bool{false},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "exit 1"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, func(c *config.Config) {
		c.Agent.MaxRetries = 1
		c.Agent.MaxSteps = 50
	})

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err == nil {
		t.Error("an anchor that never passes must make the agent finish with an error")
	}
	if result == nil {
		t.Fatal("no result")
	}
	// max_retries=1 => the second rejection ends it, NOT the step budget.
	if result.Attempts != 2 {
		t.Errorf("rounds = %d, expected 2 (max_retries=1): a rejected round is a RETRY, not a step", result.Attempts)
	}
	if !strings.Contains(result.Reason, "exhausted") {
		t.Errorf("reason = %q, expected the retry message", result.Reason)
	}
}

// TestAnActionWithNoDoneFieldKeepsTheOldBehaviour: every configuration that predates this
// field must keep working. A response that does not carry the field is treated as "the
// task is finished", which is exactly what the loop did before — otherwise a customised
// prompt would suddenly spend its whole step budget on a task that was already done.
func TestAnActionWithNoDoneFieldKeepsTheOldBehaviour(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"echo hello > legacy.txt"}},
		// donePerAttempt is nil: the fake omits the field entirely, as an older prompt would.
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "exit 0"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, func(c *config.Config) { c.Agent.MaxSteps = 10 })

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil || !result.Pass {
		t.Fatalf("expected PASS: %+v", result)
	}
	if result.Attempts != 1 {
		t.Errorf("rounds = %d, expected 1: a response without the field means the task is finished", result.Attempts)
	}
}

// TestTheNextRoundIsToldWhatThePreviousOneDid: a round that continues must carry what was
// already done, or the model repeats it. The prompt of the second round must contain the
// first round's output.
func TestTheNextRoundIsToldWhatThePreviousOneDid(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{
			{"echo the-first-round-ran"},
			{"echo the-second-round-ran"},
		},
		donePerAttempt: []bool{false, true},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "exit 0"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, func(c *config.Config) { c.Agent.MaxSteps = 10 })

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	prompt := fake.lastExecutePrompt()
	if !strings.Contains(prompt, "the-first-round-ran") {
		t.Errorf("the second round's prompt must carry what the first round did, or the model repeats it:\n%s", prompt)
	}
}

// TestProgressRoundsDoNotSpendTheRetryBudget: the two counters must stay independent.
//
// A round the anchor ACCEPTS is progress, not a correction, so it must not consume
// max_retries — otherwise a task that legitimately takes several rounds is killed by the
// retry budget, which is the opposite failure of the one this file fixes.
//
// This is the test that tells the two apart: MaxRetries=1 leaves room for exactly TWO
// refused rounds. The first round is ACCEPTED (done=false), and two more are refused, so
// the run must reach round three. If a progress round were charged to the retry budget,
// it would stop at round two.
func TestProgressRoundsDoNotSpendTheRetryBudget(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{
			{"echo progress > progress.txt"}, // round 1: ACCEPTED, but more to do
			{"rm -f progress.txt"},           // round 2: refused (rejected=1)
			{"echo wrong"},                   // round 3: refused (rejected=2 > max_retries)
		},
		donePerAttempt: []bool{false},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command",
		// The anchor is happy while the marker exists. Round 1 creates it, round 2
		// removes it, so round 1 is ACCEPTED and rounds 2 and 3 are REFUSED.
		Command:    "sh",
		Args:       []string{"-c", "test -s progress.txt"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, func(c *config.Config) {
		c.Agent.MaxRetries = 1
		c.Agent.MaxSteps = 50
	})

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err == nil {
		t.Error("a run that never finishes must end with an error")
	}
	if result == nil {
		t.Fatal("no result")
	}
	// Two REFUSED rounds are allowed (max_retries=1 => 2 attempts). The first, accepted
	// round must not be charged to that budget, so three rounds run in total.
	if result.Attempts != 3 {
		t.Errorf("rounds = %d, expected 3: an ACCEPTED round must not spend the retry budget "+
			"(max_retries=1 allows two REFUSED rounds, and the first round was accepted)",
			result.Attempts)
	}
	if !strings.Contains(result.Reason, "exhausted") {
		t.Errorf("reason = %q, expected the retry message", result.Reason)
	}
}

// TestTheStepBudgetDefaultsWhenNoneIsConfigured: a configuration that names no max_steps
// still gets a bound, and it is the built-in one. Without this the loop would either run
// forever or stop at zero rounds.
func TestTheStepBudgetDefaultsWhenNoneIsConfigured(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Agent.MaxSteps = 0 // what a configuration written before this field looks like

	a := &Agent{cfg: c, log: logx.Global()}
	if got := a.maxSteps(); got != defaultMaxSteps {
		t.Errorf("maxSteps() = %d with nothing configured, want the built-in default %d", got, defaultMaxSteps)
	}

	// And a configured value wins.
	c.Agent.MaxSteps = 7
	a = &Agent{cfg: c, log: logx.Global()}
	if got := a.maxSteps(); got != 7 {
		t.Errorf("maxSteps() = %d with 7 configured, want 7", got)
	}
	_ = dir
}

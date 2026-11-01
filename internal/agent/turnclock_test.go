package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/logx"
)

// TestALineApprovedInTheTurnIsNotAskedAgain: the line the person said yes to runs again in the
// same turn without a second question; another line, a declined one, and the same line in the
// next turn are asked about.
func TestALineApprovedInTheTurnIsNotAskedAgain(t *testing.T) {
	a := agentWith(t, false)
	ran := 0
	a.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		ran++
		return "ok", false, 0, nil
	}
	var asked []string
	answer := true
	a.SetApprover(func(_ context.Context, req ApprovalRequest) (bool, error) {
		asked = append(asked, req.Command)
		return answer, nil
	})
	outside := "rm -rf " + a.cfg.Agent.WorkspaceDir + "/../outside"
	other := "rm -rf " + a.cfg.Agent.WorkspaceDir + "/../another"
	run := func(line string) {
		t.Helper()
		_, _ = a.runActions(context.Background(), []Command{{Command: line}}, "")
	}

	run(outside)
	run(" " + outside + " ")
	if len(asked) != 1 || ran != 2 {
		t.Fatalf("the approved line must run again unasked: asked %q, ran %d", asked, ran)
	}
	if a.clock.asked.Load() != 1 || a.clock.reusedAsked.Load() != 1 || a.clock.commandRuns.Load() != 2 {
		t.Errorf("the clock must count one question, one reuse and two runs: %d %d %d",
			a.clock.asked.Load(), a.clock.reusedAsked.Load(), a.clock.commandRuns.Load())
	}

	answer = false
	run(other)
	run(other)
	if len(asked) != 3 {
		t.Errorf("a declined line must be asked about each time: %q", asked)
	}

	a.approvedInTurn.reset()
	answer = true
	run(outside)
	if len(asked) != 4 {
		t.Errorf("a new turn must ask again: %q", asked)
	}
}

// TestTheTurnSaysWhereItsTimeWent: the turn's last log line divides its time between the model,
// the commands, the questions and the gate, and starts over with the next turn.
func TestTheTurnSaysWhereItsTimeWent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	log, err := logx.New(logx.Options{Path: path, Level: logx.Info})
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{log: log}
	start := time.Now().Add(-time.Second)
	since(&a.clock.model, &a.clock.modelCalls, start)
	a.clock.anchor.Add(int64(2 * time.Second)) // more than the turn: "other" cannot go below zero
	a.logTurnTimings(start)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	for _, want := range []string{`"turn timings"`, `"model_calls":1`, `"anchor_ms":2000`, `"other_ms":0`} {
		if !strings.Contains(line, want) {
			t.Errorf("the timings line must carry %s: %s", want, line)
		}
	}
	a.clock.reset()
	if a.clock.modelCalls.Load() != 0 || a.clock.anchor.Load() != 0 {
		t.Error("a new turn must start the clock over")
	}
}

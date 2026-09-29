package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/task"
)

// The working memory: see workmemory.go for the measured failure (one file read 69 times in a
// run of 115 rounds).

// TestAReadIsKeptAndNotRunTwice: the model asks for the same file in three rounds. It is
// executed once, the later rounds are told where the answer is, and they SEE the content.
func TestAReadIsKeptAndNotRunTwice(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		switch round {
		case 1:
			return step(false, "echo CONTENT-OF-THE-FILE")
		case 2, 3:
			return step(false, "echo  CONTENT-OF-THE-FILE") // same words, different spacing
		}
		return step(true)
	}}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if !strings.Contains(s.executes[1], "WHAT YOU HAVE ALREADY READ") ||
		!strings.Contains(s.executes[1], "CONTENT-OF-THE-FILE") {
		t.Errorf("round 2 must be shown the kept output:\n%s", s.executes[1])
	}
	if !strings.Contains(s.executes[2], "not run again") {
		t.Errorf("round 3 must be told its repeat was not executed:\n%s", s.executes[2])
	}
}

// TestAWriteForgetsEveryKeptRead: after a write the files may say something else, so the
// same read is executed again and shows the new content.
func TestAWriteForgetsEveryKeptRead(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		switch round {
		case 1:
			return step(false, "echo old > f.txt", "cat f.txt")
		case 2:
			return step(false, "echo new > f.txt")
		case 3:
			return step(false, "cat f.txt")
		}
		return step(true)
	}}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if strings.Contains(s.executes[3], "not run again") {
		t.Errorf("a read after a write must run:\n%s", s.executes[3])
	}
	if !strings.Contains(s.executes[3], "new") {
		t.Errorf("round 4 must see the new content:\n%s", s.executes[3])
	}
	// The old read must not survive into the round after the write.
	if strings.Contains(s.executes[2], "### (round 1) $ cat f.txt") {
		t.Errorf("a read from before the write was still offered:\n%s", s.executes[2])
	}
}

// TestARepeatedReadIsNotProgress: a run that keeps asking for what it already holds is
// stopped as a stall even though each round's wording differs.
func TestARepeatedReadIsNotProgress(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(false, "echo data")
		}
		return step(false, "echo   data")
	}}
	_, result, err := runScript(t, s, "exit 0", func(c *config.Config) { c.Agent.MaxSteps = 50 })
	if err == nil || result.Pass || !strings.Contains(result.Reason, "stalled") {
		t.Fatalf("err=%v pass=%v reason=%q", err, result.Pass, result.Reason)
	}
	if result.Attempts > stallStopAt+2 {
		t.Errorf("rounds = %d: the run went on far past the stall bound", result.Attempts)
	}
}

// TestNotesSurviveEveryRound: notes written in round 1 are in the prompt of round 12,
// long after the journal shrank round 1 to a line, and are replaced by newer ones.
func TestNotesSurviveEveryRound(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		var m map[string]any
		_ = json.Unmarshal([]byte(step(round > 12, fmt.Sprintf("echo r%d", round))), &m)
		switch round {
		case 1:
			m["notes"] = "PLAN-ALPHA: schema done, route left"
		case 5:
			m["notes"] = "PLAN-BETA: route done"
		}
		return mustJSON(m)
	}}
	_, result, err := runScript(t, s, "exit 0", func(c *config.Config) { c.Agent.MaxSteps = 50 })
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if !strings.Contains(s.executes[3], "PLAN-ALPHA") {
		t.Errorf("round 4 lost the notes:\n%s", s.executes[3])
	}
	last := s.executes[len(s.executes)-1]
	if !strings.Contains(last, "PLAN-BETA") || strings.Contains(last, "PLAN-ALPHA") {
		t.Errorf("the last round must carry the newest notes only:\n%s", last)
	}
}

func TestReadsOnly(t *testing.T) {
	yes := []string{
		"cat -n lib/api.ts", "sed -n '1,200p' a.go", "git status --short && echo --- ; git diff --stat",
		"grep -n 'a > b' file", `grep -n "a > b" file`, "ls -la 2>/dev/null", "cat a | head -20", "find . -name '*.go'",
		"echo '=== x ==='; grep -n foo bar || echo MISSING", "awk '/a/,/b/' f",
	}
	no := []string{
		"", "   ", "echo x > f", "cat a >> b", "sed -i s/a/b/ f", "npm ci", "rm -rf x", "git commit -m x",
		"find . -delete", "find . -exec rm {} ;", "cat $(rm f)", "cat `rm f`", "echo \"$(rm f)\"",
		"cat f; rm f", "cat f | tee g", "git checkout main", "awk 'BEGIN{system(\"rm f\")}'",
		"sed --in-place s/a/b/ f", "python3 x.py", "cat < f",
	}
	for _, c := range yes {
		if !readsOnly(c) {
			t.Errorf("%q reads only", c)
		}
	}
	for _, c := range no {
		if readsOnly(c) {
			t.Errorf("%q must not be a read", c)
		}
	}
}

func TestIsReadAndIsWrite(t *testing.T) {
	if !isRead(Command{Command: "cat x"}) || isWrite(Command{Command: "cat x"}) {
		t.Error("cat is a read")
	}
	if isRead(Command{Command: "rm x"}) || !isWrite(Command{Command: "rm x"}) {
		t.Error("rm is a write")
	}
	if isRead(Command{Kind: "read_skill", Command: "x"}) || isWrite(Command{Kind: "read_skill", Command: "x"}) {
		t.Error("a library action is neither")
	}
	if isWrite(Command{Command: "  "}) {
		t.Error("an empty command writes nothing")
	}
}

func TestWorkMemoryBounds(t *testing.T) {
	var m workMemory
	if m.render() != "" {
		t.Error("an empty memory renders nothing")
	}
	m.setNotes("   ")
	if m.notes != "" {
		t.Error("blank notes are not notes")
	}
	m.setNotes("keep")
	m.setNotes("")
	if m.notes != "keep" {
		t.Error("a reply without notes must not erase them")
	}
	m.setNotes(strings.Repeat("n", notesMax*2))
	if len(m.notes) > notesMax+3 {
		t.Errorf("notes = %d bytes", len(m.notes))
	}
	for i := 0; i < 20; i++ {
		m.remember(fmt.Sprintf("cat f%d", i), i, strings.Repeat("x", evidenceEach))
	}
	m.remember("cat f3", 99, "again") // moves to the newest place, not duplicated
	if len(m.order) != 20 {
		t.Errorf("order = %d entries", len(m.order))
	}
	if got := len(m.render()); got > evidenceTotal+notesMax+4000 {
		t.Errorf("render = %d bytes: the kept reads must stay bounded", got)
	}
	if !strings.Contains(m.render(), "(round 99) $ cat f3") {
		t.Error("the newest read must be shown first")
	}
	m.invalidate()
	if _, ok := m.recall("cat f3"); ok || len(m.order) != 0 {
		t.Error("invalidate must forget everything")
	}
}

// --- The conversation keeps a run that was cut off ---------------------------------------
//
// Reported from real use: a run of 115 rounds was interrupted by a restart and the session
// afterwards showed NOTHING - not the request, not the work done. The turn was written only
// when the task ended, so a run that never ended left no trace in the conversation.

func TestARunThatIsCutOffLeavesItsRequestAndItsWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 3 {
			cancel() // the restart arrives in the middle of the third round
		}
		return step(false, fmt.Sprintf("echo work-%d > w%d.txt", round, round))
	}}
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second}, func(c *config.Config) { c.Agent.MaxSteps = 50 })
	_ = e.agent.Run(ctx)

	turns := e.agent.Transcript()
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want the one request: %+v", len(turns), turns)
	}
	got := turns[0]
	if got.User == "" || !strings.Contains(got.Agent, "cancelled") || !strings.Contains(got.Agent, "echo work-1") {
		t.Errorf("the interrupted request must stay, with the work it had done: %+v", got)
	}
}

// TestAPendingTurnIsNotPresentedAsAnAnswer: a turn still open (the process died before any
// outcome) reads as "cut off" in the next prompt, never as something the agent said.
func TestAPendingTurnIsNotPresentedAsAnAnswer(t *testing.T) {
	a := &Agent{}
	tk := task.Task{Description: "do it"}
	a.begin(tk)
	a.trail(tk, []string{"round 1: echo SECRET-TRAIL"})
	if got := a.workSoFar(tk); !strings.Contains(got, "SECRET-TRAIL") {
		t.Errorf("workSoFar = %q", got)
	}
	if d := a.dialogue(); !strings.Contains(d, "cut off") || strings.Contains(d, "SECRET-TRAIL") {
		t.Errorf("dialogue = %q", d)
	}
	if (&Agent{}).workSoFar(tk) != "" {
		t.Error("no pending turn, no work")
	}
}

func TestAFinishedRunClosesItsPendingTurn(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round < 3 {
			return step(false, fmt.Sprintf("echo r%d", round))
		}
		return step(true)
	}}
	e, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	turns := e.agent.Transcript()
	if len(turns) != 1 || turns[0].Pending {
		t.Fatalf("one closed turn expected, got %+v", turns)
	}
}

func TestAResumedRunContinuesItsOwnTurn(t *testing.T) {
	a := &Agent{}
	tk := task.Task{Description: "do the thing"}
	a.begin(tk)
	a.begin(tk) // a resumed run: same request, still pending
	a.begin(task.Task{Description: "other"})
	if n := len(a.Transcript()); n != 2 {
		t.Fatalf("turns = %d, want 2", n)
	}
	a.trail(task.Task{Description: "unrelated"}, []string{"x"}) // not the pending turn: ignored
	if strings.Contains(a.Transcript()[1].Agent, "x") {
		t.Error("a trail for another request must not land here")
	}
	a.trail(task.Task{Description: "other"}, []string{strings.Repeat("\u00e9", pendingLimit) + "x"})
	if got := a.Transcript()[1].Agent; !utf8.ValidString(got) || len(got) > pendingLimit+len(pendingText)+40 {
		t.Errorf("the trail must stay bounded and valid UTF-8 (%d bytes)", len(got))
	}
	(&Agent{}).trail(tk, []string{"x"}) // nothing pending at all: no panic
}

// TestAResumedRunStartsFromTheWorkItHadDone: after an interruption the gateway runs the same
// request again. Its first prompt must carry what the earlier life did, or the model treats
// the task as new and rebuilds what is already on disk.
func TestAResumedRunStartsFromTheWorkItHadDone(t *testing.T) {
	s := &scriptServer{execute: func(int, string) string { return step(true) }}
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second}, nil)
	e.agent.SetTranscript([]DialogueTurn{{
		User: "do the test task", Kind: KindTask, Pending: true,
		Agent: pendingText + workMarker + "round 7: git apply SCHEMA-PATCH",
	}})
	var results []TaskResult
	e.agent.Observer = func(r TaskResult) { results = append(results, r) }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := s.executes[0]
	if !strings.Contains(first, "SCHEMA-PATCH") || !strings.Contains(first, "git status") {
		t.Errorf("the first prompt of a resumed run must carry the earlier work:\n%s", first)
	}
	if n := len(e.agent.Transcript()); n != 1 {
		t.Errorf("a resumed run continues its own turn, got %d turns", n)
	}
}

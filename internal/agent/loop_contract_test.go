package agent

// THE LOOP CONTRACT and THE SUBTASK CONTRACT.
//
// Reported from real use: "the agent struggles to carry out tasks and subtasks". Each test
// below pins one of the defects that produced that, measured in the loop as it was:
//
//  1. Every round the next round read was labelled "Failed attempt N" - a round that made
//     PROGRESS included - so a model that had done half the work was told it had failed,
//     and redid it.
//  2. The anchor ran after every round, and a round whose intermediate state broke the gate
//     (a renamed function before its callers were updated) was charged to max_retries: a
//     long task died at its fourth intermediate round, and every round paid for the gate.
//  3. ONE malformed reply ended the whole task, and so did the most natural way of saying
//     "it is already finished": no actions and "done": true.
//  4. A model repeating itself spent the whole budget going nowhere.
//  5. A subtask got its own line and nothing else, could answer as chat (a PASS for work
//     never done) or stop to ask (a question the user never saw), and a failure did not
//     stop the subtasks that built on it.
//  6. The final answer was synthesised from the LAST round's output only.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/config"
)

// scriptServer is a fake model whose every phase is a function, so a test can script exactly
// the conversation it needs and read back every prompt it was sent.
type scriptServer struct {
	mu       sync.Mutex
	analyze  func(prompt string) string
	plan     func(prompt string) string
	execute  func(round int, prompt string) string
	executes []string
	analyses []string
	synth    []string
	// sse answers a streamed request as a real provider does: the reasoning tokens first,
	// then the reply in fragments.
	sse bool
}

func (s *scriptServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		s.mu.Lock()
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			s.analyses = append(s.analyses, text)
			content = `{"kind":"task","understandable":true,"summary":"the work","success_criteria":["it is done"]}`
			if s.analyze != nil {
				content = s.analyze(text)
			}
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[{"step":1,"action":"do it","command":""}],"subtasks":[]}`
			if s.plan != nil {
				content = s.plan(text)
			}
		case strings.Contains(text, "## FINAL ANSWER"):
			s.synth = append(s.synth, text)
			content = `{"summary":"all done"}`
		case strings.Contains(text, "## ACTION"):
			s.executes = append(s.executes, text)
			content = s.execute(len(s.executes), text)
		default:
			t.Errorf("unrecognisable prompt: %q", truncate(text, 200))
			content = "{}"
		}
		s.mu.Unlock()
		if s.sse && req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			frame := func(delta map[string]string) {
				data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			frame(map[string]string{"reasoning_content": "weighing the options"})
			for len(content) > 0 {
				n := min(7, len(content))
				frame(map[string]string{"content": content[:n]})
				content = content[n:]
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}
}

// step is one execute-phase reply.
func step(done bool, commands ...string) string {
	actions := []map[string]string{}
	for _, c := range commands {
		actions = append(actions, map[string]string{"kind": "command", "description": "step", "command": c})
	}
	return mustJSON(map[string]any{
		"reasoning": "scripted", "actions": actions,
		"final_action": map[string]string{"command": ""}, "done": done,
	})
}

// runScript mounts the agent on a script and runs one task to its result.
func runScript(t *testing.T, s *scriptServer, anchorScript string, cfg func(*config.Config)) (*fixture, TaskResult, error) {
	t.Helper()
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", anchorScript},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, cfg)
	var results []TaskResult
	e.agent.Observer = func(r TaskResult) { results = append(results, r) }
	err := e.agent.Run(context.Background())
	if len(results) == 0 {
		t.Fatal("no result was observed")
	}
	// The OUTERMOST result is observed last.
	return e, results[len(results)-1], err
}

// TestAProgressRoundIsNotReportedAsAFailure: defect 1. The round after a progress round is
// told it made progress, in those words, and the word "failed" does not appear.
func TestAProgressRoundIsNotReportedAsAFailure(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(false, "echo first-half > half.txt")
		}
		return step(true, "echo second-half >> half.txt")
	}}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("the task should pass: err=%v reason=%s", err, result.Reason)
	}
	second := s.executes[1]
	if !strings.Contains(second, "PROGRESS") || !strings.Contains(second, "echo first-half") {
		t.Errorf("round 2 must be told round 1 was PROGRESS, and what it ran:\n%s", second)
	}
	if strings.Contains(strings.ToLower(second), "failed attempt") {
		t.Errorf("a progress round was reported to the model as a failure:\n%s", second)
	}
}

// TestTheAnchorRunsOnlyOnAClaimOfDone: defect 2. Two progress rounds break the gate on
// purpose (the marker the anchor wants is missing until round 3), and neither is charged to
// max_retries=0 - the task still passes when the model finishes. The anchor ran ONCE.
func TestTheAnchorRunsOnlyOnAClaimOfDone(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		switch round {
		case 1:
			return step(false, "echo broken > state.txt")
		case 2:
			return step(false, "echo still-broken >> state.txt")
		default:
			return step(true, "echo fixed > ready.txt")
		}
	}}
	e, result, err := runScript(t, s, "echo run >> anchor-runs.txt; test -s ready.txt",
		func(c *config.Config) { c.Agent.MaxRetries = 0 })
	if err != nil || !result.Pass {
		t.Fatalf("intermediate rounds must not be charged to the retries: err=%v reason=%s", err, result.Reason)
	}
	data, _ := os.ReadFile(filepath.Join(e.dir, "anchor-runs.txt"))
	if n := strings.Count(string(data), "run"); n != 1 {
		t.Errorf("the anchor ran %d times, want 1: it runs on a claim of done, not on every round", n)
	}
	if result.Attempts != 3 {
		t.Errorf("rounds = %d, want 3", result.Attempts)
	}
}

// TestOneUnusableReplyIsRecoveredFrom: defect 3. A malformed reply, and a reply that claims
// work is left while proposing nothing, are both told to the model - and the run carries on.
func TestOneUnusableReplyIsRecoveredFrom(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		switch round {
		case 1:
			return "I will now create the file, one moment"
		case 2:
			return step(false) // nothing to run, and "work left": not a round of work
		default:
			return step(true, "echo ok > ok.txt")
		}
	}}
	_, result, err := runScript(t, s, "test -s ok.txt", nil)
	if err != nil || !result.Pass {
		t.Fatalf("an unusable reply must not end the task: err=%v reason=%s", err, result.Reason)
	}
	if !strings.Contains(s.executes[1], "UNUSABLE") {
		t.Errorf("round 2 must be told round 1's reply could not be used:\n%s", s.executes[1])
	}
	if !strings.Contains(s.executes[2], "proposed no actions") {
		t.Errorf("round 3 must be told round 2 proposed nothing to run:\n%s", s.executes[2])
	}
}

// TestUnusableRepliesInARowEndTheRun: a model that cannot follow the format at all is not
// retried for ever. The reason says what the replies were.
func TestUnusableRepliesInARowEndTheRun(t *testing.T) {
	s := &scriptServer{execute: func(int, string) string { return "not json at all" }}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err == nil || result.Pass {
		t.Fatal("a model that never produces a usable reply must fail the task")
	}
	if len(s.executes) != maxUnusableReplies {
		t.Errorf("execute calls = %d, want %d", len(s.executes), maxUnusableReplies)
	}
	if !strings.Contains(result.Reason, "could not obtain the action") {
		t.Errorf("reason = %q", result.Reason)
	}
}

// TestFinishingWithNothingLeftToRunIsAClaim: defect 3, the other half. "actions": [] with
// "done": true is how a finished task is reported; the anchor judges it.
func TestFinishingWithNothingLeftToRunIsAClaim(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(false, "echo work > work.txt")
		}
		return step(true)
	}}
	_, result, err := runScript(t, s, "test -s work.txt", nil)
	if err != nil || !result.Pass {
		t.Fatalf("an empty claim of done on finished work must pass: err=%v reason=%s", err, result.Reason)
	}
}

// TestARunThatRepeatsItselfIsWarnedThenStopped: defect 4. Identical rounds - same actions,
// same output - are warned about, and then the run stops with a reason that says why,
// long before the budget.
func TestARunThatRepeatsItselfIsWarnedThenStopped(t *testing.T) {
	s := &scriptServer{execute: func(int, string) string { return step(false, "echo same") }}
	_, result, err := runScript(t, s, "exit 0", func(c *config.Config) { c.Agent.MaxSteps = 50 })
	if err == nil || result.Pass {
		t.Fatal("a run going nowhere must end as a failure")
	}
	if !strings.Contains(result.Reason, "stalled") {
		t.Errorf("reason = %q, want it to say the run stalled", result.Reason)
	}
	if result.Attempts != stallStopAt+1 {
		t.Errorf("rounds = %d, want %d", result.Attempts, stallStopAt+1)
	}
	warned := false
	for _, p := range s.executes {
		if strings.Contains(p, "repeated the previous one exactly") {
			warned = true
		}
	}
	if !warned {
		t.Error("the model was never warned that it was repeating itself")
	}
}

// TestAPollWhoseOutputChangesIsNotAStall: the same command with a DIFFERENT result is
// progress being watched, not a stall.
func TestAPollWhoseOutputChangesIsNotAStall(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round <= stallStopAt+2 {
			return step(false, "echo x >> poll.txt; wc -l < poll.txt")
		}
		return step(true)
	}}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("a changing poll must not be stopped as a stall: err=%v reason=%s", err, result.Reason)
	}
}

// TestAProgressRoundThatHitAnErrorSaysSo: an action the loop refused in a progress round is
// not charged to the retries, and the next round is told the error.
func TestAProgressRoundThatHitAnErrorSaysSo(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return mustJSON(map[string]any{
				"reasoning": "wrong kind",
				"actions":   []map[string]string{{"kind": "read_file", "command": "/etc/hostname"}},
				"done":      false,
			})
		}
		return step(true, "true")
	}}
	_, result, err := runScript(t, s, "exit 0", func(c *config.Config) { c.Agent.MaxRetries = 0 })
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if !strings.Contains(s.executes[1], "Execution error") || !strings.Contains(s.executes[1], "[read_file]") {
		t.Errorf("round 2 must be told the error and the kind that caused it:\n%s", s.executes[1])
	}
}

// TestCancellingDuringTheActionPhaseSaysCancelled: a cancelled run is not reported as a
// model that could not answer.
func TestCancellingDuringTheActionPhaseSaysCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &scriptServer{execute: func(int, string) string {
		cancel()
		return "not json"
	}}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	result := e.agent.loop(ctx, taskOf("do it"), 0)
	if result.Pass || !strings.Contains(result.Reason, "cancelled") {
		t.Errorf("reason = %q, want a cancellation", result.Reason)
	}
}

// TestTheExecutePhaseSeesTheAnalysisAndTheRules: the phase that does the work is told what
// done means and what will judge it.
func TestTheExecutePhaseSeesTheAnalysisAndTheRules(t *testing.T) {
	s := &scriptServer{execute: func(int, string) string { return step(true, "true") }}
	runScript(t, s, "exit 0", nil)
	p := s.executes[0]
	for _, want := range []string{"Success criterion: it is done", "It will run: sh -c exit 0", "Round: 1"} {
		if !strings.Contains(p, want) {
			t.Errorf("the execute prompt is missing %q:\n%s", want, p)
		}
	}
}

// TestTheAnswerIsSynthesisedFromEveryRound: defect 6.
func TestTheAnswerIsSynthesisedFromEveryRound(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(false, "echo FOUND-IN-ROUND-ONE")
		}
		return step(true, "echo wrapped-up")
	}}
	_, result, _ := runScript(t, s, "exit 0", nil)
	if !result.Pass || len(s.synth) != 1 {
		t.Fatalf("pass=%v synth calls=%d", result.Pass, len(s.synth))
	}
	if !strings.Contains(s.synth[0], "FOUND-IN-ROUND-ONE") || !strings.Contains(s.synth[0], "wrapped-up") {
		t.Errorf("the synthesis must see every round's output:\n%s", s.synth[0])
	}
}

// --- Subtasks ----------------------------------------------------------------

// splitPlan answers the parent's plan with subtasks, and a subtask's plan with none.
func splitPlan(subs ...string) func(string) string {
	return func(prompt string) string {
		if strings.Contains(prompt, "This is subtask") {
			return `{"plan":[],"subtasks":[]}`
		}
		return mustJSON(map[string]any{"plan": []any{}, "subtasks": subs})
	}
}

// TestASubtaskIsToldWhatItIsPartOf: defect 5. The second subtask sees the request, its
// position, and what the first one did.
func TestASubtaskIsToldWhatItIsPartOf(t *testing.T) {
	s := &scriptServer{
		plan: splitPlan("create the model", "wire the endpoint"),
		execute: func(round int, _ string) string {
			return step(true, fmt.Sprintf("echo part-%d > part-%d.txt", round, round))
		},
	}
	_, result, err := runScript(t, s, "exit 0", func(c *config.Config) { c.Agent.SubtaskDepth = 1 })
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if result.Subtasks != 2 {
		t.Errorf("subtasks = %d, want 2", result.Subtasks)
	}
	second := s.executes[1]
	for _, want := range []string{"wire the endpoint", "subtask 2 of 2", "Overall request: do the test task",
		"[done] create the model", "[THIS ONE] wire the endpoint"} {
		if !strings.Contains(second, want) {
			t.Errorf("the second subtask's prompt is missing %q:\n%s", want, second)
		}
	}
	// The parent's answer is what the subtasks did, not a count.
	if !strings.Contains(result.Summary, "create the model") || !strings.Contains(result.Summary, "wire the endpoint") {
		t.Errorf("the parent summary must list what each subtask did: %q", result.Summary)
	}
	if result.Validation == nil {
		t.Error("the parent must carry the last subtask's validation")
	}
}

// TestASubtaskCannotChatOrAsk: defect 5. A subtask classified as chat, or one that stops to
// ask, proceeds as work - and the assumption it stated is reported.
func TestASubtaskCannotChatOrAsk(t *testing.T) {
	for _, tc := range []struct{ name, analysis string }{
		{"chat", `{"kind":"chat","reply":"sure, sounds good"}`},
		{"ask", `{"kind":"ask","understandable":false,"question":"which folder?","assumption":"the current folder"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &scriptServer{
				analyze: func(prompt string) string {
					if strings.Contains(prompt, "This is subtask") {
						return tc.analysis
					}
					return `{"kind":"task","understandable":true,"summary":"split it"}`
				},
				plan:    splitPlan("the only subtask"),
				execute: func(int, string) string { return step(true, "echo did-it > did.txt") },
			}
			e, result, err := runScript(t, s, "test -s did.txt", func(c *config.Config) { c.Agent.SubtaskDepth = 1 })
			if err != nil || !result.Pass {
				t.Fatalf("a subtask must do its work: err=%v reason=%s", err, result.Reason)
			}
			if len(s.executes) == 0 {
				t.Fatal("the subtask never reached the execute phase")
			}
			if result.NeedsInput {
				t.Error("a subtask's question must not surface as the parent waiting for input")
			}
			for _, turn := range e.agent.Transcript() {
				if strings.Contains(turn.User, "This is subtask") {
					t.Errorf("a subtask leaked into the conversation: %+v", turn)
				}
			}
		})
	}
}

// TestPlanWithOnlyBlankSubtasksRunsAsOneTask: blank subtasks are no subtasks, and the task
// runs itself instead of failing as "0 of 0".
func TestPlanWithOnlyBlankSubtasksRunsAsOneTask(t *testing.T) {
	s := &scriptServer{
		plan:    func(string) string { return `{"plan":[],"subtasks":["  ",""]}` },
		execute: func(int, string) string { return step(true, "true") },
	}
	_, result, err := runScript(t, s, "exit 0", func(c *config.Config) { c.Agent.SubtaskDepth = 1 })
	if err != nil || !result.Pass || result.Subtasks != 0 {
		t.Fatalf("err=%v pass=%v subtasks=%d reason=%s", err, result.Pass, result.Subtasks, result.Reason)
	}
}

// TestTheLastSubtaskFailingNamesNoSkipped: the reason does not invent skipped subtasks when
// the one that failed was the last.
func TestTheLastSubtaskFailingNamesNoSkipped(t *testing.T) {
	s := &scriptServer{
		plan: splitPlan("first", "second"),
		execute: func(round int, _ string) string {
			if round == 1 {
				return step(true, "echo ok > first.txt")
			}
			return step(true, "rm -f first.txt")
		},
	}
	_, result, _ := runScript(t, s, "test -s first.txt", func(c *config.Config) {
		c.Agent.SubtaskDepth = 1
		c.Agent.MaxRetries = 0
	})
	if result.Pass {
		t.Fatal("the second subtask fails, so the parent fails")
	}
	if !strings.Contains(result.Reason, "subtask 2 of 2") || strings.Contains(result.Reason, "not started") {
		t.Errorf("reason = %q", result.Reason)
	}
	if !strings.Contains(result.Summary, "first") {
		t.Errorf("the summary must still say what the first subtask did: %q", result.Summary)
	}
}

// TestCancellingBetweenSubtasks: a cancelled context stops the split before the next one.
func TestCancellingBetweenSubtasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &scriptServer{
		plan: splitPlan("first", "second"),
		execute: func(int, string) string {
			cancel()
			return step(true, "true")
		},
	}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) { c.Agent.SubtaskDepth = 1 })
	result := e.agent.loop(ctx, taskOf("do it"), 0)
	if result.Pass || !strings.Contains(result.Reason, "cancelled") {
		t.Errorf("reason = %q", result.Reason)
	}
}

// --- Helpers -------------------------------------------------------------------

// TestTheJournalCompactsOldRounds: the latest rounds are read in full, the older ones as one
// line each, so a long task cannot grow the prompt without bound.
func TestTheJournalCompactsOldRounds(t *testing.T) {
	if got := renderRounds(nil); !strings.Contains(got, "first round") {
		t.Errorf("empty journal = %q", got)
	}
	var rounds []roundRecord
	kinds := []roundKind{roundProgress, roundRejected, roundUnusable}
	for i := 1; i <= keepRoundsInFull+3; i++ {
		rounds = append(rounds, roundRecord{round: i, kind: kinds[i%3], commands: fmt.Sprintf("cmd-%d", i),
			detail: fmt.Sprintf("DETAIL-%d", i)})
	}
	got := renderRounds(rounds)
	if strings.Contains(got, "DETAIL-1\n") || !strings.Contains(got, "- Round 1 (rejected): cmd-1") {
		t.Errorf("the oldest round must be one line:\n%s", got)
	}
	if !strings.Contains(got, "- Round 2 (unusable reply): cmd-2") || !strings.Contains(got, "- Round 3 (progress): cmd-3") {
		t.Errorf("every kind has a one-line label:\n%s", got)
	}
	if !strings.Contains(got, fmt.Sprintf("DETAIL-%d", keepRoundsInFull+3)) {
		t.Errorf("the latest round must be in full:\n%s", got)
	}
}

// TestTheNewestRoundsKeepTheirWholeOutput replays "the previous output was not visible in my
// context": the round just run keeps its output, head AND tail (the end of a file, the summary line
// of a check); the older rounds still shown in full are cut, so ten of them stay affordable.
func TestTheNewestRoundsKeepTheirWholeOutput(t *testing.T) {
	big := "HEAD-MARK\n" + strings.Repeat("x", roundDetailOutput/2) + "\nTAIL-MARK"
	var rounds []roundRecord
	for i := 1; i <= keepRoundsInFull; i++ {
		rounds = append(rounds, roundRecord{round: i, kind: roundProgress, commands: "cmd", detail: big})
	}
	got := renderRounds(rounds)
	sections := strings.Split(got, "\n### Round ")
	newest, older := sections[len(sections)-1], sections[1]
	if !strings.Contains(newest, "HEAD-MARK") || !strings.Contains(newest, "TAIL-MARK") || strings.Contains(newest, "omitted") {
		t.Errorf("the newest round must be whole:\n%.300s", newest)
	}
	if !strings.Contains(older, "HEAD-MARK") || !strings.Contains(older, "TAIL-MARK") || !strings.Contains(older, "omitted") {
		t.Errorf("an older round must keep its head and tail around a cut:\n%.300s", older)
	}
	if len(older) > roundDetailOlder+200 {
		t.Errorf("an older round must be cut to about %d bytes, it is %d", roundDetailOlder, len(older))
	}
}

// TestTruncationNeverSplitsACharacter: a cut in the middle of a two-byte character used to put invalid UTF-8
// in prompts and in the chat.
func TestTruncationNeverSplitsACharacter(t *testing.T) {
	s := strings.Repeat("acci\u00f3n ", 50)
	for max := 1; max < len(s); max++ {
		if got := truncate(s, max); !utf8.ValidString(got) {
			t.Fatalf("truncate(_, %d) = invalid UTF-8", max)
		}
		if got := truncateMiddle(s, max); !utf8.ValidString(got) {
			t.Fatalf("truncateMiddle(_, %d) = invalid UTF-8", max)
		}
	}
	if got := truncateMiddle("short", 100); got != "short" {
		t.Errorf("a short text is kept whole: %q", got)
	}
	long := "HEAD" + strings.Repeat("-", 1000) + "TAIL"
	got := truncateMiddle(long, 90)
	if !strings.HasPrefix(got, "HEAD") || !strings.HasSuffix(got, "TAIL") {
		t.Errorf("both ends must survive: %q", got)
	}
	// A cut that does not say how much is missing reads as the whole answer.
	if !strings.Contains(got, fmt.Sprintf("%d of %d bytes omitted", 1004-len(got)+len("[...  of  bytes omitted ...]")-0, len(long))) &&
		!strings.Contains(got, "bytes omitted") {
		t.Errorf("the marker must say how much was dropped: %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("of %d bytes", len(long))) {
		t.Errorf("the marker must say how long the whole was: %q", got)
	}
}

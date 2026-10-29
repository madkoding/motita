package agent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/task"
)

const unmetAudit = `{"criteria":[{"criterion":"it is done","met":false,"evidence":"nothing printed shows it"}]}`

// TestAClaimWithAnUnmetCriterionIsSentBackOnce: the audit reads the claim against the success
// criteria, and the round after it is told which one is not shown.
func TestAClaimWithAnUnmetCriterionIsSentBackOnce(t *testing.T) {
	s := &scriptServer{
		execute: func(int, string) string { return step(true, "echo r > r.txt") },
	}
	calls := 0
	s.audit = func(string) string {
		calls++
		if calls == 1 {
			return unmetAudit
		}
		return `{"criteria":[{"criterion":"it is done","met":true,"evidence":"shown"}]}`
	}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if s.audits != 2 || len(s.executes) != 2 {
		t.Fatalf("audits=%d rounds=%d, want 2 and 2", s.audits, len(s.executes))
	}
	if !strings.Contains(s.executes[1], "not shown to be met") ||
		!strings.Contains(s.executes[1], "nothing printed shows it") {
		t.Errorf("the second round must be told what is missing:\n%s", s.executes[1])
	}
}

// TestTheAuditIsBoundedAndReadFromTheWholeRun: a run that cannot satisfy the audit still ends,
// and the audit sees the success criteria and the work log.
func TestTheAuditIsBoundedAndReadFromTheWholeRun(t *testing.T) {
	var seen string
	s := &scriptServer{
		execute: func(int, string) string { return step(true, "echo work-marker") },
		audit: func(prompt string) string {
			seen = prompt
			return unmetAudit
		},
	}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if s.audits != maxAuditChallenges || result.Attempts != maxAuditChallenges+1 {
		t.Errorf("audits=%d rounds=%d, want %d and %d", s.audits, result.Attempts,
			maxAuditChallenges, maxAuditChallenges+1)
	}
	if !strings.Contains(seen, "- it is done") || !strings.Contains(seen, "work-marker") {
		t.Errorf("the audit must see the criteria and the work:\n%s", seen)
	}
}

// TestAnAuditThatCannotBeReadDoesNotBlockTheRun: a broken audit takes the claim as it is, once.
func TestAnAuditThatCannotBeReadDoesNotBlockTheRun(t *testing.T) {
	s := &scriptServer{
		execute: func(int, string) string { return step(true, "true") },
		audit:   func(string) string { return "not json" },
	}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass || s.audits != 1 || result.Attempts != 1 {
		t.Fatalf("err=%v pass=%v audits=%d rounds=%d", err, result.Pass, s.audits, result.Attempts)
	}
}

// TestTheAuditIsSkippedWhenOffOrWithoutCriteria covers the two ways it is not spent.
func TestTheAuditIsSkippedWhenOffOrWithoutCriteria(t *testing.T) {
	off := &scriptServer{execute: func(int, string) string { return step(true, "true") }}
	if _, result, err := runScript(t, off, "exit 0",
		func(c *config.Config) { c.Agent.CriteriaAudit = false }); err != nil || !result.Pass || off.audits != 0 {
		t.Errorf("off: err=%v pass=%v audits=%d", err, result.Pass, off.audits)
	}
	none := &scriptServer{
		analyze: func(string) string { return `{"kind":"task","understandable":true,"summary":"x"}` },
		execute: func(int, string) string { return step(true, "true") },
	}
	if _, result, err := runScript(t, none, "exit 0", nil); err != nil || !result.Pass || none.audits != 0 {
		t.Errorf("no criteria: err=%v pass=%v audits=%d", err, result.Pass, none.audits)
	}
}

// TestAuditReplyIgnoresBlankAndMetCriteria is the reading of the model's JSON, without a server.
func TestAuditReplyIgnoresBlankAndMetCriteria(t *testing.T) {
	ar := auditReply{Criteria: []auditCriterion{
		{Criterion: "a", Met: true}, {Criterion: " ", Met: false}, {Criterion: "b", Evidence: " why "}, {Criterion: "c"},
	}}
	unmet := ar.unmet()
	if len(unmet) != 2 || unmet[0].Criterion != "b" {
		t.Fatalf("unmet = %+v", unmet)
	}
	note := auditChallenge(unmet)
	if !strings.Contains(note, "- b (why)") || !strings.Contains(note, "- c\n") {
		t.Errorf("challenge = %q", note)
	}
}

// TestAFailedSubtaskIsTriedAgainWithWhatFailed: the first failure no longer ends the parent.
func TestAFailedSubtaskIsTriedAgainWithWhatFailed(t *testing.T) {
	s := &scriptServer{
		plan: splitPlan("write the file"),
		execute: func(round int, _ string) string {
			if round == 1 {
				return step(true, "true") // the anchor wants ok.txt
			}
			return step(true, "echo ok > ok.txt")
		},
	}
	_, result, err := runScript(t, s, "test -s ok.txt", func(c *config.Config) {
		c.Agent.SubtaskDepth = 1
		c.Agent.MaxRetries = 0
	})
	if err != nil || !result.Pass {
		t.Fatalf("the retry must rescue the subtask: err=%v reason=%s", err, result.Reason)
	}
	if len(s.executes) != 2 || !strings.Contains(s.executes[1], "A previous attempt at THIS subtask failed") {
		t.Errorf("the retry must be told the last attempt failed (%d rounds):\n%v", len(s.executes), s.executes)
	}
}

// TestSubtaskRetriesAreBoundedAndCanBeTurnedOff.
func TestSubtaskRetriesAreBoundedAndCanBeTurnedOff(t *testing.T) {
	for retries, want := range map[int]int{0: 1, 2: 3} {
		s := &scriptServer{
			plan:    splitPlan("never works"),
			execute: func(int, string) string { return step(true, "true") },
		}
		_, result, _ := runScript(t, s, "exit 1", func(c *config.Config) {
			c.Agent.SubtaskDepth = 1
			c.Agent.MaxRetries = 0
			c.Agent.SubtaskRetries = retries
		})
		if result.Pass || len(s.executes) != want {
			t.Errorf("retries=%d: pass=%v attempts=%d, want %d", retries, result.Pass, len(s.executes), want)
		}
	}
}

// TestTheTurnThatAppliesSuggestionsNeverAsks: the user already said yes to the list, and asking
// what it meant sends them back to a decision they made.
func TestTheTurnThatAppliesSuggestionsNeverAsks(t *testing.T) {
	s := &scriptServer{
		analyze: func(string) string {
			return `{"kind":"ask","understandable":false,"question":"which one?","assumption":"all of them"}`
		},
		execute: func(int, string) string { return step(true, "echo applied > applied.txt") },
	}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "test -s applied.txt"},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, nil)
	e.agent.Interactive = true
	result := e.agent.processTask(context.Background(),
		task.Task{Description: ProposalLead + "\n- tidy the readme", Origin: "test"}, 0)
	if !result.Pass || result.NeedsInput {
		t.Fatalf("an accepted list must be applied, not asked about: pass=%v needs=%v reason=%s",
			result.Pass, result.NeedsInput, result.Reason)
	}
}

// TestASingleCriterionIsNotAudited: with one criterion the claim and the gate already say whether
// it is met, so the audit's model call and its possible extra round are not spent.
func TestASingleCriterionIsNotAudited(t *testing.T) {
	one := &scriptServer{
		analyze: func(string) string {
			return `{"kind":"task","understandable":true,"summary":"x","success_criteria":["it is done"]}`
		},
		execute: func(int, string) string { return step(true, "true") },
	}
	if _, result, err := runScript(t, one, "exit 0", nil); err != nil || !result.Pass || one.audits != 0 {
		t.Errorf("one criterion: err=%v pass=%v audits=%d", err, result.Pass, one.audits)
	}
}

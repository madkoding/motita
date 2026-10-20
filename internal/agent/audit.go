package agent

import (
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/llm"
)

// THE CRITERIA AUDIT: a claim of "done" read against what the request was said to need.
//
// The anchor runs what the PROJECT declares, so a criterion nobody tests (a behaviour the request
// asked for, a file no check opens) passes it untouched, and the user finds the gap afterwards in
// a second request. The analysis already wrote those criteria down; this puts them back in front
// of the run once, at the claim, while fixing them costs one more round instead of one more turn.
//
// It never decides on its own: an unmet criterion is SENT BACK like a failing check, bounded by
// maxAuditChallenges, and a reply that cannot be read counts as met - a broken audit must not
// become a reason the run cannot finish.

// maxAuditChallenges bounds how many times one run is sent back over its criteria.
const maxAuditChallenges = 2

// auditCriterion is one criterion and what the run shows about it.
type auditCriterion struct {
	Criterion string `json:"criterion"`
	Met       bool   `json:"met"`
	Evidence  string `json:"evidence"`
}

// auditReply is the audit as the model returns it.
type auditReply struct {
	Criteria []auditCriterion `json:"criteria"`
}

// unmet returns the criteria the audit says are not shown.
func (ar auditReply) unmet() []auditCriterion {
	var out []auditCriterion
	for _, c := range ar.Criteria {
		if !c.Met && strings.TrimSpace(c.Criterion) != "" {
			out = append(out, c)
		}
	}
	return out
}

// auditChallenge is the note a claim sent back over unmet criteria carries into the next round.
func auditChallenge(unmet []auditCriterion) string {
	var b strings.Builder
	b.WriteString("You reported \"done\": true, but these success criteria are not shown to be met " +
		"by anything the run printed or wrote:\n")
	for _, c := range unmet {
		fmt.Fprintf(&b, "- %s", strings.TrimSpace(c.Criterion))
		if e := strings.TrimSpace(c.Evidence); e != "" {
			fmt.Fprintf(&b, " (%s)", truncate(collapse(e), 300))
		}
		b.WriteString("\n")
	}
	b.WriteString("Do the work each one needs and PROVE it in this round: run the command that shows it " +
		"or open the file that holds it. If a criterion really does not apply, say why in \"notes\" and " +
		"report \"done\": true again.")
	return b.String()
}

// auditDue says the audit is still to be spent on this run.
func (r *taskRun) auditDue() bool {
	if !r.a.cfg.Agent.CriteriaAudit || r.auditPassed || r.auditChallenges >= maxAuditChallenges {
		return false
	}
	return len(r.analysis.SuccessCrit) > 0 && r.ctx.Err() == nil
}

// auditClaim reads a claim of "done" against the success criteria and reports whether it was sent
// back. It is spent at most maxAuditChallenges times per run, and not again once it passed.
func (r *taskRun) auditClaim(action Action) bool {
	a := r.a
	if !r.auditDue() {
		return false
	}
	vars := a.baseVariables(r.t)
	vars["criteria"] = "- " + strings.Join(r.analysis.SuccessCrit, "\n- ")
	output := r.workLog.String()
	if strings.TrimSpace(r.mem.notes) != "" {
		output += "## Notes the agent kept while working\n" + r.mem.notes + "\n"
	}
	// A check that could not run is put in front of the audit by name: a criterion about tests or a
	// build is not met by a run whose check never ran.
	output += unvalidatedBlock(r.mem.blockedChecks())
	vars["output"] = truncateMiddle(output, roundOutputChars)

	a.report("checking the result against what was asked...")
	text, err := a.ask(r.ctx, a.cfg.Prompts.Audit, vars, "audit")
	var reply auditReply
	if err == nil {
		err = llm.DecodeJSON(text, &reply)
	}
	if err != nil {
		a.log.Warn(r.prefix+"the criteria audit could not be read; taking the claim as it is", "error", err)
		r.auditPassed = true
		return false
	}
	unmet := reply.unmet()
	if len(unmet) == 0 {
		r.auditPassed = true
		return false
	}
	r.auditChallenges++
	a.report("done claimed with %d criteria not shown; sending the run back (%d/%d)",
		len(unmet), r.auditChallenges, maxAuditChallenges)
	a.log.Warn(r.prefix+"done claimed over unmet criteria", "round", r.round,
		"unmet", truncate(unmet[0].Criterion, 160), "challenge", r.auditChallenges)
	r.journal = append(r.journal, roundRecord{
		round: r.round, kind: roundProgress, commands: proposedCommands(action),
		detail: auditChallenge(unmet),
	})
	return true
}

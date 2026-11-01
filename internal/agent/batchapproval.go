package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/policy"
)

// ONE QUESTION FOR A ROUND. A round that proposes `npm install x`, `git push` and `curl …` used to
// stop three times, each question waiting for the person in turn, with the commands between them
// running only once the next answer came. The lines of a round that need approval are now asked
// about together, before the round runs: one card, every line on it, one answer. A yes approves
// each line by its exact text for this turn (see turnApprovals), a no declines each, and with
// nobody to ask each line meets the per-line question as before.

// BatchRule names a question about several lines of one round.
const BatchRule = "batch"

// BatchItem is one line of a question about several, with the rule it was asked under.
type BatchItem struct {
	Command string
	Rule    string
}

// Items are the lines a request asks about: its batch, or its one command.
func (r ApprovalRequest) Items() []BatchItem {
	if len(r.Batch) > 0 {
		return r.Batch
	}
	return []BatchItem{{Command: r.Command, Rule: r.Rule}}
}

// batchable says c is a shell line that would be asked about on its own: not another kind of action,
// not blank, not already approved this turn and not a fake check.
func (rr *roundRun) batchable(c Command) bool {
	kind := strings.ToLower(strings.TrimSpace(c.Kind))
	if kind != "" && kind != "command" {
		return false
	}
	line := strings.TrimSpace(c.Command)
	return line != "" && !rr.a.approvedInTurn.has(c.Command) && fakeCheckCommand(c.Command) == ""
}

// askRound asks once about every line of actions that would be asked about one by one, when there
// are at least two of them.
func (rr *roundRun) askRound(actions []Command) {
	a := rr.a
	var items []BatchItem
	var reasons []string
	for _, c := range actions {
		if !rr.batchable(c) {
			continue
		}
		plan := a.planRequest(c.Command)
		if plan.Verdict != policy.Ask {
			continue
		}
		items = append(items, BatchItem{Command: c.Command, Rule: plan.Rule})
		reasons = append(reasons, "- "+plan.Reason)
	}
	if len(items) < 2 || a.approver == nil {
		return
	}
	lines := make([]string, len(items))
	for i, it := range items {
		lines[i] = it.Command
	}
	a.report("asking you to approve %d commands of this round together", len(items))
	approved, err := a.approveBatch(rr, ApprovalRequest{
		Command: strings.Join(lines, "\n"),
		Reason:  fmt.Sprintf("%d commands of this round need your approval:\n%s", len(items), strings.Join(reasons, "\n")),
		Rule:    BatchRule,
		Batch:   items,
	})
	if err != nil {
		return
	}
	for _, it := range items {
		if approved {
			a.approvedInTurn.keep(it.Command)
		} else {
			rr.declined = append(rr.declined, strings.TrimSpace(it.Command))
		}
	}
}

// declinedInRound says the person declined line with the rest of the round.
func (rr *roundRun) declinedInRound(line string) bool {
	line = strings.TrimSpace(line)
	for _, d := range rr.declined {
		if d == line {
			return true
		}
	}
	return false
}

// approveBatch puts a question about several lines to the person, counted on the turn's clock.
func (a *Agent) approveBatch(rr *roundRun, req ApprovalRequest) (bool, error) {
	asked := time.Now()
	defer since(&a.clock.approvals, &a.clock.asked, asked)
	return a.approver(rr.ctx, req)
}

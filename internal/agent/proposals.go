package agent

import "strings"

// The answers every proposal offers, in the order the interface shows them.
//
// They are fixed rather than written by the model because the interface acts on them: "this one
// and the following" settles every proposal after it, and "none" settles every proposal left. A
// model-written label could not be told apart from a free answer.
const (
	// ProposalApply applies this proposal and asks about the next.
	ProposalApply = "Apply this one"
	// ProposalApplyRest applies this proposal and every one after it.
	ProposalApplyRest = "Apply this one and the following"
	// ProposalSkipAll applies none of the proposals still undecided.
	ProposalSkipAll = "Apply none"
)

// ProposalOptions are the answers of a proposal, in order.
var ProposalOptions = []string{ProposalApply, ProposalApplyRest, ProposalSkipAll}

// ProposalItems turns the suggestions of a finished task into the items the interface asks about.
//
// A report with no suggestions gives none, and nothing is asked: a task that found nothing beyond
// the request ends exactly as it always did.
func ProposalItems(rep *Report) []AskItem {
	if rep == nil || len(rep.Suggestions) == 0 {
		return nil
	}
	items := make([]AskItem, 0, len(rep.Suggestions))
	for _, s := range rep.Suggestions {
		items = append(items, AskItem{
			Text:     s.Title,
			Detail:   s.Detail,
			Options:  append([]string(nil), ProposalOptions...),
			Proposal: true,
			Needed:   s.Needed,
			Why:      s.WhyNeeded,
		})
	}
	return items
}

// askAllSuggestions turns every proposal back into the user's call, for a configuration that does
// not let auto mode apply any on its own (agent.auto_mode_ask_all_suggestions).
func (r *Report) askAllSuggestions() {
	for i := range r.Suggestions {
		r.Suggestions[i].Needed, r.Suggestions[i].WhyNeeded = false, ""
	}
}

// SplitNeeded separates the proposals auto mode applies on its own from the ones it still asks
// about, each in its original order. Items that are not proposals (questions about the request)
// are never needed: a question is asked because the answer is not obvious.
func SplitNeeded(items []AskItem) (needed, rest []AskItem) {
	for _, it := range items {
		if it.Proposal && it.Needed {
			needed = append(needed, it)
		} else {
			rest = append(rest, it)
		}
	}
	return needed, rest
}

// ProposalRequest writes the follow-up turn that applies the accepted proposals.
//
// lead is the sentence in front of the list, in the interface's language: it is a message the
// user sends, so the agent reads it in their language and answers in it. The list repeats each
// proposal whole, title and detail, because the turn that made them has ended and the next one
// has to know exactly what was accepted. With nothing accepted there is no turn: an empty string.
func ProposalRequest(lead string, accepted []AskItem) string {
	if len(accepted) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(lead))
	for _, it := range accepted {
		b.WriteString("\n- ")
		b.WriteString(it.Text)
		if it.Detail != "" {
			b.WriteString(": ")
			b.WriteString(it.Detail)
		}
	}
	return b.String()
}

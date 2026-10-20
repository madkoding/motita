package gateway

import (
	"strings"

	"github.com/madkoding/motita/internal/repeats"
)

// SKILLS FROM A SESSION THAT CLOSES.
//
// When a conversation is deleted or reset, what it did is read once for a procedure it repeated:
// the same run of commands in more than one turn. Only then is the skill review asked to write
// it down, and what it writes is a PROPOSAL waiting in the library for the person to accept or
// reject, like every proposal the background review makes. Nothing is installed by itself.

// SkillProposer is implemented by a service that can turn what a closed session repeated into
// proposed skills. It is optional: a service without it simply proposes nothing.
type SkillProposer interface {
	// ProposeSkills receives the repeated runs of commands as text, and blocks until the review is
	// done with them (or has decided not to run).
	ProposeSkills(summary string)
}

// learnFromClosing reads the closing conversation for repeated procedures and, if it finds any,
// hands them to the review in the background: the deletion does not wait for a model.
func (s *Server) learnFromClosing(c *conversation) {
	p, ok := c.svc.(SkillProposer)
	if !ok {
		return
	}
	summary := repeats.Summary(repeats.Detect(turnsOfCheckpoints(c.checkpointViews())))
	if summary == "" {
		return
	}
	s.learnWG.Go(func() { p.ProposeSkills(summary) })
}

// turnsOfCheckpoints is each turn's request and the commands it ran, in order.
func turnsOfCheckpoints(cps []checkpointView) []repeats.Turn {
	turns := make([]repeats.Turn, 0, len(cps))
	for _, cp := range cps {
		t := repeats.Turn{Ask: cp.Task}
		for _, st := range cp.Steps {
			if st.Kind == stepCommand {
				t.Steps = append(t.Steps, strings.TrimPrefix(st.Text, "running: "))
			}
		}
		turns = append(turns, t)
	}
	return turns
}

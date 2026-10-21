package policy

// Relaxing is what the mode does to a verdict, in one place.
//
// It is separate from DecisionFor because it is a property of the MODE and not of the
// command: a caller holding a verdict (one it cached, or one a rule produced) has to be
// able to apply the same policy without re-deriving it, and the mandatory floor has to
// survive that path too. The only way a mandatory Deny becomes runnable is if this function
// is wrong — which is exactly why it is this small.
func (m Mode) Relaxing(d Decision) Decision {
	if d.Mandatory {
		return d
	}
	if !m.Enforce && d.Verdict == Ask {
		return Decision{Allow, d.Reason + " (approved automatically: the policy is not enforced)", d.Rule, false}
	}
	if m.ApproveAll && d.Verdict == Ask {
		return Decision{Allow, d.Reason + " (approved automatically: allowed in the settings)", d.Rule, false}
	}
	return d
}

// Decide is DecisionFor followed by Relaxing, which is what a caller almost always wants.
func (m Mode) Decide(command string, args []string, dir string) Decision {
	return m.Relaxing(m.DecisionFor(command, args, dir))
}

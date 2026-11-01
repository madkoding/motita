package evals

// How the agent ran a check's tasks: the phased loop, or direct mode (agent.direct). A run made
// before the mode was recorded was phased.
const (
	ModePhased = "phased"
	ModeDirect = "direct"
)

// ModeOf is the mode a run was made in.
func ModeOf(r Run) string {
	if r.Mode == "" {
		return ModePhased
	}
	return r.Mode
}

// PreviousOf is the newest run before runs[i] made in the same mode, the one runs[i] is weighed
// against: a direct run judged against a phased one would read a change of mode as a regression.
func PreviousOf(runs []Run, i int) *Run {
	mode := ModeOf(runs[i])
	for j := i - 1; j >= 0; j-- {
		if ModeOf(runs[j]) == mode {
			return &runs[j]
		}
	}
	return nil
}

// Versus weighs the newest direct run against the newest phased one, on the same model and the
// same tasks: whether direct mode does better than the phased loop. Nil while either is missing.
type Versus struct {
	Phased  Summary `json:"phased"`
	Direct  Summary `json:"direct"`
	Verdict Verdict `json:"verdict"`
}

// CompareModes finds the newest run of each mode and compares them, or answers nil.
func CompareModes(runs []Run) *Versus {
	var phased, direct *Run
	for i := len(runs) - 1; i >= 0 && (phased == nil || direct == nil); i-- {
		r := &runs[i]
		switch {
		case ModeOf(*r) == ModeDirect && direct == nil:
			direct = r
		case ModeOf(*r) == ModePhased && phased == nil:
			phased = r
		default:
			// A newer run of this mode was already taken.
		}
	}
	if phased == nil || direct == nil || phased.CasesHash != direct.CasesHash || phased.Model != direct.Model {
		return nil
	}
	return &Versus{Phased: phased.Summary, Direct: direct.Summary, Verdict: Compare(phased, *direct)}
}

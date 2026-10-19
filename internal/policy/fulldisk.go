package policy

// What changes when the person let the agent reach the whole disk (Mode.FullDisk).
//
// The workspace is where a task works, and crossing it is a question because nobody said the agent
// may go further. With the whole disk allowed the person did say so, after a confirmation that
// spells out what it implies: a command that leaves the workspace, writes outside it or runs a
// script from elsewhere is the work they allowed, and asking about each one repeats a question
// that already has its answer. Two kinds of question stay: a path among the user's keys and logins
// (privatePath), which no choice about folders gives away, and every question that is not about
// where a command reaches.

// outsideRules are the questions asked only because a path is outside the workspace.
var outsideRules = map[string]bool{
	"leaves-workspace": true, "project-script-arg-outside": true, "script-outside-workspace": true,
	"write-outside-workspace": true,
}

// fullDiskDecision turns a question about a path outside the workspace into an Allow, when the
// whole disk is allowed and no word of the command names a path among the user's keys.
func (m Mode) fullDiskDecision(d Decision, args []string) Decision {
	if !m.FullDisk || d.Verdict != Ask || !outsideRules[d.Rule] || namesPrivate(args) {
		return d
	}
	return Decision{Allow, d.Reason + "; the whole disk is allowed (sandbox.disk_access: full)", "full-disk", false}
}

// namesPrivate reports whether a word names a path among the user's keys and logins.
func namesPrivate(args []string) bool {
	for _, a := range args {
		for _, p := range pathOperands(a) {
			if privatePath(p) {
				return true
			}
		}
	}
	return false
}

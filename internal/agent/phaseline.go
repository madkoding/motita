package agent

import "regexp"

// phaseLine matches the pipeline's own progress lines: the ones that say which phase the agent
// is in ("analysing the task...", "deciding action (round 2/8)...") rather than what it did.
// They are the live activity line and nothing more: filed in a turn's trail they bury the
// reads, writes, commands and thoughts between labels the reader did not ask for.
var phaseLine = regexp.MustCompile(`^(analysing the task\.\.\.|planning\.\.\.|plan ready: \d+ steps?|` +
	`deciding action \(round \d+/\d+\)\.\.\.|round \d+ done; |validating with anchor\.\.\.|` +
	`validation passed; |synthesizing answer\.\.\.|task complete: )`)

// IsPhaseLine says whether a progress line only names the phase the agent is in. Such a line
// belongs on the activity line while the run lasts, not among the steps a turn is kept with.
func IsPhaseLine(text string) bool {
	return phaseLine.MatchString(text)
}

package agent

import "strings"

// The progress lines that say a step is being tried again, or a claim of "done" is sent back.
// They are constants because a reader of the progress stream (the gateway's friction journal)
// recognises them by these words: a line reworded in one place and not the other would stop
// being counted.
const (
	// attemptFailedPrefix starts the line of an attempt that did not pass validation.
	attemptFailedPrefix = "attempt failed: "
	// subtaskRetryMarker is inside the line of a subtask that is run again.
	subtaskRetryMarker = "did not pass; trying again"
	// directGatePrefix starts the line of a direct run whose gate is handed back.
	directGatePrefix = "the project's checks failed; handing them back"
	// unchangedPrefix, failingCheckPrefix, criteriaPrefix and childrenPrefix start the lines of
	// a claim of "done" sent back before the gate: over an unchanged tree, over a check the run
	// saw fail, over success criteria never shown, with background agents still running.
	unchangedPrefix    = "done claimed but nothing has been changed yet"
	failingCheckPrefix = "done claimed while a check is still failing"
	criteriaPrefix     = "done claimed with "
	childrenPrefix     = "done claimed while "
)

// The layers that send a step back, as the friction journal names them.
const (
	LayerGate         = "gate"
	LayerSubtask      = "subtask"
	LayerUnchanged    = "unchanged"
	LayerFailingCheck = "failing-check"
	LayerCriteria     = "criteria"
	LayerChildren     = "background"
)

// RetryLayer names the layer a progress line reports sending a step back, or "" when the line is
// not one. The order matters: the failing-check line also starts like a background-agents one.
func RetryLayer(line string) string {
	switch {
	case strings.HasPrefix(line, attemptFailedPrefix), strings.HasPrefix(line, directGatePrefix):
		return LayerGate
	case strings.Contains(line, subtaskRetryMarker):
		return LayerSubtask
	case strings.HasPrefix(line, unchangedPrefix):
		return LayerUnchanged
	case strings.HasPrefix(line, failingCheckPrefix):
		return LayerFailingCheck
	case strings.HasPrefix(line, criteriaPrefix):
		return LayerCriteria
	case strings.HasPrefix(line, childrenPrefix) && strings.Contains(line, "background agent"):
		return LayerChildren
	default:
		return ""
	}
}

// IsRetryLine says a progress line reports a step that did not pass and is being tried again.
func IsRetryLine(line string) bool { return RetryLayer(line) != "" }

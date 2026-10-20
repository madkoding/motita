package agent

import "strings"

// The two progress lines that say a step is being tried again. They are constants because a
// reader of the progress stream (the gateway's friction journal) recognises them by these words:
// a line reworded in one place and not the other would stop being counted.
const (
	// attemptFailedPrefix starts the line of an attempt that did not pass validation.
	attemptFailedPrefix = "attempt failed: "
	// subtaskRetryMarker is inside the line of a subtask that is run again.
	subtaskRetryMarker = "did not pass; trying again"
)

// IsRetryLine says a progress line reports a step that did not pass and is being tried again.
func IsRetryLine(line string) bool {
	return strings.HasPrefix(line, attemptFailedPrefix) || strings.Contains(line, subtaskRetryMarker)
}

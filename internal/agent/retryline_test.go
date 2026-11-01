package agent

import "testing"

func TestIsRetryLine(t *testing.T) {
	for line, want := range map[string]bool{
		"attempt failed: tests did not pass":                       true,
		"subtask 2/3 did not pass; trying again (1/2): lint error": true,
		"running go test":                      false,
		"the attempt failed: not at the start": false,
		"":                                     false,
	} {
		if got := IsRetryLine(line); got != want {
			t.Errorf("IsRetryLine(%q) = %v, want %v", line, got, want)
		}
	}
}

// Each layer that sends a claim of "done" back is named by the line it reports.
func TestRetryLayer(t *testing.T) {
	for line, want := range map[string]string{
		attemptFailedPrefix + "tests":                                  LayerGate,
		directGatePrefix + " (1/2)":                                    LayerGate,
		"subtask 1/2 " + subtaskRetryMarker + " (1/1): x":              LayerSubtask,
		unchangedPrefix + "; asking the model to confirm":              LayerUnchanged,
		failingCheckPrefix + "; sending the run back to it (1/4)":      LayerFailingCheck,
		criteriaPrefix + "2 criteria not shown; sending the run back":  LayerCriteria,
		childrenPrefix + "1 background agent(s) are still running; ok": LayerChildren,
		childrenPrefix + "the moon is up":                              "",
		"running go test":                                              "",
	} {
		if got := RetryLayer(line); got != want {
			t.Errorf("RetryLayer(%q) = %q, want %q", line, got, want)
		}
	}
}

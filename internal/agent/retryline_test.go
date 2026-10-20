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

package agent

import (
	"strings"
	"testing"
)

func TestDescribeSecretsListsNamesOnly(t *testing.T) {
	if describeSecrets(nil) != "" {
		t.Error("no variables, no section")
	}
	got := describeSecrets([]string{"A_KEY", "B_KEY"})
	if !strings.Contains(got, "A_KEY, B_KEY") || !strings.Contains(got, "Never print") {
		t.Errorf("got %q", got)
	}
}

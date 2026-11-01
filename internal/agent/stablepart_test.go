package agent

import (
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/template"
)

// TestTheRoundsMarkTheirStableStart: an execution round marks the instructions before the task as
// the part every round shares, which is most of the prompt; other phases mark nothing, and a
// prefix that does not render to the start of the prompt is not marked.
func TestTheRoundsMarkTheirStableStart(t *testing.T) {
	vars := map[string]string{"tools": "TOOLS", "rules": "RULES", "desktop": "", "task": "the task",
		"workspace": "/w", "analysis": "", "plan": "", "dialogue": "", "recall": "", "project": "",
		"attempt": "3", "history": "H"}
	tpl := config.BaseExecuteTemplate.User
	user, _ := template.Render(tpl, vars)
	n := stablePart("execute", tpl, vars, user)
	if n == 0 || n > len(user) || user[n:] == "" {
		t.Fatalf("the round must mark a stable start shorter than the prompt: %d of %d", n, len(user))
	}
	if n < len(user)/2 {
		t.Errorf("the instructions are most of the prompt: only %d of %d bytes marked", n, len(user))
	}
	if stablePart("analyze", tpl, vars, user) != 0 {
		t.Error("a phase that runs once must not be marked")
	}
	if stablePart("execute", "{{tools}} x {{task}}", vars, "something else") != 0 {
		t.Error("a prefix that is not the start of the prompt must not be marked")
	}
}

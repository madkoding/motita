package policy

import (
	"strings"
	"testing"
)

// Every commit the agent makes on the user's behalf is a semantic one. The rule is a refusal
// that teaches: the reason carries the form to write.
func TestCommitMessagesMustBeSemantic(t *testing.T) {
	m := Mode{Enforce: true}
	deny := [][]string{
		{"commit", "-m", "update stuff"},
		{"commit", "-am", "Fixed the bug"},
		{"commit", "--message=wip"},
		{"commit", "-m", "feat add x"},
		{"-c", "user.name=x", "--no-pager", "commit", "-m", "nope"},
		{"-C", ".", "commit", "-m", "nope"},
	}
	for _, args := range deny {
		d := m.Decide("git", args, "/work")
		if d.Verdict != Deny || d.Rule != "semantic-commit" || !strings.Contains(d.Reason, "type(scope): description") {
			t.Errorf("git %v: %+v", args, d)
		}
	}
	allow := [][]string{
		{"commit", "-m", "feat(auth): refresh the token"},
		{"commit", "-am", "fix: a crash"},
		{"commit", "-m", "chore(deps)!: drop the old client"},
		{"commit", "-F", "msg.txt"},
		{"commit", "--amend", "--no-edit"},
		{"commit"},
		{"status"},
		{"log", "--oneline"},
		{"--version"},
		{},
	}
	for _, args := range allow {
		if d := m.Decide("git", args, "/work"); d.Rule == "semantic-commit" {
			t.Errorf("git %v must not be judged: %+v", args, d)
		}
	}
	// Another program is never judged by it.
	if d := m.Decide("echo", []string{"commit", "-m", "x"}, "/work"); d.Rule == "semantic-commit" {
		t.Errorf("echo: %+v", d)
	}
	// And the refusal holds with the policy relaxed: it is not a question that "approve
	// everything" can answer.
	if d := (Mode{}).Decide("git", []string{"commit", "-m", "bad"}, "/work"); d.Verdict != Deny {
		t.Errorf("relaxed: %+v", d)
	}
	// Through a whole line, too.
	if d := m.DecideLine(`git add -A && git commit -m "update"`, "/work"); d.Verdict != Deny {
		t.Errorf("line: %+v", d)
	}
}

func TestForgeCommands(t *testing.T) {
	m := Mode{Enforce: true}
	if d := m.Decide("motita", []string{"forge", "pr", "create", "--title", "feat: x"}, "/work"); d.Verdict != Ask || d.Rule != "external-effect" {
		t.Errorf("pr create: %+v", d)
	}
	for _, args := range [][]string{
		{"forge", "pr", "checks", "--wait"},
		{"forge", "pr", "status"},
		{"forge", "status"},
		{"forge"},
		{"forge", "pr"},
	} {
		if d := m.Decide("motita", args, "/work"); d.Verdict != Allow || d.Rule != "reader" {
			t.Errorf("motita %v: %+v", args, d)
		}
	}
	// Other motita commands are not this rule's business.
	for _, args := range [][]string{nil, {"gateway", "start"}} {
		if _, hit := forgeDecision("motita", args); hit {
			t.Errorf("motita %v must fall through", args)
		}
	}
	if _, hit := forgeDecision("git", []string{"forge"}); hit {
		t.Error("only motita has a forge command")
	}
	// With the policy relaxed, opening a PR is approved automatically like any other Ask.
	if d := (Mode{}).Decide("motita", []string{"forge", "pr", "create"}, "/work"); d.Verdict != Allow {
		t.Errorf("relaxed: %+v", d)
	}
}

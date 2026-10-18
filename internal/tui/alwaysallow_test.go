package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/i18n"
)

// withApprovalsFile keeps the terminal's remembered decisions in a file of this test's own.
func withApprovalsFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "approvals.json")
	old := approvalsPath
	approvalsPath = func() string { return path }
	t.Cleanup(func() { approvalsPath = old })
	return path
}

// askAndAnswer hands req to the TUI as the agent would, lets the loop answer it, and returns the
// answer the agent got.
func askAndAnswer(t *testing.T, tui *TUI, req agent.ApprovalRequest) bool {
	t.Helper()
	ctx := context.Background()
	approved := make(chan bool, 1)
	go func() {
		ok, _ := tui.askConfirm(ctx, req)
		approved <- ok
	}()
	tui.answerConfirm(ctx, <-tui.approvalChannel())
	return <-approved
}

func transcript(tui *TUI) string {
	var sb strings.Builder
	for _, m := range tui.messages {
		sb.WriteString(m.Text + "\n")
	}
	return sb.String()
}

// TestAlwaysAllowHereAnswersTheSameShapeWithoutAsking: "a" says yes and keeps the shape of the
// command (its rule and program) allowed in this folder: the next command of that shape, in this
// session or a later one, runs without a window and the transcript says so; another shape still
// asks. The decision is written to a file, so it survives the process.
func TestAlwaysAllowHereAnswersTheSameShapeWithoutAsking(t *testing.T) {
	path := withApprovalsFile(t)
	tui, _, _ := confirmTUI(t, "a\n", "git push")
	push := agent.ApprovalRequest{Command: "git push origin main", Reason: "pushes", Rule: "shell"}
	if !askAndAnswer(t, tui, push) {
		t.Fatal("a is a yes")
	}
	if !strings.Contains(transcript(tui), "always allowed here from now on] git push origin main") {
		t.Fatalf("the transcript must say the shape is now allowed:\n%s", transcript(tui))
	}
	// The same shape: no key is read (the input is spent), and the answer is yes.
	tags := agent.ApprovalRequest{Command: "git push --tags", Rule: "shell"}
	if !askAndAnswer(t, tui, tags) || tui.answeringConfirm() {
		t.Fatal("the same shape runs without a window")
	}
	if !strings.Contains(transcript(tui), "ran without asking] git push --tags") {
		t.Fatalf("the transcript must say it ran without asking:\n%s", transcript(tui))
	}
	// Another shape is asked about; with nothing left to read it is refused.
	if askAndAnswer(t, tui, agent.ApprovalRequest{Command: "npm test", Rule: "shell"}) {
		t.Fatal("another shape still asks")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the decision must be written: %v", err)
	}
	// A later process over the same folder reads the file.
	later, _, _ := confirmTUI(t, "", "git push")
	if !later.alwaysAllowed(push) {
		t.Error("the decision must survive the process")
	}
	// In Spanish, with the hint naming the key.
	later.Lang = i18n.ES
	later.confirm = &confirmState{req: push, reply: make(chan bool, 1)}
	window := strings.Join(later.confirmLines(0), "\n")
	if !strings.Contains(window, "a = permitir siempre aquí") { // spanish-fixture: the Spanish window
		t.Errorf("window:\n%s", window)
	}
}

// A line with no shape to remember (no rule, or no program) is approved once, as a plain yes, and
// nothing is kept; a file that cannot be read starts empty rather than failing every command.
func TestAlwaysAllowWithNothingToRememberIsAPlainYes(t *testing.T) {
	path := withApprovalsFile(t)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	tui, _, _ := confirmTUI(t, "a\n", "rm -rf build")
	if !askAndAnswer(t, tui, agent.ApprovalRequest{Command: "rm -rf build"}) {
		t.Fatal("a is a yes even with nothing to remember")
	}
	if got := lastMessage(tui); got != "[approved by the user] rm -rf build" {
		t.Errorf("recorded %q, want a plain yes", got)
	}
	if tui.alwaysAllowed(agent.ApprovalRequest{Command: "rm -rf build", Rule: "shell"}) {
		t.Error("nothing was remembered")
	}
}

// The file lives in the motita home; with no home at all the decisions last for the process.
func TestTheApprovalsFileLivesInTheMotitaHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got, want := approvalsPath(), filepath.Join(os.Getenv("HOME"), ".motita", "approvals.json"); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if got := approvalsPath(); got != "" {
		t.Errorf("without a home the path is empty, got %q", got)
	}
}

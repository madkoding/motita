package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/execx"
)

// roundAsking is an agent whose approver records each question and answers with answer/err.
func roundAsking(t *testing.T, answer bool, err error) (*Agent, *[]ApprovalRequest, *int) {
	t.Helper()
	a := agentWith(t, false)
	ran := 0
	a.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		ran++
		return "ok", false, 0, nil
	}
	var asked []ApprovalRequest
	a.SetApprover(func(_ context.Context, req ApprovalRequest) (bool, error) {
		asked = append(asked, req)
		return answer, err
	})
	return a, &asked, &ran
}

// outsideLines are two lines the policy asks about, and one it does not.
func outsideLines(a *Agent) []Command {
	ws := a.cfg.Agent.WorkspaceDir
	return []Command{{Command: "rm -rf " + ws + "/../one"}, {Command: "ls"}, {Command: "rm -rf " + ws + "/../two"}}
}

// TestTheLinesOfARoundAreAskedAboutTogether: two lines that need approval are one question with
// both on it; a yes runs both, a no runs neither, and neither is asked about again.
func TestTheLinesOfARoundAreAskedAboutTogether(t *testing.T) {
	a, asked, ran := roundAsking(t, true, nil)
	if _, err := a.runActions(context.Background(), outsideLines(a), ""); err != nil {
		t.Fatalf("an approved round must run: %v", err)
	}
	if len(*asked) != 1 || (*asked)[0].Rule != BatchRule || len((*asked)[0].Items()) != 2 ||
		strings.Count((*asked)[0].Command, "\n") != 1 {
		t.Fatalf("one question must carry both lines: %+v", *asked)
	}
	if *ran != 3 {
		t.Errorf("every line must run after the yes: %d", *ran)
	}

	a, asked, ran = roundAsking(t, false, nil)
	out, _ := a.runActions(context.Background(), outsideLines(a), "")
	if len(*asked) != 1 || *ran != 1 || strings.Count(out, "declined") != 2 {
		t.Errorf("a no must decline both lines with one question: asked %d, ran %d\n%s", len(*asked), *ran, out)
	}
}

// TestARoundWithOneQuestionOrNobodyAsksAsBefore: a single line is asked about alone, and when the
// batch cannot be asked (no approver, or it fails) each line meets its own question.
func TestARoundWithOneQuestionOrNobodyAsksAsBefore(t *testing.T) {
	a, asked, _ := roundAsking(t, true, nil)
	_, _ = a.runActions(context.Background(), outsideLines(a)[:2], "")
	if len(*asked) != 1 || (*asked)[0].Rule == BatchRule || len((*asked)[0].Items()) != 1 {
		t.Errorf("one line is its own question: %+v", *asked)
	}

	a, asked, _ = roundAsking(t, false, errors.New("nobody"))
	_, _ = a.runActions(context.Background(), outsideLines(a), "")
	if len(*asked) != 3 {
		t.Errorf("a failed batch must fall back to one question per line: %d", len(*asked))
	}

	a = agentWith(t, false)
	a.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) { return "", false, 0, nil }
	if _, err := a.runActions(context.Background(), outsideLines(a), ""); err == nil ||
		!strings.Contains(err.Error(), "nobody to ask") {
		t.Errorf("with no approver every line is refused as before: %v", err)
	}
}

// TestABatchSkipsWhatIsNotAQuestion: actions of another kind, blank lines, lines already approved
// and fabricated checks are not put on the card.
func TestABatchSkipsWhatIsNotAQuestion(t *testing.T) {
	a, asked, _ := roundAsking(t, true, nil)
	ws := a.cfg.Agent.WorkspaceDir
	a.approvedInTurn.keep("rm -rf " + ws + "/../kept")
	rr := &roundRun{a: a, ctx: context.Background()}
	rr.askRound([]Command{
		{Kind: "write_file", Command: "x"}, {Command: " "}, {Command: "rm -rf " + ws + "/../kept"},
		{Command: `printf '{"scripts":{"test":"exit 0"}}' > package.json`}, {Command: "rm -rf " + ws + "/../new"},
	})
	if len(*asked) != 0 {
		t.Errorf("only one line was a question, so no batch was asked: %+v", *asked)
	}
}

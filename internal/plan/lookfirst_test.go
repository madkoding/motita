package plan

import (
	"context"
	"testing"

	"github.com/madkoding/motita/internal/llm"
)

const cleanup = "Remove it with git worktree remove --force ~/.motita/workspace/worktrees/s4bd"

// TestACleanupProposedWithoutLookingIsSentBackOnce replays the session that claimed which
// repository registered a worktree without running a command: the first answer is not delivered,
// the model looks, and the answer after that is.
func TestACleanupProposedWithoutLookingIsSentBackOnce(t *testing.T) {
	a, fake := makeAgent(t, true)
	fake.defaultOutput = "/home/x/repo  abc [main]\n"
	srv := llmServer(t, []replyStep{
		{content: cleanup},
		{finishReason: "tool_calls", calls: []map[string]any{
			toolCall("execute_command", "c1", map[string]string{"command": "git worktree list"})}},
		{content: "checked: it is an independent repository. " + cleanup},
	})
	defer srv.Close()
	out, err := New(newClient(t, srv), a).Run(context.Background(), "propose how to clean the session")
	if err != nil {
		t.Fatal(err)
	}
	if out != "checked: it is an independent repository. "+cleanup {
		t.Errorf("the delivered answer must be the one after looking: %q", out)
	}
	if len(fake.calls) != 1 {
		t.Errorf("the model must have looked: %d commands", len(fake.calls))
	}
}

// TestACleanupAfterLookingIsDelivered: a turn that already observed the repository is not slowed.
func TestACleanupAfterLookingIsDelivered(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{
			toolCall("execute_command", "c1", map[string]string{"command": "git -C /x rev-parse --show-toplevel"})}},
		{content: cleanup},
		{content: "never reached"},
	})
	defer srv.Close()
	out, err := New(newClient(t, srv), a).Run(context.Background(), "clean up")
	if err != nil || out != cleanup {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

// TestTheTurnLookIsBounded covers the rest of the rule on its own.
func TestTheTurnLookIsBounded(t *testing.T) {
	var turn turnLook
	if turn.sendBack("the plan is to read the manual") {
		t.Error("an answer that deletes nothing is never sent back")
	}
	if !turn.sendBack(cleanup) {
		t.Error("the first destructive answer without a look is sent back")
	}
	if turn.sendBack(cleanup) {
		t.Error("it is sent back once per turn")
	}
	var seen turnLook
	seen.note([]llm.ToolCall{{Function: llm.FunctionCall{Name: "read_file", Arguments: []byte(`{}`)}}})
	echo := llm.FunctionCall{Name: "execute_command", Arguments: []byte(`{"command":"echo"}`)}
	seen.note([]llm.ToolCall{{Function: echo}})
	if seen.looked {
		t.Error("reading a file or echoing is not looking at the repository")
	}
	seen.note([]llm.ToolCall{{Function: llm.FunctionCall{Name: "list_directory", Arguments: []byte(`{}`)}}})
	if !seen.looked || seen.sendBack(cleanup) {
		t.Error("listing a directory counts as looking")
	}
}

package tui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// newContextRunner builds a runner whose HOME is its own, so an archive opened by a test never
// lands in the real ~/.motita.
func newContextRunner(t *testing.T, cfg config.Config) *AppRunner {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
}

// TestSetContextIDCarriesTheArchiveWithTheConversation: the id names the folder, so binding a
// served conversation to its id is what lets it find its history after a restart — and what
// deletes the placeholder archive nobody can return to.
func TestSetContextIDCarriesTheArchiveWithTheConversation(t *testing.T) {
	r := newContextRunner(t, config.Default())
	r.contextID = "placeholder"

	r.SetContextID("   ")
	if r.contextID != "placeholder" {
		t.Errorf("an empty id replaced %q", r.contextID)
	}
	r.SetContextID("placeholder")
	if r.context != nil {
		t.Error("rebinding to the same id opened an archive")
	}

	r.SetContextID("conversation-1")
	store := r.contextStore()
	if store == nil {
		t.Fatal("the archive was not opened")
	}
	old := store.Root()
	r.RestoreTranscript([]agent.DialogueTurn{{Kind: agent.KindChat, User: "hello", Agent: "hi"}})
	r.syncContext()

	r.SetContextID("conversation-2")
	if r.context != nil {
		t.Error("the previous archive survived the rebinding")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the placeholder archive is still on disk: %v", err)
	}
	if got := r.contextStore().Root(); got == old || !strings.HasSuffix(got, "conversation-2") {
		t.Errorf("the new archive is %q", got)
	}
}

func TestContextStoreIsOffWithoutTheFeatureOrAValidID(t *testing.T) {
	cfg := config.Default()
	cfg.ContextMemory.Enabled = false
	if store := newContextRunner(t, cfg).contextStore(); store != nil {
		t.Error("a disabled archive was opened")
	}

	r := newContextRunner(t, config.Default())
	r.contextID = ""
	if store := r.contextStore(); store != nil {
		t.Error("an archive was opened for a conversation with no id")
	}
}

func TestContextStoreReportsAFolderItCannotUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".motita"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The archive's PARENT is a file: the folder cannot be created under it.
	if err := os.WriteFile(filepath.Join(home, ".motita", "context"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), nil, nil, logx.Global())
	r.SetContextID("conversation")
	if store := r.contextStore(); store != nil {
		t.Errorf("an unusable folder produced an archive at %q", store.Root())
	}
}

func TestResetContextDeletesTheArchive(t *testing.T) {
	r := newContextRunner(t, config.Default())
	r.resetContext() // nothing open: nothing to do

	store := r.contextStore()
	if store == nil {
		t.Fatal("the archive was not opened")
	}
	root := store.Root()
	r.resetContext()
	if r.context != nil {
		t.Error("the runner kept the archive it deleted")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the archive survived ResetConversation: %v", err)
	}
}

func TestResetContextReportsAFolderItCannotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission that makes the removal fail")
	}
	r := newContextRunner(t, config.Default())
	store := r.contextStore()
	if store == nil {
		t.Fatal("the archive was not opened")
	}
	parent := filepath.Dir(store.Root())
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	r.resetContext() // the failure is logged; the runner still forgets the archive
	if r.context != nil {
		t.Error("a folder that could not be deleted was kept open")
	}
}

func TestResetConversationForgetsTheArchive(t *testing.T) {
	r := newContextRunner(t, config.Default())
	store := r.contextStore()
	if store == nil {
		t.Fatal("the archive was not opened")
	}
	root := store.Root()
	r.ResetConversation()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("a new conversation can still recall the last one: %v", err)
	}
}

func TestSyncContextArchivesTheTurnAndItsReport(t *testing.T) {
	cfg := config.Default()
	cfg.ContextMemory.Enabled = false
	off := newContextRunner(t, cfg)
	off.RestoreTranscript([]agent.DialogueTurn{{Kind: agent.KindTask, User: "a task", Agent: "done"}})
	off.syncContext() // disabled: nothing is written and nothing fails

	r := newContextRunner(t, config.Default())
	r.SetContextID("conversation")
	store := r.contextStore()
	if store == nil {
		t.Fatal("the archive was not opened")
	}
	r.RestoreTranscript([]agent.DialogueTurn{{Kind: agent.KindTask, User: "the payroll fix", Agent: "it is green"}})
	r.setReport(&agent.Report{Summary: "the pipeline is green"})
	r.syncContext()

	body, err := os.ReadFile(filepath.Join(store.Root(), "turn-000001-task.md"))
	if err != nil {
		t.Fatalf("the turn was not archived: %v", err)
	}
	text := string(body)
	for _, want := range []string{"the payroll fix", "it is green", "the pipeline is green"} {
		if !strings.Contains(text, want) {
			t.Errorf("the archive does not contain %q:\n%s", want, text)
		}
	}
}

func TestSyncContextReportsAWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission that makes the write fail")
	}
	r := newContextRunner(t, config.Default())
	r.SetContextID("conversation")
	store := r.contextStore()
	if store == nil {
		t.Fatal("the archive was not opened")
	}
	r.RestoreTranscript([]agent.DialogueTurn{{Kind: agent.KindChat, User: "one", Agent: "a"}})
	r.syncContext()

	if err := os.Chmod(store.Root(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store.Root(), 0o700) })
	r.RestoreTranscript([]agent.DialogueTurn{
		{Kind: agent.KindChat, User: "one", Agent: "a"},
		{Kind: agent.KindChat, User: "two", Agent: "b"},
	})
	r.syncContext() // must not panic: the failure is logged, the turn is lost
	if _, err := os.Stat(filepath.Join(store.Root(), "turn-000002-chat.md")); !os.IsNotExist(err) {
		t.Errorf("a turn was written into a folder that cannot be written: %v", err)
	}
}

// TestRecallAsksTheArchive: Task mode leaves the last DialogueTurns in the prompt, Plan mode
// says how many it still shows. The previous request is part of the query because a short
// "and the other one?" means nothing without it.
func TestRecallAsksTheArchive(t *testing.T) {
	disabled := config.Default()
	disabled.ContextMemory.Enabled = false
	r := newContextRunner(t, disabled)
	if block, n := r.recall(context.Background(), "anything", 0); n != 0 || block != "" {
		t.Errorf("a disabled archive recalled %q", block)
	}

	r = newContextRunner(t, config.Default())
	r.SetContextID("conversation")
	store := r.contextStore()
	if store == nil {
		t.Fatal("the archive was not opened")
	}
	if block, n := r.recall(context.Background(), "payroll", 0); n != 0 || block != "" {
		t.Errorf("an empty archive recalled %q", block)
	}

	// Enough turns that the oldest is past the tail Task mode carries verbatim.
	var turns []agent.DialogueTurn
	for i := 0; i < agent.DialogueTurns+1; i++ {
		turns = append(turns, agent.DialogueTurn{
			Kind: agent.KindChat, User: "the payroll question", Agent: "the payroll answer",
		})
	}
	r.RestoreTranscript(turns)
	r.syncContext()

	block, n := r.recallTask(context.Background(), "payroll", agent.DialogueTurns-1)
	if n == 0 {
		t.Fatal("Task mode recalled nothing from past its own window")
	}
	if !strings.Contains(block, "payroll") {
		t.Errorf("the recalled block does not answer the question:\n%s", block)
	}
	// Everything is still shown verbatim: the recall has nothing left to offer.
	if block, n := r.recallTask(context.Background(), "payroll", len(turns)); n != 0 || block != "" {
		t.Errorf("a recall that excludes every turn brought %q", block)
	}

	planBlock, planN := r.recallPlan(context.Background(), "payroll", 0)
	if planN == 0 {
		t.Error("Plan mode recalled nothing from an archive that has turns")
	}
	if !strings.Contains(planBlock, "payroll") {
		t.Errorf("the plan block does not answer the question:\n%s", planBlock)
	}
}

func TestLastUserTurnIsTheMostRecentQuestion(t *testing.T) {
	r := newContextRunner(t, config.Default())
	if got := r.lastUserTurn(); got != "" {
		t.Errorf("a conversation with no turns has a last question: %q", got)
	}
	r.RestoreTranscript([]agent.DialogueTurn{
		{Kind: agent.KindChat, User: "the first", Agent: "a"},
		{Kind: agent.KindChat, User: "  ", Agent: "b"},
		{Kind: agent.KindChat, User: "the second", Agent: "c"},
	})
	if got := r.lastUserTurn(); got != "the second" {
		t.Errorf("lastUserTurn = %q", got)
	}
	r.RestoreTranscript([]agent.DialogueTurn{{Kind: agent.KindChat, User: "", Agent: "only an answer"}})
	if got := r.lastUserTurn(); got != "" {
		t.Errorf("lastUserTurn with no question = %q", got)
	}
}

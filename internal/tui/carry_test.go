package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
)

// TestAPlanConversationSurvivesARestart: the summary, the pinned plan and the last turns come back
// in the session a restarted runner builds for the same conversation.
func TestAPlanConversationSurvivesARestart(t *testing.T) {
	cfg := config.Default()
	r := newContextRunner(t, cfg)
	r.SetContextID("conv-1")
	s := r.conversation(cfg, nil)
	s.Restore("the account", "1. a\n2. b\n3. c")
	r.saveCarry()

	// the same HOME, a new runner: what a restarted gateway builds
	again := NewAppRunner(nil, nil, cfg, nil, nil, r.Log)
	again.SetContextID("conv-1")
	again.RestoreTranscript([]agent.DialogueTurn{
		{Kind: "plan", User: "plan the trip", Agent: "1. a\n2. b\n3. c"},
		{Kind: agent.KindTask, User: "failed", Agent: ""},
		{Kind: agent.KindTask, User: "running", Agent: "…", Pending: true},
	})
	got := again.conversation(cfg, nil)
	sum, pin := got.Carried()
	if sum != "the account" || pin != "1. a\n2. b\n3. c" {
		t.Errorf("carried = %q %q", sum, pin)
	}
	var users []string
	for _, m := range got.Messages() {
		if m.Role == "user" {
			users = append(users, m.Content)
		}
	}
	if strings.Join(users, "|") != "plan the trip" {
		t.Errorf("seeded turns = %v, want only the finished one", users)
	}

	// nothing to carry removes the file, so a dropped plan does not come back
	got.Restore("", "")
	again.saveCarry()
	if _, err := os.Stat(again.carryPath()); !os.IsNotExist(err) {
		t.Errorf("the empty carry was kept: %v", err)
	}
}

func TestSeedMessagesKeepsOnlyTheLastTurns(t *testing.T) {
	var turns []agent.DialogueTurn
	for i := 0; i < seedTurns+3; i++ {
		turns = append(turns, agent.DialogueTurn{User: "q", Agent: "a"})
	}
	if got := len(seedMessages(turns)); got != 2*seedTurns {
		t.Errorf("seeded %d messages, want %d", got, 2*seedTurns)
	}
}

// TestCarryIsSkippedWithoutAnArchiveOrASession: no archive, no session, no file; a broken file
// or one that cannot be written leaves the session as it was.
func TestCarryIsSkippedWithoutAnArchiveOrASession(t *testing.T) {
	off := config.Default()
	off.ContextMemory.Enabled = false
	r := newContextRunner(t, off)
	r.saveCarry()
	if r.carryPath() != "" {
		t.Error("a carry path without an archive")
	}
	r.conversation(off, nil)

	cfg := config.Default()
	on := newContextRunner(t, cfg)
	on.SetContextID("conv-2")
	on.saveCarry() // no session yet
	path := on.carryPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a carry was written with no session: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sum, pin := on.conversation(cfg, nil).Carried(); sum != "" || pin != "" {
		t.Errorf("a broken file was read: %q %q", sum, pin)
	}
	// the path taken by a folder: the write fails and is only logged
	_ = os.Remove(path)
	if err := os.MkdirAll(filepath.Join(path, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	on.session.Pin("1. a\n2. b\n3. c")
	on.saveCarry()
	on.Log = nil
	on.saveCarry()
}

package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// pinNotes counts the pinned-plan notes a request carries.
func pinNotes(s *Session) int {
	n := 0
	for _, m := range s.Messages() {
		if m.Role == "system" && strings.HasPrefix(m.Content, "## The active plan") {
			n++
		}
	}
	return n
}

// TestThePinnedPlanSurvivesACompaction: while the plan is in the conversation it is not sent
// twice; once a compaction folds it away it comes back word for word, after the summary.
func TestThePinnedPlanSurvivesACompaction(t *testing.T) {
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 3
	s.Summariser = &stubSummariser{out: "the account"}

	plan := "1. read\n2. change\n3. test"
	s.Append(user("make a plan"))
	s.Append(assistant("Here it is:\n" + plan))
	s.Pin(plan)
	if s.Pinned() != plan || pinNotes(s) != 0 {
		t.Fatalf("pinned %q, %d notes while the plan is still shown", s.Pinned(), pinNotes(s))
	}
	for i := 0; i < 30; i++ {
		s.Append(user(fmt.Sprintf("message %d %s", i, pad(20))))
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	msgs := s.Messages()
	if pinNotes(s) != 1 || !strings.Contains(msgs[2].Content, plan) || !strings.Contains(msgs[1].Content, "the account") {
		t.Errorf("after compaction the plan is not carried after the summary: %+v", msgs[:3])
	}
	s.Pin("  ")
	if s.Pinned() != "" || pinNotes(s) != 0 {
		t.Errorf("a blank pin must unpin")
	}
}

// TestAPinIsBounded: a runaway answer cannot fill the window on its own.
func TestAPinIsBounded(t *testing.T) {
	s := New("gpt-4o", "", 0)
	s.Pin(strings.Repeat("é", maxPinnedRunes+10))
	got := s.Pinned()
	if !strings.HasSuffix(got, "it was cut here]") || strings.Count(got, "é") != maxPinnedRunes {
		t.Errorf("pin was not cut at %d runes: %d", maxPinnedRunes, strings.Count(got, "é"))
	}
	if pinNotes(s) != 1 {
		t.Errorf("a plan no message shows must be carried")
	}
}

// TestWhatASessionCarriesCanBeRestored: the summary and the plan survive a rebuilt session.
func TestWhatASessionCarriesCanBeRestored(t *testing.T) {
	s := New("gpt-4o", "system", 0)
	s.Restore(" the account ", "1. a\n2. b\n3. c", false)
	sum, pin := s.Carried()
	if sum != "the account" || pin != "1. a\n2. b\n3. c" {
		t.Errorf("carried = %q %q", sum, pin)
	}
	msgs := s.Messages()
	if len(msgs) != 3 || !strings.Contains(msgs[1].Content, "the account") || !strings.Contains(msgs[2].Content, "1. a") {
		t.Errorf("restored session = %+v", msgs)
	}
}

// TestAProposedPlanWaitsForThePerson: a proposal is framed as not approved, a newer proposal
// replaces it, it never replaces an approved plan, and a done plan is let go only while it is the
// one pinned.
func TestAProposedPlanWaitsForThePerson(t *testing.T) {
	s := New("gpt-4o", "", 0)
	if s.PinState() != "" {
		t.Errorf("state with nothing pinned = %q", s.PinState())
	}
	s.Propose("1. a\n2. b\n3. c")
	s.Propose("  ")
	if s.PinState() != PinProposed || !strings.Contains(s.Messages()[0].Content, "NOT approved") {
		t.Errorf("proposal = %q %+v", s.PinState(), s.Messages())
	}
	s.Propose("1. x\n2. y\n3. z")
	if s.Pinned() != "1. x\n2. y\n3. z" {
		t.Errorf("a newer proposal did not replace the older one: %q", s.Pinned())
	}
	s.Pin("1. x\n2. y\n3. z")
	s.Propose("1. other\n2. b\n3. c")
	if s.PinState() != PinActive || pinNotes(s) != 1 {
		t.Errorf("approved plan = %q %q", s.Pinned(), s.PinState())
	}
	if s.Release("1. not it") || s.Release("") || s.Pinned() == "" {
		t.Error("a plan that is not the pinned one was let go")
	}
	if !s.Release(" 1. x\n2. y\n3. z ") || s.Pinned() != "" || s.PinState() != "" {
		t.Errorf("the done plan was kept: %q", s.Pinned())
	}
	s.Restore("", "1. a", true)
	if s.PinState() != PinProposed {
		t.Errorf("restored proposal = %q", s.PinState())
	}
	s.Restore("", "", true)
	if s.PinState() != "" {
		t.Errorf("an empty restore is %q", s.PinState())
	}
}

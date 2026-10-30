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

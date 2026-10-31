package session

import (
	"context"
	"fmt"
	"testing"

	"github.com/madkoding/motita/internal/llm"
)

// toolTurn opens a turn with request and adds rounds of tool calls, the shape a planner turn
// has: an assistant message and its result per round.
func toolTurn(s *Session, request string, rounds int) {
	s.BeginTurn(user(request))
	for i := 0; i < rounds; i++ {
		s.Append(llm.Message{Role: "assistant", Content: fmt.Sprintf("round %d %s", i, pad(20))})
		s.Append(toolResult(fmt.Sprintf("result %d %s", i, pad(20))))
	}
}

// compacted builds a small session and compacts it once.
func compacted(t *testing.T, fill func(s *Session)) *Session {
	t.Helper()
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 4
	s.Summariser = &stubSummariser{out: "the account"}
	fill(s)
	if err := s.Compact(context.Background()); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	return s
}

// TestACompactionInTheMiddleOfATurnKeepsItsRequest: a turn with several tool rounds grows past
// the verbatim tail, and the request that opened it must not survive only as a paraphrase.
func TestACompactionInTheMiddleOfATurnKeepsItsRequest(t *testing.T) {
	s := compacted(t, func(s *Session) { toolTurn(s, "rename the config loader", 8) })
	msgs := s.Messages()
	// system, summary, the request, then the verbatim tail.
	if got := msgs[2]; got.Role != "user" || got.Content != "rename the config loader" {
		t.Fatalf("the request is not kept after the summary: %+v", got)
	}
	if s.Len() != 5 {
		t.Errorf("held %d messages, want the request and the 4 kept verbatim", s.Len())
	}
	// A second compaction keeps it again, and only once.
	for i := 0; i < 8; i++ {
		s.Append(assistant(fmt.Sprintf("more %d %s", i, pad(20))))
	}
	if err := s.Compact(context.Background()); err != nil {
		t.Fatalf("second compaction failed: %v", err)
	}
	requests := 0
	for _, m := range s.Messages() {
		if m.Content == "rename the config loader" {
			requests++
		}
	}
	if requests != 1 {
		t.Errorf("the request is held %d times after two compactions, want 1", requests)
	}
}

// TestARequestStillInTheTailIsNotDuplicated: a request the tail keeps anyway stays where it is,
// and an earlier turn's request is summarised like any other message.
func TestARequestStillInTheTailIsNotDuplicated(t *testing.T) {
	s := compacted(t, func(s *Session) {
		toolTurn(s, "an earlier request", 8)
		toolTurn(s, "the current request", 1)
	})
	// The tail cut lands on a tool result and moves past it, so it opens at the current request.
	msgs := s.Messages()
	if s.Len() != 3 || msgs[2].Content != "the current request" {
		t.Errorf("the tail should hold the current request once, at its place: %+v", msgs)
	}
	for _, m := range msgs {
		if m.Content == "an earlier request" {
			t.Error("an earlier turn's request must be folded into the summary")
		}
	}
}

// TestASessionWithNoTurnCompactsAsBefore: messages appended without BeginTurn (a seeded
// transcript) are folded with nothing kept in front of the tail.
func TestASessionWithNoTurnCompactsAsBefore(t *testing.T) {
	s := compacted(t, func(s *Session) {
		for i := 0; i < 20; i++ {
			s.Append(user(fmt.Sprintf("message %d %s", i, pad(20))))
		}
	})
	if s.Len() != 4 {
		t.Errorf("held %d messages, want the 4 kept verbatim", s.Len())
	}
}

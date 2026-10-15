package session

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/madkoding/motita/internal/llm"
)

// funcSummariser answers with whatever its function returns, so a test can act while the model
// is "thinking".
type funcSummariser func(ctx context.Context, messages []llm.Message) (string, error)

func (f funcSummariser) Complete(ctx context.Context, messages []llm.Message) (string, error) {
	return f(ctx, messages)
}

// fullSession is a session past its trigger, with KeepRecent 3 and 30 messages.
func fullSession(sum Summariser) *Session {
	s := New("gpt-4o", "system", 1000)
	s.Reserve = 200
	s.CompactAt = 0.5
	s.KeepRecent = 3
	s.Summariser = sum
	for i := 0; i < 30; i++ {
		s.Append(user(fmt.Sprintf("message %d %s", i, pad(20))))
	}
	return s
}

// A message appended while the summary is being written is kept: only the prefix that was
// summarised is replaced. Before, the history was replaced with the tail read before the call,
// and the message appended meanwhile was gone.
func TestAMessageAppendedDuringCompactionIsKept(t *testing.T) {
	var s *Session
	s = fullSession(funcSummariser(func(context.Context, []llm.Message) (string, error) {
		s.Append(user("appended while summarising"))
		return "the account", nil
	}))
	if err := s.Compact(context.Background()); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	msgs := s.Messages()
	if last := msgs[len(msgs)-1]; last.Content != "appended while summarising" {
		t.Fatalf("the appended message was lost: last = %q", last.Content)
	}
	if s.Len() != 4 {
		t.Errorf("held %d messages, want the 3 kept verbatim plus the appended one", s.Len())
	}
	if s.Summary() != "the account" || s.Compactions() != 1 {
		t.Errorf("summary %q after %d compactions", s.Summary(), s.Compactions())
	}
}

// Two compactions that overlap: the one that finishes second read a prefix the first already
// folded away, so it is discarded rather than installed over the first one's account.
func TestAnOverlappingCompactionIsDiscarded(t *testing.T) {
	var s *Session
	calls := 0
	s = fullSession(funcSummariser(func(ctx context.Context, _ []llm.Message) (string, error) {
		calls++
		if calls == 1 {
			// The second compaction runs, and finishes, while the first is still summarising.
			if err := s.Compact(ctx); err != nil {
				t.Errorf("the inner compaction failed: %v", err)
			}
			return "outer", nil
		}
		return "inner", nil
	}))
	if err := s.Compact(context.Background()); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if s.Summary() != "inner" || s.Compactions() != 1 || s.Len() != 3 {
		t.Errorf("summary %q, %d compactions, %d messages; want the inner one alone", s.Summary(),
			s.Compactions(), s.Len())
	}
}

// Appends from the goroutine running the turn and a compaction from another, under -race: the
// history is only ever touched under the lock, and every append survives.
func TestCompactionAndAppendsRunConcurrently(t *testing.T) {
	s := fullSession(funcSummariser(func(context.Context, []llm.Message) (string, error) {
		return "the account", nil
	}))
	const appends = 50
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < appends; i++ {
			s.Append(assistant(fmt.Sprintf("late %d", i)))
		}
	})
	if err := s.Compact(context.Background()); err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	wg.Wait()
	seen := 0
	for _, m := range s.Messages() {
		if m.Role == "assistant" {
			seen++
		}
	}
	if seen != appends {
		t.Errorf("%d of the %d appended messages survived", seen, appends)
	}
}

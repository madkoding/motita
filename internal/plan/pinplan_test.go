package plan

import (
	"context"
	"testing"

	"github.com/madkoding/motita/internal/session"
)

func TestLooksLikePlan(t *testing.T) {
	cases := map[string]bool{
		"1. read\n2. change\n3. test":           true,
		"- [ ] a\n- [x] b\n* [ ] c":             true,
		"1) a\n2) b":                            false,
		"Just an answer.\n- a point\n- another": false,
	}
	for in, want := range cases {
		if got := looksLikePlan(in); got != want {
			t.Errorf("looksLikePlan(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestAPlanAnswerIsPinnedAndAnAnswerAboutItIsNot: the plan becomes the session's pin, and a
// later answer that is not a plan leaves it in force.
func TestAPlanAnswerIsPinnedAndAnAnswerAboutItIsNot(t *testing.T) {
	a, _ := makeAgent(t, true)
	plan := "1. outline the trip\n2. book the hotel\n3. pack the bags"
	srv := llmServer(t, []replyStep{
		{finishReason: "stop", content: plan},
		{finishReason: "stop", content: "Step 2 means the hotel."},
	})
	t.Cleanup(srv.Close)
	p := New(newClient(t, srv), a)
	for _, q := range []string{"plan it", "what is step 2?"} {
		if _, err := p.Run(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.Session().Pinned(); got != plan {
		t.Errorf("pinned = %q, want the plan", got)
	}
}

// TestTheForcedAnswerIsPinnedToo: a plan the model gives only when the tool limit forces it is
// as much the plan as any other.
func TestTheForcedAnswerIsPinnedToo(t *testing.T) {
	s := session.New("gpt-4o", "", 0)
	pinIfPlan(s, "1. a\n2. b\n3. c")
	if s.Pinned() == "" {
		t.Error("a plan was not pinned")
	}
}

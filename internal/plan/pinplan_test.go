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
	pinIfPlan(s, "make a plan", "1. a\n2. b\n3. c")
	if s.Pinned() == "" {
		t.Error("a plan was not pinned")
	}
}

// TestOnlyAPlanThePersonAskedForIsProposed: a numbered answer to a question is not a plan, a plan
// asked for is pinned as proposed, and a proposal never replaces an approved plan.
func TestOnlyAPlanThePersonAskedForIsProposed(t *testing.T) {
	list := "The differences are:\n1. speed\n2. size\n3. price"
	s := session.New("gpt-4o", "", 0)
	pinIfPlan(s, "what are the differences between them?", list)
	if s.Pinned() != "" {
		t.Errorf("an informative list was pinned: %q", s.Pinned())
	}
	pinIfPlan(s, "dame un plan para migrar", "1. a\n2. b\n3. c") // spanish-fixture: input
	if s.PinState() != session.PinProposed {
		t.Errorf("a plan asked for is %q, want proposed", s.PinState())
	}
	s.Pin("1. approved\n2. b\n3. c")
	pinIfPlan(s, "make another plan", "1. x\n2. y\n3. z")
	if s.Pinned() != "1. approved\n2. b\n3. c" || s.PinState() != session.PinActive {
		t.Errorf("a proposal replaced the approved plan: %q %q", s.Pinned(), s.PinState())
	}
}

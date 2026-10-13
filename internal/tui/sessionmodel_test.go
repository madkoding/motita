package tui

import (
	"context"
	"strings"
	"testing"
)

// ceilingRunner is a switcher that knows its gateway's ceiling.
type ceilingRunner struct {
	*switcherRunner
	max int
}

func (c *ceilingRunner) MaxSessions() int { return c.max }

// TestTheListingShowsEachSessionsModelAndTheCeiling: the model belongs to the session, so the list
// says which one each runs; and it says how close the gateway is to refusing a new one.
func TestTheListingShowsEachSessionsModelAndTheCeiling(t *testing.T) {
	r := &ceilingRunner{switcherRunner: &switcherRunner{fakeRunner: &fakeRunner{},
		sessions: []SessionInfo{{ID: "sa", Model: "haiku"}, {ID: "sb", Model: "opus"}}, current: "sa"}, max: 64}
	out, err := newFakeTUI("", r).sessionsText(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sa  haiku", "sb  opus", "2 of 64 sessions in use"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing from:\n%s", want, out)
		}
	}

	// A gateway that did not report its ceiling gets no made-up one.
	r.max = 0
	if out, _ := newFakeTUI("", r).sessionsText(context.Background()); strings.Contains(out, "in use") {
		t.Errorf("a ceiling was reported for a gateway that sent none:\n%s", out)
	}
}

// TestSwitchingTheModelOfAConversationThatTalkedWarns: the switch is applied, and said out loud when
// the conversation already has turns or a turn is running.
func TestSwitchingTheModelOfAConversationThatTalkedWarns(t *testing.T) {
	f := &fakeRunner{}
	tu := newFakeTUI("/models haiku\n", f)
	tu.Run(context.Background())
	if strings.Contains(lastFrame(t, tu), "⚠") {
		t.Errorf("a conversation with nothing said yet was warned:\n%s", lastFrame(t, tu))
	}

	tu = newFakeTUI("/models haiku\n", &fakeRunner{})
	tu.messages = append(tu.messages, Message{Author: AuthorUser, Text: "hi"})
	tu.Run(context.Background())
	if !strings.Contains(lastFrame(t, tu), "⚠") {
		t.Errorf("a switch after the conversation started was not warned:\n%s", lastFrame(t, tu))
	}

	tu = newFakeTUI("", &fakeRunner{})
	if got := tu.modelSwitchWarning("a", "a"); got != "" {
		t.Errorf("keeping the model warned %q", got)
	}
	tu.messages = append(tu.messages, Message{Author: AuthorUser, Text: "hi"})
	got := tu.modelSwitchWarning("a", "b")
	if !strings.Contains(got, "written with a") || strings.Contains(got, "in progress") {
		t.Errorf("a switch after the conversation started warned %q", got)
	}
	tu.busy = true
	if got := tu.modelSwitchWarning("a", "b"); strings.Count(got, "⚠") != 2 {
		t.Errorf("a switch mid-turn warned %q, want both warnings", got)
	}
}

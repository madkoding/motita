package agent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/memory"
)

// A long conversation, reduced to what makes an agent lose its place: the person agreed on a plan
// many turns ago, said something about the work a few turns ago, keeps a fact in the memory, and
// now answers "yes, do it". Every round of the work must still see all four, or the agent works
// from the task text alone and wanders.
//
// The probe counts how many of them reach the prompt of the rounds that do the work. It is the
// measure the context brief is judged by: before it, the working rounds saw none of them.
func TestTheWorkingRoundsSeeTheWholeContext(t *testing.T) {
	fake := &fakeLLMServer{donePerAttempt: []bool{true}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	mem := memory.Open(t.TempDir())
	mem.WriteText("Deploys", "Deploys go to the staging-zeta cluster first.")
	e.agent.SetMemory(mem)
	e.agent.SetRecall(func(context.Context, string, int) (string, int) {
		return "Turn 3: the person asked to keep the old endpoint ARCHIVED-ENDPOINT.", 1
	})
	e.agent.SetPinnedPlan(func() string { return "1. add the PINNED-CACHE layer\n2. test it\n3. deploy it" })
	e.agent.SetTranscript([]DialogueTurn{
		{Kind: KindChat, User: "use the DIALOGUE-REDIS client, not memcached", Agent: "noted"},
		{Kind: KindTask, User: "deploy it to staging, yes do it", Agent: pendingText, Pending: true},
	})

	e.agent.loop(context.Background(), textTask("deploy it to staging, yes do it"), 0)
	prompt := fake.lastExecutePrompt()
	if prompt == "" {
		t.Fatal("no working round ran")
	}
	probes := []string{"PINNED-CACHE", "DIALOGUE-REDIS", "ARCHIVED-ENDPOINT", "staging-zeta"}
	var missing []string
	for _, p := range probes {
		if !strings.Contains(prompt, p) {
			missing = append(missing, p)
		}
	}
	t.Logf("context reaching the working round: %d of %d", len(probes)-len(missing), len(probes))
	if len(missing) > 0 {
		t.Errorf("the working round does not see %v", missing)
	}
}

// The pinned plan is left out when there is none, or when the request already carries it word for
// word (a task that carries out the plan): the brief must not say the same plan twice.
func TestThePinnedPlanIsNotRepeated(t *testing.T) {
	a := &Agent{}
	if got := a.pinnedPlan(textTask("go on")); got != "" {
		t.Errorf("no hook gave %q", got)
	}
	a.SetPinnedPlan(func() string { return "  " })
	if got := a.pinnedPlan(textTask("go on")); got != "" {
		t.Errorf("an empty pin gave %q", got)
	}
	a.SetPinnedPlan(func() string { return "1. a\n2. b" })
	if got := a.pinnedPlan(textTask("carry out this plan:\n1. a\n2. b")); got != "" {
		t.Errorf("a plan the request carries was repeated: %q", got)
	}
	if got := a.pinnedPlan(textTask("go on")); got != "1. a\n2. b" {
		t.Errorf("pinnedPlan = %q", got)
	}
}

// A task that carries out the plan never gets the pin again, even when the pin was edited since,
// and a long pin is held to its share of the brief so the archive and the memory still fit.
func TestThePinnedPlanKeepsToItsShare(t *testing.T) {
	a := &Agent{}
	a.SetPinnedPlan(func() string { return "1. older wording\n2. b\n3. c" })
	if got := a.pinnedPlan(textTask(FollowPlanTask("ok", "1. a\n2. b\n3. c"))); got != "" {
		t.Errorf("a plan-following task got the pin too: %q", got)
	}
	long := strings.Repeat("x", 2*pinnedShare)
	a.SetPinnedPlan(func() string { return long })
	if got := a.pinnedPlan(textTask("go on")); len(got) > pinnedShare || !strings.Contains(got, "cut to fit") {
		t.Errorf("the pin took %d of the %d it may", len(got), pinnedShare)
	}
}

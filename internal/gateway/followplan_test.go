package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// A go-ahead after a plan runs the plan as a task, with the plan inside the task; after anything
// else it is an ordinary message.
func TestAGoAheadAfterAPlanCarriesItOut(t *testing.T) {
	var got []string
	svc := autoService(&got)
	svc.transcript = []agent.DialogueTurn{{User: "how?", Agent: "1. do this\n2. then that", Kind: "plan"}}
	srv := newTestServer(t, svc)
	events := withoutPreamble(t, collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/run"), `{"task":"ok, do it"}`))
	if len(events) == 0 || !strings.Contains(events[0].Data, "mode: task (carrying out the plan)") {
		t.Fatalf("the note must say the plan is carried out: %+v", events)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "task:"+agent.PlanLead) || !strings.Contains(got[0], "2. then that") {
		t.Errorf("ran %v", got)
	}

	for _, turns := range [][]agent.DialogueTurn{
		nil,
		{{User: "fix it", Agent: "fixed", Kind: agent.KindTask}},
		{{User: "how?", Agent: "", Kind: "plan"}},
		{{User: "how?", Agent: "1. x", Kind: "plan", Pending: true}},
	} {
		got = nil
		svc := autoService(&got)
		svc.transcript = turns
		srv := newTestServer(t, svc)
		collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/run"), `{"task":"ok"}`)
		if len(got) != 1 || got[0] != "task:ok" {
			t.Errorf("after %+v ran %v, want the bare message", turns, got)
		}
	}
}

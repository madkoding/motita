package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/llm"
)

func gatewayAgents() []agent.AgentInfo {
	t0 := time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)
	return []agent.AgentInfo{
		{ID: "main", Purpose: "the task", State: agent.AgentRunning, Started: t0, Tokens: llm.Usage{Input: 100}},
		{ID: "a1", Parent: "main", Purpose: "write the tests", State: agent.AgentRunning, Started: t0},
		{ID: "a2", Parent: "main", Purpose: "the docs", State: agent.AgentPassed, Started: t0, Branch: "motita/sub/a2"},
	}
}

// TestTheAgentsSnapshotIsFlashedNotLogged: the list of a run's agents reaches the reader attached
// now and the conversation, and is never a step: it is not in the log a reconnecting client replays,
// and not in the checkpoint the transcript is rebuilt from.
func TestTheAgentsSnapshotIsFlashedNotLogged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rn := newRun("r", ctx, cancel, "")
	var kept []agent.AgentInfo
	var steps []string
	rn.onAgents = func(a []agent.AgentInfo) { kept = a }
	rn.onLine = func(s string) { steps = append(steps, s) }
	sub := rn.subscribe()
	rn.progress()("%s", agent.AgentsLine(gatewayAgents()))

	e := <-sub.ch
	if e.Event != EventAgents || e.Seq != 0 || !strings.Contains(string(e.Data), `"purpose":"write the tests"`) {
		t.Fatalf("event = %+v", e)
	}
	if replay, _ := rn.since(0); len(replay) != 0 {
		t.Errorf("the snapshot must not be logged: %+v", replay)
	}
	if len(kept) != 3 || len(steps) != 0 {
		t.Errorf("kept=%d steps=%q", len(kept), steps)
	}

	// A run nobody keeps the list for still flashes it.
	bare := newRun("r2", ctx, cancel, "")
	sub2 := bare.subscribe()
	bare.progress()("%s", agent.AgentsLine(nil))
	if e := <-sub2.ch; e.Event != EventAgents || string(e.Data) != `{"agents":[]}` {
		t.Fatalf("bare run event = %+v %s", e, e.Data)
	}
}

// TestASessionKeepsItsAgentsAndCountsTheRunningOnes walks the whole life of the list through the
// API: reported while the run works, in the preamble of a client that joins, counted in the session
// list, still readable after the run, and started over by the next run.
func TestASessionKeepsItsAgentsAndCountsTheRunningOnes(t *testing.T) {
	release := make(chan struct{})
	svc := &fakeService{task: func(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
		if task == "second" {
			return "nothing started", nil
		}
		progress("%s", agent.AgentsLine(gatewayAgents()))
		select {
		case <-release:
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	srv := newTestServer(t, svc)

	if w := get(t, srv, sessionPath(srv, DefaultSession, "/agents"), testToken); w.Code != http.StatusOK || w.Body.String() != "{\"agents\":[]}\n" {
		t.Fatalf("a session that never ran agents = %d %q", w.Code, w.Body.String())
	}

	cancel := startInBackground(t, srv, DefaultSession, "/task", `{"task":"first"}`)
	defer cancel()
	conv, _ := srv.lookup(DefaultSession)
	deadline := time.Now().Add(10 * time.Second)
	for len(conv.agentsNow()) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}

	events := firstEvents(t, srv, sessionPath(srv, DefaultSession, "/events?from=0"), 1)
	var pre attachedEvent
	if len(events) != 1 || json.Unmarshal([]byte(events[0].Data), &pre) != nil || len(pre.Agents) != 3 {
		t.Fatalf("the preamble must carry the agents: %+v", events)
	}

	var list struct {
		Sessions []SessionStatus `json:"sessions"`
	}
	if err := json.Unmarshal(get(t, srv, "/v1/sessions", testToken).Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) == 0 || list.Sessions[0].AgentsRunning != 1 {
		t.Fatalf("agents_running must count the background agents at work, the main one aside: %+v", list.Sessions)
	}

	close(release)
	waitForNoRun(t, srv)
	w := get(t, srv, sessionPath(srv, DefaultSession, "/agents"), testToken)
	var after agentsEvent
	if err := json.Unmarshal(w.Body.Bytes(), &after); err != nil || len(after.Agents) != 3 || after.Agents[2].Branch != "motita/sub/a2" {
		t.Fatalf("the finished run's agents must still be readable: %s", w.Body.String())
	}

	collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"second"}`)
	if got := conv.agentsNow(); len(got) != 0 {
		t.Fatalf("a new run starts with no agents, got %+v", got)
	}
}

// TestTheClientHandsTheAgentsOnAsTheAgentsLine: whichever way the list arrives - in the preamble or
// as its own event - a consumer of the client sees the same line the agent writes in local mode.
func TestTheClientHandsTheAgentsOnAsTheAgentsLine(t *testing.T) {
	list, _ := json.Marshal(gatewayAgents()[:2])
	body := fmt.Sprintf("event: attached\ndata: {\"run_id\":\"r\",\"agents\":%s}\n\n", list) +
		"event: attached\ndata: {\"run_id\":\"r\"}\n\n" +
		fmt.Sprintf("event: agents\ndata: {\"agents\":%s}\n\n", list) +
		"event: done\ndata: {\"result\":\"ok\"}\n\n"
	c := rawGateway(t, body)
	var lines []string
	got, err := c.RunTask(context.Background(), "x", func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	if err != nil || got != "ok" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %q, want one from the preamble and one from the event", lines)
	}
	for _, l := range lines {
		if a, ok := agent.ParseAgentsLine(l); !ok || len(a) != 2 {
			t.Errorf("not an agents line: %q", l)
		}
	}

	// Without a callback the list is read and dropped.
	if _, err := rawGateway(t, body).RunTask(context.Background(), "x", nil); err != nil {
		t.Fatalf("no callback: %v", err)
	}
	// A list the client cannot read ends the run with a reason, like every other payload.
	if _, err := rawGateway(t, "event: agents\ndata: not json\n\n").RunTask(context.Background(), "x", func(string, ...any) {}); err == nil {
		t.Fatal("a malformed agents event must end the run")
	}
}

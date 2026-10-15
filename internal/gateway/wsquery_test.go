package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// openQueryClient dials the default conversation's WebSocket and authenticates, ready to query.
func openQueryClient(t *testing.T, srv *Server) *wsTestClient {
	t.Helper()
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	t.Cleanup(cl.close)
	_ = cl.readMsg(t) // welcome
	if err := cl.sendTextMsg(makeMsg(MsgAuth, map[string]any{"token": testToken})); err != nil {
		t.Fatalf("sending the auth: %v", err)
	}
	if resp := cl.readMsg(t); !resp.hasFlag(FlagAuthenticated) {
		t.Fatalf("auth flags = %v, want %s", resp.Flags, FlagAuthenticated)
	}
	return cl
}

// A query that takes longer than the heartbeat keeps its connection: the client answers every
// heartbeat while the query runs, and gets the answer. The query used to run inside the read loop,
// so nothing read the client's acks, and the heartbeat closed a healthy connection ("heartbeat ack
// timed out") about 40 seconds into any long query.
func TestALongWebSocketQuerySurvivesTheHeartbeat(t *testing.T) {
	release := make(chan struct{})
	svc := &fakeService{plan: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		select {
		case <-release:
			return "the slow answer", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	srv := newTestServer(t, svc, func(o *Options) { o.WSHeartbeat = 20 * time.Millisecond })
	cl := openQueryClient(t, srv)

	if err := cl.sendTextMsg(makeMsg(MsgQuery, map[string]any{"query": "think about it"})); err != nil {
		t.Fatalf("sending the query: %v", err)
	}
	heartbeats := 0
	for {
		msg := cl.readMsg(t)
		switch {
		case msg.Type == MsgHeartbeat:
			heartbeats++
			if err := cl.sendTextMsg(makeMsg(MsgHeartbeatAck, nil)); err != nil {
				t.Fatalf("acking heartbeat %d: %v", heartbeats, err)
			}
			// Several heartbeats each answered while the query is still running: the acks are
			// being read, so the query ends only now.
			if heartbeats == 3 {
				close(release)
			}
		case msg.Type == MsgQueryResponse && msg.hasFlag(FlagCompleted):
			var payload struct {
				Result string `json:"result"`
			}
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Result != "the slow answer" {
				t.Errorf("result = %q, want the query's answer", payload.Result)
			}
			if heartbeats < 3 {
				t.Errorf("the answer came after %d heartbeats, want it after the third", heartbeats)
			}
			return
		case msg.Type == MsgQueryResponse && msg.hasFlag(FlagProcessing):
			// The ack of the query.
		default:
			t.Fatalf("unexpected %q with flags %v: %s", msg.Type, msg.Flags, msg.Payload)
		}
	}
}

// A query whose connection goes away is stopped: its answer can only be written to that
// connection, so going on would spend a model's tokens on an answer nobody can receive. The
// conversation's run slot is given back with it.
func TestAWebSocketQueryStopsWhenItsConnectionCloses(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan error, 1)
	svc := &fakeService{plan: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		close(started)
		<-ctx.Done()
		stopped <- ctx.Err()
		return "", ctx.Err()
	}}
	srv := newWSTestServer(t, svc)
	cl := openQueryClient(t, srv)

	if err := cl.sendTextMsg(makeMsg(MsgQuery, map[string]any{"query": "think about it"})); err != nil {
		t.Fatalf("sending the query: %v", err)
	}
	<-started
	c, _ := srv.lookup(DefaultSession)
	if !c.isRunning() {
		t.Error("a running query must hold the conversation's run slot")
	}
	cl.close()

	select {
	case err := <-stopped:
		if err == nil {
			t.Error("the query's context ended without an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the query was still running after its connection closed")
	}
	waitForNoRun(t, srv)
}

// A query takes the conversation's run slot like any other run, so one that finds a run in flight
// is answered BUSY and the model is never called: the query and the run would otherwise write one
// transcript at once.
func TestAWebSocketQueryIsBusyWhileARunHoldsTheSlot(t *testing.T) {
	called := false
	svc := &fakeService{plan: func(context.Context, string, func(string, ...any)) (string, error) {
		called = true
		return "should not run", nil
	}}
	cl, client, _ := wsPair(t, svc, "the-token")
	cl.authenticated = true
	if !cl.conv.takeRunSlot() {
		t.Fatal("the slot of a fresh conversation must be free")
	}

	go cl.handleQuery(wsMessage{
		MsgID: "m1", Type: MsgQuery, Timestamp: "2026-01-01T00:00:00Z",
		Payload: json.RawMessage(`{"query":"hello"}`),
	})

	_, msg := readOneFrame(t, client)
	if msg.Type != MsgQueryResponse || !msg.hasFlag(FlagBusy) {
		t.Fatalf("got %q with flags %v, want a query_response with %s", msg.Type, msg.Flags, FlagBusy)
	}
	cl.queries.Wait()
	if called {
		t.Error("a query refused as BUSY must not reach the model")
	}
	if !cl.conv.isRunning() {
		t.Error("a refused query must not give back a slot it never took")
	}
}

// A query that ended gives the slot back, so the conversation can run again.
func TestAWebSocketQueryGivesItsSlotBack(t *testing.T) {
	svc := &fakeService{plan: func(context.Context, string, func(string, ...any)) (string, error) {
		return "the answer", nil
	}}
	cl, client, _ := wsPair(t, svc, "the-token")
	cl.authenticated = true

	go cl.handleQuery(wsMessage{
		MsgID: "m1", Type: MsgQuery, Timestamp: "2026-01-01T00:00:00Z",
		Payload: json.RawMessage(`{"query":"hello"}`),
	})
	_, ack := readOneFrame(t, client)
	_, answer := readOneFrame(t, client)
	if !ack.hasFlag(FlagProcessing) || !answer.hasFlag(FlagCompleted) {
		t.Fatalf("flags = %v then %v, want %s then %s", ack.Flags, answer.Flags, FlagProcessing, FlagCompleted)
	}
	cl.queries.Wait()
	if cl.conv.isRunning() {
		t.Error("the slot is still held after the query answered")
	}
}

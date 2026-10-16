package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"
)

// THE WEBSOCKET WIRE CONTRACT.
//
// Programs outside Motita speak this protocol, so what travels on the wire is a public contract.
// The tests next to this one use the Go constants (MsgAuth, FlagIdle...), which means a renamed
// value would change both ends at once and pass. This file spells the contract out as literals:
// a change here is a break for every client already written, and has to be a decision.

// wireFrame reads one server message as the generic JSON object a foreign client would see.
func wireFrame(t *testing.T, cl *wsTestClient) map[string]any {
	t.Helper()
	raw, opcode := cl.readText(t)
	if opcode != opText {
		t.Fatalf("expected a text frame, got opcode %x", opcode)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	return m
}

func wireKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func wireFlags(t *testing.T, m map[string]any) []string {
	t.Helper()
	raw, ok := m["flags"].([]any)
	if !ok {
		t.Fatalf("flags is not an array: %#v", m["flags"])
	}
	flags := make([]string, 0, len(raw))
	for _, f := range raw {
		flags = append(flags, f.(string))
	}
	return flags
}

func wireSend(t *testing.T, cl *wsTestClient, typ, payload string) {
	t.Helper()
	body := `{"msg_id":"c-1","type":"` + typ + `","flags":[],"timestamp":"2026-01-01T00:00:00Z","payload":` + payload + `}`
	if err := cl.sendText([]byte(body)); err != nil {
		t.Fatal(err)
	}
}

func TestTheWireNamesAreFrozen(t *testing.T) {
	types := map[string]string{
		MsgAuth: "auth", MsgAuthResponse: "auth_response", MsgQuery: "query",
		MsgQueryResponse: "query_response", MsgHeartbeat: "heartbeat",
		MsgHeartbeatAck: "heartbeat_ack", MsgNotification: "notification", MsgError: "error",
	}
	for got, want := range types {
		if got != want {
			t.Errorf("message type %q changed from %q", got, want)
		}
	}
	flags := map[string]string{
		FlagAuthenticated: "AUTHENTICATED", FlagUnauthorized: "UNAUTHORIZED", FlagIdle: "IDLE",
		FlagBusy: "BUSY", FlagProcessing: "PROCESSING", FlagCompleted: "COMPLETED",
		FlagPartial: "PARTIAL", FlagErrorRecoverable: "ERROR_RECOVERABLE",
		FlagFatalError: "FATAL_ERROR", FlagLowMemory: "LOW_MEMORY", FlagRateLimited: "RATE_LIMITED",
		FlagCacheHit: "CACHE_HIT", FlagCacheMiss: "CACHE_MISS", FlagEncrypted: "ENCRYPTED",
	}
	for got, want := range flags {
		if got != want {
			t.Errorf("flag %q changed from %q", got, want)
		}
	}
}

func TestTheWireTimingsAreFrozen(t *testing.T) {
	if wsHeartbeatInterval != 30*time.Second {
		t.Errorf("heartbeat interval = %v, the protocol says 30s", wsHeartbeatInterval)
	}
	if wsHeartbeatTimeout != 10*time.Second {
		t.Errorf("heartbeat ack timeout = %v, the protocol says 10s", wsHeartbeatTimeout)
	}
}

func TestTheEnvelopeHasExactlyTheFiveDocumentedFields(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()

	welcome := wireFrame(t, cl)
	want := []string{"flags", "msg_id", "payload", "timestamp", "type"}
	if got := wireKeys(welcome); !reflect.DeepEqual(got, want) {
		t.Errorf("envelope fields = %v, want %v", got, want)
	}
	if welcome["type"] != "auth_response" {
		t.Errorf("welcome type = %v, want auth_response", welcome["type"])
	}
	if got := wireFlags(t, welcome); !reflect.DeepEqual(got, []string{"IDLE"}) {
		t.Errorf("welcome flags = %v, want [IDLE]", got)
	}
	if _, err := time.Parse(time.RFC3339Nano, welcome["timestamp"].(string)); err != nil {
		t.Errorf("timestamp is not RFC 3339: %v", err)
	}
}

func TestTheDocumentedConversationIsStable(t *testing.T) {
	svc := &fakeService{
		plan: func(_ context.Context, q string, _ func(string, ...any)) (string, error) {
			return "echo: " + q, nil
		},
	}
	srv := newWSTestServer(t, svc)
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = wireFrame(t, cl) // welcome

	wireSend(t, cl, "auth", `{"token":"`+testToken+`"}`)
	auth := wireFrame(t, cl)
	if got := wireFlags(t, auth); !reflect.DeepEqual(got, []string{"AUTHENTICATED", "IDLE"}) {
		t.Errorf("auth flags = %v, want [AUTHENTICATED IDLE]", got)
	}

	wireSend(t, cl, "query", `{"query":"hi"}`)
	processing := wireFrame(t, cl)
	if processing["type"] != "query_response" {
		t.Errorf("ack type = %v, want query_response", processing["type"])
	}
	if got := wireFlags(t, processing); !reflect.DeepEqual(got, []string{"PROCESSING"}) {
		t.Errorf("ack flags = %v, want [PROCESSING]", got)
	}
	done := wireFrame(t, cl)
	if got := wireFlags(t, done); !reflect.DeepEqual(got, []string{"COMPLETED"}) {
		t.Errorf("answer flags = %v, want [COMPLETED]", got)
	}
	payload := done["payload"].(map[string]any)
	if payload["msg_id"] != "c-1" || payload["result"] != "echo: hi" {
		t.Errorf("answer payload = %v, want msg_id c-1 and result \"echo: hi\"", payload)
	}
}

func TestTheDocumentedFailuresKeepTheirFlags(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = wireFrame(t, cl) // welcome

	cases := []struct {
		name, typ, payload string
		flag               string
	}{
		{"a query before auth", "query", `{"query":"hi"}`, "UNAUTHORIZED"},
		{"a wrong token", "auth", `{"token":"nope"}`, "UNAUTHORIZED"},
		{"a server-only type", "error", `{}`, "ERROR_RECOVERABLE"},
		{"an unknown type", "dance", `{}`, "ERROR_RECOVERABLE"},
	}
	for _, c := range cases {
		wireSend(t, cl, c.typ, c.payload)
		got := wireFlags(t, wireFrame(t, cl))
		if !reflect.DeepEqual(got, []string{c.flag}) {
			t.Errorf("%s: flags = %v, want [%s]", c.name, got, c.flag)
		}
	}

	if err := cl.sendText([]byte(`not json`)); err != nil {
		t.Fatal(err)
	}
	bad := wireFrame(t, cl)
	if bad["type"] != "error" || !reflect.DeepEqual(wireFlags(t, bad), []string{"ERROR_RECOVERABLE"}) {
		t.Errorf("malformed JSON answered %v, want an error with ERROR_RECOVERABLE", bad)
	}
}

func TestTheClientHeartbeatIsAnsweredWithAnAck(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = wireFrame(t, cl) // welcome

	wireSend(t, cl, "heartbeat", `{}`)
	ack := wireFrame(t, cl)
	if ack["type"] != "heartbeat_ack" {
		t.Errorf("heartbeat answered with %v, want heartbeat_ack", ack["type"])
	}
}

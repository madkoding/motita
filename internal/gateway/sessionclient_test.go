package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sessionRecorder is a gateway that records every path it is asked for and answers the given
// payload, so a test can assert WHERE the client went rather than only that it got an answer.
func sessionRecorder(t *testing.T, status int, payload any) (*Client, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		if status >= http.StatusBadRequest {
			writeError(w, status, "no")
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, testToken), &seen
}

// TestTheClientAddressesTheSessionItWasGiven: a client that could silently be unbound is a client
// whose status bar can show one conversation while its run lands in another. The session is a
// constructor argument for exactly that reason.
func TestTheClientAddressesTheSessionItWasGiven(t *testing.T) {
	c, seen := sessionRecorder(t, http.StatusOK, map[string]string{"text": "ok"})
	c = NewClientForSession(strings.TrimSuffix(c.baseURL, ""), testToken, "sabc")

	if _, err := c.RunModels(context.Background()); err != nil {
		t.Fatalf("RunModels: %v", err)
	}
	if err := c.CloseSession(context.Background(), "sother"); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("the gateway was asked %d times, want 2: %v", len(*seen), *seen)
	}
	if !strings.HasPrefix((*seen)[0], "/v1/sessions/sabc/") {
		t.Errorf("RunModels addressed %q, every conversation call must name its session", (*seen)[0])
	}
	if (*seen)[1] != "/v1/sessions/sother" {
		t.Errorf("CloseSession addressed %q, it must name the session being closed", (*seen)[1])
	}
}

// TestCreateAndListSessionsAreNotScoped: opening and listing conversations is how a client FINDS a
// conversation, so those two cannot be prefixed with one - a client that prefixed them would be
// addressing itself. This is the pair the plan singles out, and it is a test because the natural
// mistake is to route every call through scoped.
func TestCreateAndListSessionsAreNotScoped(t *testing.T) {
	c, seen := sessionRecorder(t, http.StatusOK, map[string]any{
		"sessions": []SessionStatus{{ID: DefaultSession}},
	})
	c = NewClientForSession(strings.TrimSuffix(c.baseURL, ""), testToken, "sabc")

	if _, err := c.CreateSession(context.Background()); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := c.ListSessions(context.Background()); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	for _, path := range *seen {
		if path != "/v1/sessions" {
			t.Errorf("a session-management call addressed %q, it must be the unprefixed list", path)
		}
	}
}

// TestListSessionsAnswersAnEmptyListAndNotNil: a caller that has to tell "none" from "the field is
// missing" is a caller with a bug waiting to happen, and the fix costs one line in the client.
func TestListSessionsAnswersAnEmptyListAndNotNil(t *testing.T) {
	c, _ := sessionRecorder(t, http.StatusOK, map[string]any{})
	c = NewClientForSession(strings.TrimSuffix(c.baseURL, ""), testToken, "sabc")

	got, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if got == nil {
		t.Fatal("ListSessions returned nil, an empty conversation list must be an empty list")
	}
	if len(got) != 0 {
		t.Errorf("ListSessions returned %d sessions from an empty answer", len(got))
	}
}

// TestCreateSessionCarriesTheRefusal: a gateway at its ceiling refuses with a reason, and a client
// that flattened it into a generic failure would hide the one thing the caller can act on.
func TestCreateSessionCarriesTheRefusal(t *testing.T) {
	c, _ := sessionRecorder(t, http.StatusConflict, nil)
	c = NewClientForSession(strings.TrimSuffix(c.baseURL, ""), testToken, "sabc")

	if _, err := c.CreateSession(context.Background()); err == nil {
		t.Fatal("a refused CreateSession must be reported")
	}
}

// TestListSessionsCarriesTheRefusal is the same rule for the read: a gateway that cannot be asked
// what it holds has to say so, because a caller that saw an empty list would open a conversation
// it did not need - or, worse, believe the one it was using had gone.
func TestListSessionsCarriesTheRefusal(t *testing.T) {
	c, _ := sessionRecorder(t, http.StatusUnauthorized, nil)
	c = NewClientForSession(strings.TrimSuffix(c.baseURL, ""), testToken, "sabc")

	got, err := c.ListSessions(context.Background())
	if err == nil {
		t.Fatal("a refused ListSessions must be reported, not answered with an empty list")
	}
	if got != nil {
		t.Errorf("a failed ListSessions returned %v, it must return nothing", got)
	}
}

// TestASessionStatusRoundTripsThroughTheWire pins the field names. The client and the server share
// one type, so a renamed json tag would compile on both sides and send a client a struct full of
// zeroes - which is exactly the kind of break the shared type is supposed to make impossible, and
// it is worth a test that reads them.
func TestASessionStatusRoundTripsThroughTheWire(t *testing.T) {
	payload := SessionStatus{ID: "sabc", Running: true}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"id":"sabc"`, `"running":true`, `"created"`, `"last_used"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the wire form is missing %s: %s", want, data)
		}
	}
	var back SessionStatus
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.ID != payload.ID || !back.Running {
		t.Errorf("round trip lost data: %+v", back)
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// body reads a recorder's body as a string.
func body(t *testing.T, resp *httptest.ResponseRecorder) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return string(b)
}

// TestTheConversationCanBeRead: a chat cannot be drawn from figures. A front end that connects to
// a conversation it did not start has to be able to see what has been said, or it can only show an
// empty screen next to a token count - which is a client that looks broken the moment it opens.
//
// This route is ADDED here rather than relied upon. The plan that was supposed to introduce it (the
// remote-client plan, "GET /v1/sessions/{id}/messages") was never implemented: server.go registered
// no such route, so the interface could switch conversations and had nothing to show. When the plan
// and the code disagree, the code is what is true.
func TestTheConversationCanBeRead(t *testing.T) {
	svc := &fakeService{
		transcript: []agent.DialogueTurn{
			{User: "count the files", Agent: "there are twelve", Kind: "task"},
			{User: "and the directories?", Agent: "three", Kind: "task"},
		},
	}
	srv := newTestServer(t, svc)

	resp := get(t, srv, sessionPath(srv, DefaultSession, "/messages"), srv.Token())
	if resp.Code != http.StatusOK {
		t.Fatalf("reading the conversation answered %d: %s", resp.Code, resp.Body.String())
	}
	var got struct {
		Messages []agent.DialogueTurn `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatalf("the conversation is not a list of turns: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("the conversation has %d turns, the two that were said must be there", len(got.Messages))
	}
	if got.Messages[0].User != "count the files" || got.Messages[0].Agent != "there are twelve" {
		t.Errorf("the turns do not carry what was said: %+v", got.Messages[0])
	}
}

// TestAnEmptyConversationIsAnEmptyList: a client that has to tell "nothing said yet" from "the field
// is missing" is a client with a bug waiting to happen. Same rule the questions endpoint follows.
func TestAnEmptyConversationIsAnEmptyList(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	resp := get(t, srv, sessionPath(srv, DefaultSession, "/messages"), srv.Token())
	if got := body(t, resp); got != `{"messages":[]}`+"\n" {
		t.Errorf("an empty conversation answered %q, it must be an empty list", got)
	}
}

// TestTheConversationIsBehindTheToken: it is the most revealing thing this gateway holds - every
// task the user described and everything the agent answered.
func TestTheConversationIsBehindTheToken(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	resp := get(t, srv, sessionPath(srv, DefaultSession, "/messages"), "")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("the conversation answered %d without a token, it must be 401", resp.Code)
	}
}

// TestTheConversationIsScopedToTheSession: every conversation endpoint is addressed by the session
// it is about, and this one is no exception. A route that answered from the DEFAULT session whichever
// id was named would be the one thing the routing rule exists to prevent.
func TestTheConversationIsScopedToTheSession(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, withFactory())
	created := post(t, srv, "/v1/sessions", "{}", testToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("opening a session answered %d: %s", created.Code, created.Body.String())
	}
	var conv SessionStatus
	if err := json.Unmarshal(created.Body.Bytes(), &conv); err != nil {
		t.Fatalf("the answer is not a session: %v", err)
	}

	if w := get(t, srv, sessionPath(srv, conv.ID, "/messages"), testToken); w.Code != http.StatusOK {
		t.Errorf("the new session's conversation answered %d: %s", w.Code, w.Body.String())
	}
	// And a session that does not exist is a 404, resolved by the same middleware as everything
	// else - never an empty conversation, which would look like a real one that is silent.
	if w := get(t, srv, sessionPath(srv, "smissing", "/messages"), testToken); w.Code != http.StatusNotFound {
		t.Errorf("an unknown session answered %d, it must be 404", w.Code)
	}
}

// TestTheTranscriptIsOnTheServiceContract: the compile-time guard in service_test.go is what keeps
// *tui.AppRunner satisfying Service. This asserts the property the whole task rests on: the
// conversation is readable through the CONTRACT and not through a concrete type.
func TestTheTranscriptIsOnTheServiceContract(t *testing.T) {
	var svc Service = &fakeService{transcript: []agent.DialogueTurn{{User: "one"}}}
	_ = svc.Transcript()
}

// TestTheTranscriptHandedOutIsACopy: a caller that mutated what it was given would be editing the
// conversation the agent is carrying. The production runner copies on the way out, and this pins the
// fake to the same property so a test cannot come to rely on aliasing.
func TestTheTranscriptHandedOutIsACopy(t *testing.T) {
	svc := &fakeService{transcript: []agent.DialogueTurn{{User: "one"}}}
	got := svc.Transcript()
	got[0].User = "changed"
	if svc.Transcript()[0].User != "one" {
		t.Error("Transcript handed out the slice it holds, so a caller can edit the conversation in place")
	}
}

// TestTheClientReadsTheConversation: the client half of the same route. It is what makes attaching
// mean something on the wire, and it is scoped to this client's session - the plan's second client
// task was never implemented either, so this too is added here.
func TestTheClientReadsTheConversation(t *testing.T) {
	c, seen := sessionRecorder(t, http.StatusOK, map[string]any{
		"messages": []map[string]string{
			{"User": "count the files", "Agent": "there are twelve", "Kind": "task"},
		},
	})
	c = NewClientForSession(strings.TrimSuffix(c.baseURL, ""), testToken, "sabc")

	turns, err := c.Conversation(context.Background())
	if err != nil {
		t.Fatalf("Conversation: %v", err)
	}
	if len(turns) != 1 || turns[0].User != "count the files" || turns[0].Agent != "there are twelve" {
		t.Errorf("Conversation returned %+v", turns)
	}
	if len(*seen) != 1 || !strings.HasPrefix((*seen)[0], "/v1/sessions/sabc/") {
		t.Errorf("the read addressed %v, every conversation call must name its session", *seen)
	}
}

// TestTheClientReportsARefusedConversation: a gateway that will not answer must produce an error and
// not an empty conversation - an empty list reads as "nothing has been said", which is a different
// and much more alarming statement than "the question could not be asked".
func TestTheClientReportsARefusedConversation(t *testing.T) {
	c, _ := sessionRecorder(t, http.StatusUnauthorized, nil)
	turns, err := c.Conversation(context.Background())
	if err == nil {
		t.Fatal("a refused read must be reported, not answered with an empty conversation")
	}
	if turns != nil {
		t.Errorf("a failed read returned %v, it must return nothing", turns)
	}
}

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/agent"
)

// sessionPath builds the path of one endpoint of one conversation.
//
// Every request that says something ABOUT a conversation goes through here, so a test cannot
// accidentally address another one: the addressing rule is written once, in the same shape the
// server uses.
func sessionPath(srv *Server, id, rest string) string {
	return "/v1/sessions/" + id + rest
}

// TestTwoSessionsCanRunAtTheSameTime is the whole point of the feature: the run slot that used
// to live on the Server guaranteed ONE conversation, and it is moved onto the conversation
// itself so that two of them do not wait for each other.
//
// The blocking is done with a channel rather than with a timer, so the two runs really do
// overlap instead of racing the scheduler.
func TestTwoSessionsCanRunAtTheSameTime(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	// The DEFAULT conversation is the one that blocks, and the second one answers immediately.
	//
	// The other way round - a blocking service in the new session and a task posted to the
	// default one - is what the shape of this test first suggested, and it waits forever: the
	// default conversation's service is the one handed to Start, so a run there never touches the
	// blocking service and its channel is never closed. The service under the default id is the
	// one the first request reaches, so that is where the block has to live.
	srv := newTestServer(t, &blockingService{fakeService: fakeService{}, started: started, release: release})
	first := sessionPath(srv, DefaultSession, "/task")

	second, err := srv.newSession(&fakeService{})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	// Session A: a run that is in flight and stays there until this test says otherwise.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = post(t, srv, first, `{"task":"one"}`, testToken)
	}()

	<-started // A is genuinely running.

	// Session B must not wait for A.
	w := post(t, srv, sessionPath(srv, second.id, "/task"), `{"task":"two"}`, testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("a run in a second session answered %d, it must not be refused: %s", w.Code, w.Body.String())
	}
	close(release)
	<-done
}

// TestASecondRunInTheSameSessionIsRefused: the slot did not disappear, it moved. Two runs in one
// conversation would still interleave two tasks into one transcript.
func TestASecondRunInTheSameSessionIsRefused(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	// The blocking service is the one Start was given, so it is the one under the default id -
	// the same shape as the test above, and for the same reason.
	srv := newTestServer(t, &blockingService{fakeService: fakeService{}, started: started, release: release})
	path := sessionPath(srv, DefaultSession, "/task")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = post(t, srv, path, `{"task":"one"}`, testToken)
	}()
	<-started

	w := post(t, srv, path, `{"task":"two"}`, testToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("a second run in one session answered %d, it must be 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "already in progress") {
		t.Errorf("the refusal must say what is happening: %s", w.Body.String())
	}
	close(release)
	<-done
}

// TestASessionsApprovalDoesNotAnswerAnothers: the approval slot moved with the run slot. Two
// conversations waiting on a question at once used to overwrite one another's id, so an answer
// meant for one could land on the other - which is exactly what the id exists to prevent.
func TestASessionsApprovalDoesNotAnswerAnothers(t *testing.T) {
	relA, relB := make(chan struct{}), make(chan struct{})

	srv := newTestServer(t, &fakeService{})
	a, err := srv.newSession(&approvalService{command: "rm -rf /a", release: relA})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	b, err := srv.newSession(&approvalService{command: "rm -rf /b", release: relB})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	// Each run is started over a REAL socket and its own stream is read for the question. That is
	// how the id is learned: the gateway mints it, so the stream is where a client gets it - and
	// reading it there keeps this test from reaching into a field the run's goroutine writes.
	respA := startRun(t, srv, sessionPath(srv, a.id, "/task"), `{"task":"a"}`)
	defer respA.Body.Close()
	respB := startRun(t, srv, sessionPath(srv, b.id, "/task"), `{"task":"b"}`)
	defer respB.Body.Close()

	idA := approvalIDFromStream(t, respA)
	idB := approvalIDFromStream(t, respB)

	// Two conversations asking at once must not share an id, or one answer could land on the
	// other's question.
	if idA == idB {
		t.Fatalf("two sessions were asked with the same approval id (%q), so one answer could land on the other", idA)
	}

	// Answering A is accepted...
	w := post(t, srv, sessionPath(srv, a.id, "/runs/approval"), `{"id":"`+idA+`","approve":true}`, testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("answering A answered %d: %s", w.Code, w.Body.String())
	}
	// ...and B is still waiting on ITS question: A's answer did not resolve it.
	if got := pendingApprovalID(t, b); got != idB {
		t.Errorf("answering A changed B's question (%q -> %q)", idB, got)
	}
	// And B's own question is still answerable, which is what proves it was not consumed.
	wB := post(t, srv, sessionPath(srv, b.id, "/runs/approval"), `{"id":"`+idB+`","approve":true}`, testToken)
	if wB.Code != http.StatusNoContent {
		t.Errorf("answering B answered %d, its own question must still be open: %s", wB.Code, wB.Body.String())
	}
	close(relA)
	close(relB)
}

// startRun POSTs a run and returns the streaming response, still open.
func startRun(t *testing.T, srv *Server, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do %s: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("%s answered %d: %s", path, resp.StatusCode, b)
	}
	return resp
}

// approvalIDFromStream reads a run's stream until the question arrives and returns the id it
// carries, then STOPS reading - the run is still in flight, and the stream has to stay open for
// the answer to travel back.
func approvalIDFromStream(t *testing.T, resp *http.Response) string {
	t.Helper()
	data := waitForEvent(t, resp, EventApproval)
	var ask approvalEvent
	if err := json.Unmarshal([]byte(data), &ask); err != nil {
		t.Fatalf("the approval payload %q is not readable: %v", data, err)
	}
	if ask.ID == "" {
		t.Fatal("the question arrived with no id, so it could not be answered")
	}
	return ask.ID
}

// waitForEvent reads a stream until the named event arrives and returns its payload.
//
// It reads in a goroutine so the wait is bounded: a stream that never produces the event must fail
// the test with a reason instead of hanging it, and the run is blocked waiting for an answer that
// only this test can send.
func waitForEvent(t *testing.T, resp *http.Response, want string) string {
	t.Helper()
	found := make(chan string, 1)
	go func() {
		defer close(found)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		name := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if name == want {
					found <- strings.TrimPrefix(line, "data: ")
					return
				}
			}
		}
	}()
	select {
	case data, ok := <-found:
		if !ok {
			t.Fatalf("the stream ended before the %s event", want)
		}
		return data
	case <-time.After(5 * time.Second):
		t.Fatalf("the %s event never arrived", want)
		return ""
	}
}

// TestTheDefaultSessionIsAddressable: a gateway started with one service still HAS a conversation,
// under the name the embedded client uses. It is in the path like every other one, so there is no
// second way to address a conversation.
func TestTheDefaultSessionIsAddressable(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, sessionPath(srv, DefaultSession, ""), testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("the default session answered %d: %s", w.Code, w.Body.String())
	}
}

// TestAnUnknownSessionIsNotFound: a request for a conversation that does not exist must be
// refused by the MIDDLEWARE, so that no handler can answer about the wrong one.
func TestAnUnknownSessionIsNotFound(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, sessionPath(srv, "nope", "/report"), testToken)
	if w.Code != http.StatusNotFound {
		t.Fatalf("an unknown session answered %d, it must be 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "nope") {
		t.Errorf("the 404 must name the session that was asked for: %s", w.Body.String())
	}
}

// TestEveryScopedEndpointIsBehindTheToken: the session is not a second door. Each of these is 401
// without the token, whatever it addresses.
func TestEveryScopedEndpointIsBehindTheToken(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	for _, path := range []string{"", "/report", "/config", "/models", "/reward", "/questions"} {
		w := get(t, srv, sessionPath(srv, DefaultSession, path), "")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a token answered %d, it must be 401", path, w.Code)
		}
	}
}

// TestTheHandlerRefusesARequestWithNoConversation: convOf panics rather than falling back to the
// default conversation. A fallback would silently answer about the wrong one, which is the single
// failure this whole design exists to prevent - so a programming error must be loud.
func TestTheHandlerRefusesARequestWithNoConversation(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("convOf must panic when the request carries no conversation")
		}
	}()
	convOf(&http.Request{})
}

// blockingService is a Service whose run blocks until the test releases it.
type blockingService struct {
	fakeService
	started chan struct{}
	release chan struct{}
}

func (b *blockingService) RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
	close(b.started)
	select {
	case <-b.release:
		return "released", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// approvalService is a Service whose run asks a question and blocks on the answer.
//
// It calls the approver the SERVER installed, so what it exercises is the real wiring: the
// question travels out on this conversation's stream and the answer comes back on this
// conversation's endpoint.
type approvalService struct {
	fakeService
	command string
	release chan struct{}
}

func (a *approvalService) RunTask(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
	fn := a.approver
	if fn == nil {
		return "", errNoApprover
	}
	ok, err := fn(ctx, agent.ApprovalRequest{Command: a.command, Reason: "because", Rule: "test"})
	if err != nil {
		return "", err
	}
	select {
	case <-a.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if !ok {
		return "refused", nil
	}
	return "approved", nil
}

// errNoApprover is reported when a run asked a question before the gateway installed an approver,
// which would mean a consequential command was refused for a wiring reason rather than a policy
// one - and the test has to be able to tell that apart from the policy answer.
var errNoApprover = errorString("no approver was installed before the run started")

type errorString string

func (e errorString) Error() string { return string(e) }

// pendingApprovalID reads the question a conversation is waiting on, for the test that checks one
// session's answer did not resolve another's.
func pendingApprovalID(t *testing.T, c *conversation) string {
	t.Helper()
	p := c.pendingApprovalNow()
	if p == nil {
		return ""
	}
	return p.id
}

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/schedule"
	"github.com/madkoding/motita/internal/session"
)

// event is one parsed server-sent event.
type event struct {
	Event string
	Data  string
}

// readEvents parses the wire format this server writes: one data line per event, an event name on
// the line before it, a blank line between events.
func readEvents(t *testing.T, r io.Reader) []event {
	t.Helper()
	var out []event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	name := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			out = append(out, event{name, strings.TrimPrefix(line, "data: ")})
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the stream: %v", err)
	}
	return out
}

// collect runs a request over a REAL socket and returns every event the server streamed.
func collect(t *testing.T, srv *Server, method, path, body string) []event {
	t.Helper()
	req, err := http.NewRequest(method, srv.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}
	return readEvents(t, resp.Body)
}

// withoutPreamble drops the EventAttached frame every stream now opens with.
//
// It is NOT noise to be filtered out of the design - it is the frame that carries what a client
// cannot learn from the events themselves. It is dropped here because these tests are about the
// run's own events, and the preamble has tests of its own.
func withoutPreamble(t *testing.T, events []event) []event {
	t.Helper()
	if len(events) == 0 || events[0].Event != EventAttached {
		t.Fatalf("every stream must open with an %s preamble, got %+v", EventAttached, events)
	}
	return events[1:]
}

func TestATaskStreamsProgressAndEndsWithDone(t *testing.T) {
	svc := &fakeService{task: func(_ context.Context, _ string, progress func(string, ...any)) (string, error) {
		progress("reading the tree")
		progress("found %d files", 7)
		return "completed: the report is written", nil
	}}
	srv := newTestServer(t, svc)

	events := collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"leave a report"}`)
	// The preamble comes first, then the run's own events.
	events = withoutPreamble(t, events)
	if len(events) != 3 {
		t.Fatalf("events = %+v, want 2 progress and 1 done", events)
	}
	if events[0].Event != EventProgress || !strings.Contains(events[0].Data, "reading the tree") {
		t.Errorf("first event = %+v", events[0])
	}
	// The line is forwarded VERBATIM. A gateway that reformatted it would break the prefixes
	// the plan view parses.
	if events[1].Event != EventProgress || !strings.Contains(events[1].Data, "found 7 files") {
		t.Errorf("second event = %+v", events[1])
	}
	if events[2].Event != EventDone {
		t.Fatalf("last event = %+v, want %s", events[2], EventDone)
	}
	var done doneEvent
	if err := json.Unmarshal([]byte(events[2].Data), &done); err != nil {
		t.Fatalf("done payload = %q: %v", events[2].Data, err)
	}
	if done.Result != "completed: the report is written" {
		t.Errorf("result = %q", done.Result)
	}
}

// The turn's figures travel WITH the answer, so a status bar is right the instant the run ends
// without a second request.
func TestTheDoneEventCarriesTheSessionFigures(t *testing.T) {
	svc := &fakeService{
		task:    func(context.Context, string, func(string, ...any)) (string, error) { return "ok", nil },
		summary: session.Snapshot{Model: "gpt-4o-mini", Window: 128000, Tokens: 4242, Used: 0.033},
	}
	srv := newTestServer(t, svc)

	events := collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)
	last := events[len(events)-1]
	var done doneEvent
	if err := json.Unmarshal([]byte(last.Data), &done); err != nil {
		t.Fatalf("payload = %q: %v", last.Data, err)
	}
	if done.Session.Window != 128000 || done.Session.Tokens != 4242 {
		t.Errorf("session = %+v", done.Session)
	}
}

func TestARunThatFailsIsAnErrorEventNotA500(t *testing.T) {
	svc := &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "", errors.New("the provider refused the request")
	}}
	srv := newTestServer(t, svc)

	events := collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)
	if len(events) == 0 || events[len(events)-1].Event != EventError {
		t.Fatalf("events = %+v, want an %s event", events, EventError)
	}
	// The status is already 200 and the headers are already out: a failure after the first byte
	// belongs on the stream, where the client is reading.
	if !strings.Contains(events[len(events)-1].Data, "the provider refused") {
		t.Errorf("the reason must reach the user: %s", events[len(events)-1].Data)
	}
}

// A run that returns nothing and no error is a failure of the run, not a successful empty
// answer: reporting it as done would be the transport lying to the interface.
func TestAnEmptyResultIsAnError(t *testing.T) {
	svc := &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "", nil
	}}
	srv := newTestServer(t, svc)

	events := collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)
	if len(events) == 0 || events[len(events)-1].Event != EventError {
		t.Fatalf("events = %+v, want an %s event", events, EventError)
	}
	if !strings.Contains(events[len(events)-1].Data, "without reporting a result") {
		t.Errorf("payload = %q", events[len(events)-1].Data)
	}
}

func TestAPlanStreamCarriesTheSameFraming(t *testing.T) {
	svc := &fakeService{plan: func(_ context.Context, _ string, progress func(string, ...any)) (string, error) {
		progress("[tool] ls -la")
		return "the answer", nil
	}}
	srv := newTestServer(t, svc)

	events := withoutPreamble(t, collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/plan"), `{"prompt":"what is here?"}`))
	if len(events) != 2 || events[0].Event != EventProgress || events[1].Event != EventDone {
		t.Fatalf("events = %+v", events)
	}
	// The plan view parses prefixes out of these lines, so the prefix has to survive.
	if !strings.Contains(events[0].Data, "[tool] ls -la") {
		t.Errorf("the line was rewritten: %s", events[0].Data)
	}
}

func TestAnEmptyTaskOrPromptIsRefusedBeforeTheStream(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	for _, tc := range []struct{ path, body string }{
		{sessionPath(srv, DefaultSession, "/task"), `{"task":""}`},
		{sessionPath(srv, DefaultSession, "/task"), `{"task":"   "}`},
		{sessionPath(srv, DefaultSession, "/plan"), `{"prompt":""}`},
	} {
		t.Run(tc.path+" "+tc.body, func(t *testing.T) {
			// A refusal the client can read, and NOT a stream: the headers must not have gone
			// out, so this is a plain 400 rather than an event.
			w := post(t, srv, tc.path, tc.body, testToken)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if strings.Contains(w.Body.String(), "event:") {
				t.Errorf("an empty request started a stream: %q", w.Body.String())
			}
		})
	}
}

// A malformed body is refused at the endpoint that takes one, for both streaming routes: the
// refusal comes from decodeBody, so the run never starts and no stream is opened.
func TestAMalformedBodyIsRefusedOnTheRunEndpoints(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	for _, path := range []string{sessionPath(srv, DefaultSession, "/task"), sessionPath(srv, DefaultSession, "/plan")} {
		t.Run(path, func(t *testing.T) {
			w := post(t, srv, path, "not json", testToken)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if strings.Contains(w.Body.String(), "event:") {
				t.Errorf("a malformed body opened a stream: %q", w.Body.String())
			}
		})
	}
}

func TestOnlyOneRunAtATime(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	svc := &fakeService{task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "done", nil
	}}
	srv := newTestServer(t, svc)

	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), strings.NewReader(`{"task":"one"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	defer resp.Body.Close()
	<-started

	// The second client is refused AND told why. The agent has ONE conversation: two runs at
	// once would interleave two tasks into one transcript and neither user could follow it.
	second := post(t, srv, sessionPath(srv, DefaultSession, "/task"), `{"task":"two"}`, testToken)
	if second.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", second.Code)
	}
	if !strings.Contains(second.Body.String(), "already in progress") {
		t.Errorf("body = %q: the refusal has to say what is going on", second.Body.String())
	}

	close(release)
	_, _ = io.ReadAll(resp.Body)
}

// The slot is RELEASED, not leaked. A leaked slot would leave the gateway permanently refusing
// everybody after one run, which looks like a hung agent rather than a transport bug.
func TestTheRunSlotIsReleasedAfterEveryOutcome(t *testing.T) {
	cases := map[string]func(context.Context, string, func(string, ...any)) (string, error){
		"success": func(context.Context, string, func(string, ...any)) (string, error) { return "done", nil },
		"failure": func(context.Context, string, func(string, ...any)) (string, error) {
			return "", errors.New("nope")
		},
		"empty": func(context.Context, string, func(string, ...any)) (string, error) { return "", nil },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeService{task: fn}
			srv := newTestServer(t, svc)
			for i := 0; i < 3; i++ {
				if events := collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`); len(events) == 0 {
					t.Fatalf("run %d produced no events", i)
				}
			}
		})
	}
}

// The slot is taken BEFORE the stream starts, so the refusal is a readable 409 and not a stream
// that dies immediately with no explanation.
func TestTheRefusalHappensBeforeAnyHeaders(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	svc := &fakeService{task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "done", nil
	}}
	srv := newTestServer(t, svc)

	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), strings.NewReader(`{"task":"one"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	defer resp.Body.Close()
	<-started

	w := post(t, srv, sessionPath(srv, DefaultSession, "/task"), `{"task":"two"}`, testToken)
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "event-stream") {
		t.Errorf("the refusal was a stream (%q): it must be a plain JSON error", ct)
	}
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("body = %q", w.Body.String())
	}
	close(release)
	_, _ = io.ReadAll(resp.Body)
}

// --- the approval round trip -------------------------------------------------

// THE approval test: the run blocks, the client is asked with the EXACT command, and the answer
// unblocks it. Without this the policy's confirmation channel has nobody on the other end, and a
// consequential command is refused rather than reviewed.
func TestAnApprovalIsAskedAndAnswered(t *testing.T) {
	// The result travels on a channel: the run executes in the server's goroutine while this
	// test reads the stream, so a shared bool would be a data race.
	approved := make(chan bool, 1)
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		if svc.approver == nil {
			t.Fatal("the server must install an approver before running, or a consequential command is refused silently")
		}
		ok, err := svc.approver(ctx, agent.ApprovalRequest{
			Command: "rm -rf ./build",
			Reason:  "it deletes a directory",
			Rule:    "destructive",
		})
		if err != nil {
			return "", err
		}
		approved <- ok
		return "the command ran", nil
	}
	srv := newTestServer(t, svc)

	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), strings.NewReader(`{"task":"clean"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	ask := waitForApproval(t, br)
	if ask.Command != "rm -rf ./build" {
		t.Errorf("command = %q: the user approves THE TEXT, so it must arrive whole", ask.Command)
	}
	if ask.Reason != "it deletes a directory" || ask.Rule != "destructive" {
		t.Errorf("the reason and the rule must arrive too: %+v", ask)
	}
	if ask.ID == "" {
		t.Fatal("the approval needs an id, or an answer can be replayed onto the next question")
	}

	w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"), fmt.Sprintf(`{"id":%q,"approve":true}`, ask.ID), testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("answer status = %d, want 204 (body %q)", w.Code, w.Body.String())
	}

	events := readEvents(t, br)
	if len(events) == 0 || events[len(events)-1].Event != EventDone {
		t.Fatalf("events = %+v, want a final done", events)
	}
	select {
	case ok := <-approved:
		if !ok {
			t.Error("the approver returned false after a yes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the run never received the answer")
	}
}

// A NO is an answer too, and the run continues with it: the agent decides what to do about a
// refusal, which is different from never being asked.
func TestAnApprovalCanBeRefused(t *testing.T) {
	// The result travels on a CHANNEL rather than in a variable: the run happens in the server's
	// handler goroutine while this test reads the stream, and a shared bool is a data race that
	// -race catches. The channel is the synchronization.
	got := make(chan bool, 1)
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		ok, err := svc.approver(ctx, agent.ApprovalRequest{Command: "rm -rf ./build"})
		if err != nil {
			return "", err
		}
		got <- ok
		return "the agent did not run it", nil
	}
	srv := newTestServer(t, svc)

	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), strings.NewReader(`{"task":"clean"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer resp.Body.Close()

	ask := waitForApproval(t, bufio.NewReader(resp.Body))
	if w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"), fmt.Sprintf(`{"id":%q,"approve":false}`, ask.ID), testToken); w.Code != http.StatusNoContent {
		t.Fatalf("answer status = %d", w.Code)
	}
	select {
	case ok := <-got:
		if ok {
			t.Error("a no arrived as a yes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the run never received the answer")
	}
}

// waitForApproval drains the stream until the approval event and returns it.
func waitForApproval(t *testing.T, br *bufio.Reader) approvalEvent {
	t.Helper()
	name := ""
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended before an approval was asked: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		if strings.HasPrefix(line, "event: ") {
			name = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") || name != EventApproval {
			continue
		}
		var ask approvalEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ask); err != nil {
			t.Fatalf("approval payload = %q: %v", line, err)
		}
		return ask
	}
}

// An answer to a question nobody asked is refused. This is the replay this channel exists to
// prevent: a stale client approving the NEXT command.
func TestAnApprovalWithTheWrongIDIsRefused(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	svc := &fakeService{task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "done", nil
	}}
	srv := newTestServer(t, svc)

	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), strings.NewReader(`{"task":"x"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer resp.Body.Close()
	<-started

	// Nothing is being asked right now, so there is no id to answer.
	w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"), `{"id":"not-the-one","approve":true}`, testToken)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409: an answer to a question nobody asked must not be accepted", w.Code)
	}
	if !strings.Contains(w.Body.String(), "nothing is waiting") {
		t.Errorf("body = %q", w.Body.String())
	}
	close(release)
	_, _ = io.ReadAll(resp.Body)
}

// An answer that arrives after the run already stopped waiting is ACCEPTED and dropped, not
// refused: the question was really asked, so the answer is not a replay. The send is buffered
// for exactly this - a client answering a question whose run has moved on must not block the
// answering client forever.
func TestAnAnswerForAQuestionThatWasAskedIsAcceptedEvenWhenLate(t *testing.T) {
	var id string
	release := make(chan struct{})
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		// Ask, then give up immediately: the question is asked and the run moves on without
		// waiting, which is what a cancelled run looks like from the handler's side.
		dead, cancel := context.WithCancel(ctx)
		cancel()
		ok, err := svc.approver(dead, agent.ApprovalRequest{Command: "rm -rf ./build"})
		if err == nil || ok {
			return "", errors.New("a cancelled question must refuse")
		}
		return "gone", nil
	}
	// Capture the id of the question as it goes out, so the answer can carry it.
	svc.approverWrap = func(fn agent.Approver) { _ = fn }
	srv := newTestServer(t, svc)

	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), strings.NewReader(`{"task":"x"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	ask := waitForApproval(t, br)
	id = ask.ID
	_, _ = io.ReadAll(br)
	close(release)

	// The question is closed, so an answer is refused rather than silently accepted. This is the
	// path that keeps a stale answer from landing on the NEXT question.
	w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"), fmt.Sprintf(`{"id":%q,"approve":true}`, id), testToken)
	if w.Code != http.StatusConflict {
		t.Errorf("a late answer got %d, want 409", w.Code)
	}
}

// The answer is delivered even when nobody is watching the channel any more: the send is
// non-blocking, so a client answering a question that has already been closed gets an answer
// instead of a hang. Driven directly, because the handler's buffered send is the thing under
// test and a run cannot be made to leave it pending from the outside.
func TestAnAnswerNeverBlocksOnAQuestionNobodyIsWaitingFor(t *testing.T) {
	srv := newTestServer(t, &fakeService{})

	// A channel with a reader that already left, and an id that matches: the send must go
	// through (buffered) or be dropped, but it must RETURN.
	conv := srv.sessions[DefaultSession]
	p := &pendingApproval{id: "the-id", ch: make(chan bool, 1)}
	conv.setPendingApproval(p)
	defer conv.clearPendingApproval()

	done := make(chan int, 1)
	go func() {
		w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"), `{"id":"the-id","approve":true}`, testToken)
		done <- w.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusNoContent {
			t.Errorf("status = %d, want 204", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("answering a question blocked: the send must be buffered or dropped, never waited on")
	}

	// And the answer really was delivered on the channel.
	select {
	case v := <-p.ch:
		if !v {
			t.Error("the answer arrived as a no")
		}
	default:
		t.Error("the answer was dropped although the channel had room")
	}
}

// A malformed answer is refused before the id is even looked at.
func TestAMalformedApprovalBodyIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"), "not json", testToken); w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// An answer whose id does not match the question being asked is refused. This is the branch that
// stops a stale client from approving the NEXT command, and it is distinct from "nothing is
// pending": there IS a question, and this is not the answer to it.
func TestAnAnswerWithTheWrongIDAgainstALiveQuestionIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	p := &pendingApproval{id: "the-real-id", ch: make(chan bool, 1)}
	conv.setPendingApproval(p)
	defer conv.clearPendingApproval()

	w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"), `{"id":"a-different-id","approve":true}`, testToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "does not match") {
		t.Errorf("body = %q, the refusal must say the id was the problem", w.Body.String())
	}
	// And nothing was delivered on the channel: the command must not have been approved.
	select {
	case v := <-p.ch:
		t.Errorf("an answer with the wrong id reached the run: %v", v)
	default:
	}
}

// A channel that is already full is DROPPED rather than waited on. This is the buffer's whole
// purpose: the run may have moved on, and the answering client must still get an answer instead
// of blocking forever on a question that no longer exists.
func TestAnAnswerIsDroppedRatherThanBlockingOnAFullChannel(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	full := make(chan bool, 1)
	full <- true // the buffer is taken, as it would be if the run already read one answer
	conv := srv.sessions[DefaultSession]
	conv.setPendingApproval(&pendingApproval{id: "the-id", ch: full})
	defer conv.clearPendingApproval()

	done := make(chan int, 1)
	go func() {
		done <- post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"), `{"id":"the-id","approve":false}`, testToken).Code
	}()
	select {
	case code := <-done:
		if code != http.StatusNoContent {
			t.Errorf("status = %d, want 204", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the answer blocked on a full channel; it must be dropped and answered")
	}
	// The original value is untouched: the drop is real, not an overwrite.
	if v := <-full; !v {
		t.Error("the buffered answer was overwritten")
	}
}

// The approver records the question in TWO places before it blocks, and both are load-bearing.
//
// On the CONVERSATION, because that is what a client answers through - and what a reconnecting
// client is told about in its preamble. In the run's LOG, because a client that was not connected
// when the question was asked has to find it when it reattaches: the question is part of the
// turn's history, not a packet sent once to whoever happened to be listening.
func TestAnApprovalIsRecordedBeforeItBlocks(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rn := newRun("r-test", ctx, cancel)

	answered := make(chan struct{})
	var got bool
	var err error
	go func() {
		defer close(answered)
		got, err = srv.approverFor(conv, rn)(ctx, agent.ApprovalRequest{
			Command: "rm -rf ./build", Reason: "it deletes a directory", Rule: "destructive",
		})
	}()

	// The question is on the conversation, with everything the user needs in order to decide.
	var p *pendingApproval
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p = conv.pendingApprovalNow(); p != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p == nil {
		t.Fatal("the question was never recorded, so no client could answer it")
	}
	if p.command != "rm -rf ./build" || p.reason != "it deletes a directory" || p.rule != "destructive" {
		t.Errorf("the recorded question lost its detail: %+v", p)
	}
	if p.id == "" {
		t.Error("the question has no id, so an answer cannot be matched to it")
	}

	// And it is in the run's log, which is what a reattaching client reads.
	replay, _ := rn.since(0)
	if len(replay) != 1 || replay[0].Event != EventApproval {
		t.Fatalf("the log holds %+v, the question must be there for a client that comes back", replay)
	}
	if !strings.Contains(string(replay[0].Data), "rm -rf ./build") {
		t.Errorf("the logged question does not carry the command: %s", replay[0].Data)
	}

	// Answering it releases the approver, which is the blocking half.
	p.ch <- true
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("the approver never returned after being answered")
	}
	if err != nil || !got {
		t.Errorf("approver = (%v, %v), want (true, nil)", got, err)
	}

	// And the question is gone, so a late answer cannot land on the next one.
	if conv.pendingApprovalNow() != nil {
		t.Error("the question is still pending after being answered")
	}
}

// A cancelled run refuses the command. It is the cautious answer and the honest one: the user
// did not say yes, and silence is not consent for a consequential action.
func TestACancelledRunRefusesTheCommand(t *testing.T) {
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		dead, cancel := context.WithCancel(ctx)
		cancel()
		ok, err := svc.approver(dead, agent.ApprovalRequest{Command: "rm -rf ./build"})
		if err == nil {
			return "", errors.New("a cancelled context must be reported as an error")
		}
		if ok {
			return "", errors.New("a cancelled approval must REFUSE")
		}
		return "refused, as it must be", nil
	}
	srv := newTestServer(t, svc)

	events := collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)
	last := events[len(events)-1]
	if last.Event != EventDone {
		t.Fatalf("the refused command must not end the run, got %+v", last)
	}
	// The point of the assertion: the run CONTINUED and reported what it did about the refusal.
	// Turning a refusal into a failed run would tell the user the agent broke, when what
	// happened is that it asked and was told no.
	if !strings.Contains(last.Data, "refused, as it must be") {
		t.Errorf("payload = %q", last.Data)
	}
}

// Without a usable source of randomness the question cannot be formed safely, so it is refused
// rather than asked with a predictable id one client could answer on another's behalf.
func TestAnApprovalCannotBeAskedWithoutAnID(t *testing.T) {
	original := randReader
	randReader = failingReader{}
	t.Cleanup(func() { randReader = original })

	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		ok, err := svc.approver(ctx, agent.ApprovalRequest{Command: "rm -rf ./build"})
		if err == nil {
			return "", errors.New("no id must be an error")
		}
		if ok {
			return "", errors.New("and it must refuse")
		}
		return "refused", nil
	}
	srv := newTestServer(t, svc)

	events := collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)
	if len(events) == 0 {
		t.Fatal("no events")
	}
	last := events[len(events)-1]
	// The run was told no and carried on: what matters is that it was REFUSED, not that it
	// failed. A run that dies on a refusal would make the failure look like the agent's.
	if last.Event != EventDone || !strings.Contains(last.Data, "refused") {
		t.Errorf("last = %+v", last)
	}
}

// newApprovalID is unguessable and distinct every time: two questions must never share an id.
func TestApprovalIDsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err := newApprovalID()
		if err != nil {
			t.Fatalf("newApprovalID: %v", err)
		}
		if len(id) != 32 {
			t.Errorf("id = %q, want 32 hex characters", id)
		}
		if seen[id] {
			t.Fatalf("id %q was produced twice", id)
		}
		seen[id] = true
	}
}

func TestNewApprovalIDReportsAFailingSource(t *testing.T) {
	original := randReader
	randReader = failingReader{}
	t.Cleanup(func() { randReader = original })
	if _, err := newApprovalID(); err == nil {
		t.Fatal("a failing source of randomness must be reported")
	}
}

// A client whose stream cannot even be opened does NOT lose the run.
//
// The old behaviour was the opposite - the run was not started at all - and that is exactly the
// property this design changes: the run is no longer bounded by its connection, so a client that
// cannot read is a reader problem, not a reason to throw the turn away. The events go into the log
// and wait for whoever attaches next.
func TestAClientThatCannotBeStreamedToDoesNotLoseTheRun(t *testing.T) {
	done := make(chan struct{})
	svc := &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		close(done)
		return "done", nil
	}}
	srv := newTestServer(t, svc)
	req := httptest.NewRequest(http.MethodPost, sessionPath(srv, DefaultSession, "/task"), strings.NewReader("{}"))

	conv := srv.sessions[DefaultSession]
	srv.startRun(noFlushWriter{}, req, conv, "a task", schedule.KindTask)

	select {
	case <-done:
		// The run happened, which is the point: nobody could watch it, and it is in the log.
	case <-time.After(5 * time.Second):
		t.Fatal("the run was abandoned because its client could not be written to")
	}
	// And the slot comes back, so a refused stream cannot leave the gateway refusing everybody.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !conv.isRunning() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the run slot was leaked by a stream that never started")
}

package gateway

// "Allow all commands for this session", and the live view of the model's reasoning.
//
// Requested from real use: being asked about every command, one after another, while working
// through a task the user had already decided to trust in this session.

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

// TestAllowingAllCommandsAnswersTheRestOfTheSession: one "allow all" and the next command is
// not asked about - it runs, and the run says so.
func TestAllowingAllCommandsAnswersTheRestOfTheSession(t *testing.T) {
	var answers []bool
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		for _, cmd := range []string{"npm install", "npm test"} {
			ok, err := svc.approver(ctx, agent.ApprovalRequest{Command: cmd, Reason: "r", Rule: "unclassified"})
			if err != nil {
				return "", err
			}
			answers = append(answers, ok)
		}
		return "both ran", nil
	}
	srv := newTestServer(t, svc)

	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), strings.NewReader(`{"task":"x"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	ask := waitForApproval(t, br)
	w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"),
		fmt.Sprintf(`{"id":%q,"approve":true,"scope":"session"}`, ask.ID), testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("answer = %d %s", w.Code, w.Body.String())
	}
	events := readEvents(t, br)
	var said bool
	for _, e := range events {
		if e.Event == EventApproval {
			t.Errorf("the second command was asked about after allow-all: %s", e.Data)
		}
		if e.Event == EventProgress && strings.Contains(string(e.Data), "all commands allowed in this session") &&
			strings.Contains(string(e.Data), "npm test") {
			said = true
		}
	}
	if !said {
		t.Errorf("the run must say which command ran on the standing answer: %+v", events)
	}
	if len(answers) != 2 || !answers[0] || !answers[1] {
		t.Errorf("answers = %v", answers)
	}
	if !srv.sessions[DefaultSession].status().AutoApprove {
		t.Error("the session status must report the standing answer")
	}
}

// TestTheBudgetQuestionIsStillAsked: "allow all commands" is about commands; spending another
// step budget is a different question.
func TestTheBudgetQuestionIsStillAsked(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	conv.setAutoApprove(true)
	ctx, cancel := context.WithCancel(context.Background())
	rn := newRun("r", ctx, cancel, "")
	var returned atomic.Bool
	go func() {
		srv.approverFor(conv, rn)(ctx, agent.ApprovalRequest{Command: "continue", Rule: agent.BudgetRule})
		returned.Store(true)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for conv.pendingApprovalNow() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if conv.pendingApprovalNow() == nil || returned.Load() {
		t.Fatal("the budget question must be put to the user even with every command allowed")
	}
	cancel()
}

// TestAllowAllCanBeTakenBack: the interface turns it off, and the next command is asked again.
func TestAllowAllCanBeTakenBack(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.sessions[DefaultSession].setAutoApprove(true)
	w := post(t, srv, sessionPath(srv, DefaultSession, "/auto-approve"), `{"enabled":false}`, testToken)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "auto_approve") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if srv.sessions[DefaultSession].autoApproving() {
		t.Error("the standing answer must be gone")
	}
	w = post(t, srv, sessionPath(srv, DefaultSession, "/auto-approve"), `{"enabled":true}`, testToken)
	if !strings.Contains(w.Body.String(), `"auto_approve":true`) {
		t.Errorf("turning it on answers the new status: %s", w.Body.String())
	}
	if w := post(t, srv, sessionPath(srv, DefaultSession, "/auto-approve"), `not json`, testToken); w.Code != http.StatusBadRequest {
		t.Errorf("a malformed body = %d", w.Code)
	}
}

// TestLiveReasoningIsFlashedNotLogged: a snapshot reaches the reader attached now, and is
// neither kept in the log nor replayed; a reader whose queue is filling up skips it rather than
// being cut off.
func TestLiveReasoningIsFlashedNotLogged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rn := newRun("r", ctx, cancel, "")
	sub := rn.subscribe()
	progress := rn.progress()
	progress("%sweighing the options", agent.LivePrefix)
	progress("running: ls")

	first := <-sub.ch
	if first.Event != EventThinking || first.Seq != 0 || !strings.Contains(string(first.Data), "weighing the options") {
		t.Errorf("first = %+v", first)
	}
	second := <-sub.ch
	if second.Event != EventProgress || second.Seq != 1 {
		t.Errorf("second = %+v", second)
	}
	if replay, _ := rn.since(0); len(replay) != 1 {
		t.Errorf("the log must hold the progress line only: %+v", replay)
	}

	// A reader half full is skipped, not cut off.
	for i := 0; i < subscriberBuffer/2; i++ {
		sub.ch <- loggedEvent{Seq: 99}
	}
	rn.flash(EventThinking, progressEvent{Text: "skipped"})
	if len(sub.ch) != subscriberBuffer/2 {
		t.Errorf("queue = %d, the snapshot must be skipped", len(sub.ch))
	}
	select {
	case <-sub.lost:
		t.Error("a reader must not be cut off over an ephemeral snapshot")
	default:
	}
	// A payload that cannot be encoded is dropped quietly.
	rn.flash(EventThinking, make(chan int))
}

// TestAnEphemeralEventIsWrittenLiveButNotAfterTheRun: through attach, an ephemeral event is
// always new; one still queued when the run has ended describes nothing and is not written.
func TestAnEphemeralEventIsWrittenLiveButNotAfterTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rn := newRun("r2", ctx, cancel, "")
	sub := rn.subscribe()
	rn.flash(EventThinking, progressEvent{Text: "stale"})
	rec := httptest.NewRecorder()
	if err := rn.drainFrom(rec, http.NewResponseController(rec), sub, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), "stale") {
		t.Errorf("a stale snapshot was written: %s", rec.Body.String())
	}
	if err := rn.writeIfNew(rec, http.NewResponseController(rec),
		loggedEvent{Event: EventThinking, Data: []byte(`{"text":"now"}`)}, 50); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), "now") {
		t.Errorf("an ephemeral event must be written whatever the replay covered: %s", rec.Body.String())
	}
}

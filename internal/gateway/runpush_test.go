package gateway

import (
	"context"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"net/http"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/i18n"
)

func TestRunMessageSaysWhatTheRunNeeds(t *testing.T) {
	for _, kind := range []string{"approval", "done", "error"} {
		for _, lang := range []i18n.Lang{i18n.EN, i18n.ES} {
			m := runMessage(lang, kind, "make test", "Login page", "s 1")
			wrongHead := !strings.HasSuffix(m.Title, " — Login page") || m.Body == ""
			wrongLink := m.Tag != "motita-run-s 1" || m.URL != "/?session=s+1"
			if wrongHead || wrongLink || strings.Contains(m.Body, "%") {
				t.Errorf("%s/%s = %+v", kind, lang, m)
			}
		}
	}
	if m := runMessage(i18n.ES, "approval", "make test", "", "s"); m.Title != "Motita necesita tu aprobación" {
		t.Errorf("es = %+v", m)
	}
}

// TestAnUnwatchedRunReachesThePhoneAndAWatchedOneDoesNot: a run with no page attached is the one
// nobody will see ask or end; one with a reader attached shows it on screen already.
func TestAnUnwatchedRunReachesThePhoneAndAWatchedOneDoesNot(t *testing.T) {
	srv, tr, _ := newPushServer(t)
	_ = pushCall(t, srv, http.MethodPost, "/v1/push/subscriptions", subscriptionJSON(t, testEndpoint, "es"))
	c := newConversation("s1", &fakeService{})
	rn := newRun("r1", srv.baseCtx, func() {}, "")

	srv.pushUnwatched(c, rn, "done", "")
	srv.pushWG.Wait()
	if tr.count() != 1 {
		t.Fatalf("unwatched sent %d, want 1", tr.count())
	}

	sub := rn.subscribe()
	srv.pushUnwatched(c, rn, "approval", "make test")
	srv.pushWG.Wait()
	rn.unsubscribe(sub)
	if tr.count() != 1 {
		t.Fatalf("watched sent %d, want none more", tr.count())
	}

	srv.unattendedApprover("job")(rn)
	srv.pushUnwatched(c, rn, "done", "")
	srv.pushWG.Wait()
	if tr.count() != 1 || !rn.quiet {
		t.Fatalf("a scheduled run sent %d", tr.count())
	}
}

// TestAnApprovalNobodyAnswersIsRefusedAfterTheTimeout: with agent.approval_timeout set, a question
// left open stops holding the run, says so in its log, and the session reports it while it waits.
func TestAnApprovalNobodyAnswersIsRefusedAfterTheTimeout(t *testing.T) {
	svc := &fakeService{}
	svc.cfg.Agent.ApprovalTimeout = 150 * time.Millisecond
	srv := newTestServer(t, svc)
	conv := srv.sessions[DefaultSession]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rn := newRun("r-timeout", ctx, cancel, "")

	type answer struct {
		ok  bool
		err error
	}
	got := make(chan answer, 1)
	go func() {
		ok, err := srv.approverFor(conv, rn)(ctx, agent.ApprovalRequest{Command: "make deploy", Rule: "network"})
		got <- answer{ok, err}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !conv.status().AwaitingApproval && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !conv.status().AwaitingApproval {
		t.Fatal("the session did not say it was waiting for an approval")
	}
	select {
	case a := <-got:
		if a.ok || a.err == nil || !strings.Contains(a.err.Error(), "nobody answered") {
			t.Fatalf("answer = %+v, want a refusal that says nobody answered", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the unanswered question held the run past its timeout")
	}
	if conv.status().AwaitingApproval {
		t.Error("the session still says it waits after the question was refused")
	}
	events, _ := rn.since(0)
	var denied bool
	for _, e := range events {
		denied = denied || e.Event == EventApprovalDenied
	}
	if !denied {
		t.Error("the refusal was not written to the run's log")
	}
}

// TestTheApprovalTimeoutIsSavedFromTheSettings: it is read back as minutes, needs no confirmation
// (it only narrows what waits), a nonsense value is refused, and a run in flight follows the file.
func TestTheApprovalTimeoutIsSavedFromTheSettings(t *testing.T) {
	withConfinement(t, true)
	path := networkConfig(t, "sandbox:\n  kind: none\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	if v := decodeSecurity(t, get(t, srv, "/v1/security", testToken)); v.ApprovalTimeoutMinutes != 0 {
		t.Fatalf("GET = %+v, want waiting for ever", v)
	}
	v := decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"approval_timeout_minutes":15}`))
	if v.ApprovalTimeoutMinutes != 15 || mustLoad(t, path).Agent.ApprovalTimeout != 15*time.Minute {
		t.Fatalf("PUT = %+v", v)
	}
	if got := srv.approvalTimeout(srv.sessions[DefaultSession]); got != 15*time.Minute {
		t.Errorf("a run asks the file: %s", got)
	}
	for _, bad := range []string{`{"approval_timeout_minutes":-1}`, `{"approval_timeout_minutes":99999}`} {
		if w := sendNetwork(t, srv, http.MethodPut, "/v1/security", bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, w.Code)
		}
	}
	v = decodeSecurity(t, sendNetwork(t, srv, http.MethodPut, "/v1/security", `{"approval_timeout_minutes":0}`))
	if v.ApprovalTimeoutMinutes != 0 {
		t.Fatalf("back to waiting: %+v", v)
	}
}

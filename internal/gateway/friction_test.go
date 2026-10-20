package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/friction"
	"github.com/madkoding/motita/internal/schedule"
)

func TestAFailedRunLeavesAnEntryButACancelledOneDoesNot(t *testing.T) {
	svc := &fakeService{task: func(_ context.Context, task string, _ func(string, ...any)) (string, error) {
		if task == "boom" {
			return "", errors.New("the model said no")
		}
		if task == "cancel" {
			return "", context.Canceled
		}
		return "", nil // no result, no error
	}}
	srv := newTestServer(t, svc)
	for _, task := range []string{"boom", "cancel", "silent"} {
		runTask(t, srv, task)
	}
	got := srv.friction.Entries()
	if len(got) != 2 || got[0].Kind != friction.KindError || got[1].Kind != friction.KindError {
		t.Fatalf("entries = %+v, want two errors and nothing for the cancelled run", got)
	}
	if got[0].Text != "the model said no" || got[0].Session != DefaultSession {
		t.Errorf("first entry = %+v", got[0])
	}
	if !strings.Contains(got[1].Text, "without reporting a result") {
		t.Errorf("second entry = %+v", got[1])
	}
}

func TestARetryLineIsJournaledAndStillStreamed(t *testing.T) {
	svc := recordingService(func(p func(string, ...any)) {
		p("attempt failed: %s", "tests did not pass")
		p("running: go test")
	})
	srv := newTestServer(t, svc)
	runTask(t, srv, "fix it")
	got := srv.friction.Entries()
	if len(got) != 1 || got[0].Kind != friction.KindRetry || got[0].Text != "attempt failed: tests did not pass" {
		t.Fatalf("entries = %+v", got)
	}
	if got[0].Turn != 0 {
		t.Errorf("turn = %d", got[0].Turn)
	}
	steps := checkpointsOf(t, srv)[0].Steps
	if len(steps) < 2 || !strings.Contains(steps[0].Text, "attempt failed") {
		t.Errorf("the retry line did not reach the turn's steps: %+v", steps)
	}
}

func TestARefusedApprovalIsJournaled(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	ask := func() *pendingApproval {
		return &pendingApproval{id: "a", ch: make(chan bool, 1), command: "rm -rf /tmp/x", rule: "destructive"}
	}
	// Approving writes nothing; refusing writes one entry, with or without a run in flight.
	srv.deliverApproval(conv, ask(), true, "")
	if n := len(srv.friction.Entries()); n != 0 {
		t.Fatalf("an approval was journaled as friction: %d", n)
	}
	srv.deliverApproval(conv, ask(), false, "")
	conv.stateMu.Lock()
	conv.current = &run{turn: 4}
	conv.stateMu.Unlock()
	srv.deliverApproval(conv, ask(), false, "")
	got := srv.friction.Entries()
	if len(got) != 2 {
		t.Fatalf("entries = %+v", got)
	}
	first, second := got[0], got[1]
	if first.Kind != friction.KindDenied || first.Why != friction.WhyPerson || first.Turn != 0 {
		t.Fatalf("entries = %+v", got)
	}
	if second.Turn != 4 || second.Text != "rm -rf /tmp/x" {
		t.Fatalf("entries = %+v", got)
	}
}

func TestARunWithNobodyToAskJournalsTheRefusal(t *testing.T) {
	var svc *fakeService
	svc = &fakeService{task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		ok, err := svc.approver(ctx, agent.ApprovalRequest{Command: "git push", Reason: "pushes", Rule: "push"})
		if ok || err == nil {
			t.Errorf("the unattended approver answered (%v, %v)", ok, err)
		}
		return "done", nil
	}}
	srv := newTestServer(t, svc)
	c, _ := srv.lookup(DefaultSession)
	rn, started := srv.startDetachedRun(c, "audit", schedule.KindTask, "", srv.unattendedApprover("s1"))
	if !started {
		t.Fatal("the run did not start")
	}
	<-rn.done
	waitForNoRun(t, srv)
	got := srv.friction.Entries()
	if len(got) != 1 || got[0].Kind != friction.KindDenied || got[0].Why != friction.WhyNobody ||
		got[0].Text != "git push" {
		t.Fatalf("entries = %+v", got)
	}
}

func TestTheSummaryIsServedInTheInterfaceLanguageAndCanBeCleared(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	for i := 0; i < 2; i++ {
		srv.recordFriction(friction.Entry{Kind: friction.KindError, Text: "boom"})
	}
	w := get(t, srv, "/v1/friction", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var v struct {
		Total    int            `json:"total"`
		Counts   map[string]int `json:"counts"`
		Repeated []struct {
			Count int `json:"count"`
		} `json:"repeated"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	week := "Runs that ended with a failure: 2"
	if v.Total != 2 || v.Counts["error"] != 2 || len(v.Repeated) != 1 || !strings.Contains(v.Text, week) {
		t.Errorf("view = %+v", v)
	}

	// The page can ask for its own language, whatever the gateway's is.
	if text := get(t, srv, "/v1/friction?lang=es", testToken).Body.String(); !strings.Contains(text, "Ejecuciones que terminaron") {
		t.Errorf("?lang=es not honoured: %s", text)
	}
	if text := get(t, srv, "/v1/friction?lang=fr", testToken).Body.String(); !strings.Contains(text, "Runs that ended") {
		t.Errorf("an unknown language must fall back: %s", text)
	}
	es := newTestServer(t, &fakeService{}, func(o *Options) { o.UILanguage = "es" })
	es.recordFriction(friction.Entry{Kind: friction.KindError, Text: "boom"})
	spanish := "Ejecuciones que terminaron con un fallo: 1"
	if text := get(t, es, "/v1/friction", testToken).Body.String(); !strings.Contains(text, spanish) {
		t.Errorf("not in Spanish: %s", text)
	}

	if w := send(t, srv, http.MethodDelete, "/v1/friction", testToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("clear: %d", w.Code)
	}
	if n := len(srv.friction.Entries()); n != 0 {
		t.Errorf("%d entries after clearing", n)
	}
}

func TestClearingAJournalThatCannotBeRemovedIsA500(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	path := filepath.Join(t.TempDir(), "j.jsonl")
	srv.friction, _ = friction.Open(path)
	if err := os.MkdirAll(filepath.Join(path, "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if w := send(t, srv, http.MethodDelete, "/v1/friction", testToken, ""); w.Code != http.StatusInternalServerError {
		t.Errorf("clear: %d, want 500", w.Code)
	}
}

func TestAJournalThatCannotBeOpenedOrWrittenDoesNotStopTheGateway(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.FrictionFile = dir }) // a directory is no journal
	srv.recordFriction(friction.Entry{Kind: friction.KindError, Text: "kept in memory"})
	if n := len(srv.friction.Entries()); n != 1 {
		t.Errorf("the fallback journal holds %d entries", n)
	}
	// A file that can no longer be written: its folder was replaced by a file.
	folder := filepath.Join(t.TempDir(), "gone")
	srv.friction, _ = friction.Open(filepath.Join(folder, "j.jsonl"))
	if err := os.WriteFile(folder, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	srv.recordFriction(friction.Entry{Kind: friction.KindError, Text: "still in memory"}) // warns, does not panic
	if n := len(srv.friction.Entries()); n != 1 {
		t.Errorf("an entry that could not be written was lost: %d", n)
	}
}

func TestFlaggingAnAnswerKeepsTheRealContext(t *testing.T) {
	svc := recordingService(func(p func(string, ...any)) {
		p("attempt failed: first try")
		p("running: ls")
	})
	srv := newTestServer(t, svc)
	runTask(t, srv, "rename the file, key sk-"+strings.Repeat("a", 30))

	path := sessionPath(srv, DefaultSession, "/friction")
	if w := post(t, srv, path, `{"turn":0,"note":"it renamed the wrong one"}`, testToken); w.Code != http.StatusNoContent {
		t.Fatalf("flag: %d %s", w.Code, w.Body.String())
	}
	var flagged friction.Entry
	for _, e := range srv.friction.Entries() {
		if e.Kind == friction.KindFlagged {
			flagged = e
		}
	}
	asked := flagged.Note == "it renamed the wrong one" && flagged.Mode == "task" && flagged.Steps == 2
	if !asked || flagged.Related != 1 || flagged.Session != DefaultSession {
		t.Errorf("flagged = %+v", flagged)
	}
	if !strings.HasPrefix(flagged.Answer, "done rename the file") || strings.Contains(flagged.Ask, "sk-aaaa") ||
		!strings.Contains(flagged.Ask, "[REDACTED]") {
		t.Errorf("ask/answer = %q / %q", flagged.Ask, flagged.Answer)
	}

	if w := post(t, srv, path, `{"turn":99}`, testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown turn: %d, want 404", w.Code)
	}
	if w := post(t, srv, path, `{`, testToken); w.Code != http.StatusBadRequest {
		t.Errorf("bad body: %d, want 400", w.Code)
	}
}

func TestFlaggingAFailedTurnKeepsItsError(t *testing.T) {
	svc := &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "", errors.New("provider down")
	}}
	srv := newTestServer(t, svc)
	runTask(t, srv, "do it")
	post(t, srv, sessionPath(srv, DefaultSession, "/friction"), `{"turn":0}`, testToken)
	entries := srv.friction.Entries()
	last := entries[len(entries)-1]
	if last.Kind != friction.KindFlagged || last.Text != "provider down" || last.Related != 1 {
		t.Errorf("flagged = %+v", last)
	}
}

// proposingService is a service that can turn what a closed session repeated into proposals.
type proposingService struct {
	*fakeService
	got chan string
}

func (p *proposingService) ProposeSkills(summary string) { p.got <- summary }

func TestADeletedSessionHandsItsRepeatedProceduresToTheReview(t *testing.T) {
	svc := &proposingService{fakeService: recordingService(func(p func(string, ...any)) {
		p("running: go test ./a")
		p("running: git add -A")
	}), got: make(chan string, 4)}
	srv := newTestServer(t, svc, func(o *Options) {
		o.SessionDir = t.TempDir()
		o.NewService = func() (Service, error) { return svc, nil }
	})
	id := newSessionFor(t, srv).ID
	for _, task := range []string{"first", "second"} {
		body, _ := json.Marshal(map[string]string{"task": task})
		collect(t, srv, http.MethodPost, sessionPath(srv, id, "/task"), string(body))
	}
	waitForNoRun(t, srv)

	if w := send(t, srv, http.MethodDelete, sessionPath(srv, id, ""), testToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	srv.learnWG.Wait()
	select {
	case got := <-svc.got:
		if !strings.Contains(got, "repeated in 2 turns") || !strings.Contains(got, "go test ./a") {
			t.Errorf("the review was handed %q", got)
		}
	default:
		t.Fatal("the review was never asked about the closed session")
	}
}

func TestResettingTheDefaultSessionLearnsToo(t *testing.T) {
	svc := &proposingService{fakeService: recordingService(func(p func(string, ...any)) {
		p("running: make build")
		p("running: make test")
	}), got: make(chan string, 1)}
	srv := newTestServer(t, svc)
	runTask(t, srv, "one")
	runTask(t, srv, "two")
	if w := send(t, srv, http.MethodDelete, sessionPath(srv, DefaultSession, ""), testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	srv.learnWG.Wait()
	if len(svc.got) != 1 {
		t.Errorf("the review was asked %d times, want 1", len(svc.got))
	}
}

func TestASessionThatRepeatedNothingOrCannotProposeIsLeftAlone(t *testing.T) {
	// A service that cannot propose: nothing is read, nothing is started.
	plain := newTestServer(t, recordingService(nil))
	runTask(t, plain, "one")
	send(t, plain, http.MethodDelete, sessionPath(plain, DefaultSession, ""), testToken, "")
	plain.learnWG.Wait()

	// A service that can, with nothing repeated.
	svc := &proposingService{fakeService: recordingService(func(p func(string, ...any)) { p("running: ls") }),
		got: make(chan string, 1)}
	srv := newTestServer(t, svc)
	runTask(t, srv, "one")
	runTask(t, srv, "two")
	send(t, srv, http.MethodDelete, sessionPath(srv, DefaultSession, ""), testToken, "")
	srv.learnWG.Wait()
	if len(svc.got) != 0 {
		t.Errorf("the review was asked about a session that repeated nothing: %q", <-svc.got)
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/schedule"
)

// A gateway started without a schedule directory answers an EMPTY LIST, not an error:
// scheduling is off in that deployment, and a front end that draws an empty list is
// telling the truth. This mirrors /v1/projects, which answers an empty list too.
func TestAScheduleListWithoutAStoreIsEmpty(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, "/v1/schedules", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Schedules []map[string]any `json:"schedules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if body.Schedules == nil || len(body.Schedules) != 0 {
		t.Fatalf("schedules = %v, want an empty list and never null", body.Schedules)
	}
}

// withSchedules gives a test server a schedule store of its own.
func withSchedules(t *testing.T, srv *Server, minEvery time.Duration) {
	t.Helper()
	st, err := schedule.Open(filepath.Join(t.TempDir(), "schedules"))
	if err != nil {
		t.Fatalf("schedule.Open: %v", err)
	}
	srv.schedules = st
	srv.opts.ScheduleMinEvery = minEvery
}

func TestCreateScheduleStoresTheTask(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)

	w := postJSON(t, srv, "/v1/schedules", testToken, `{"title":"nightly audit","task":"check the logs","kind":"plan","every":"30m"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Schedule struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Every     string `json:"every"`
			SessionID string `json:"session_id"`
			Enabled   bool   `json:"enabled"`
			NextRun   string `json:"next_run"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if body.Schedule.Title != "nightly audit" || body.Schedule.Every != "30m0s" {
		t.Errorf("created = %+v", body.Schedule)
	}
	// A task that names no conversation fires into the default one: that is where the
	// user who created it from the interface is already looking.
	if body.Schedule.SessionID != schedule.DefaultSessionID {
		t.Errorf("SessionID = %q, want %q", body.Schedule.SessionID, schedule.DefaultSessionID)
	}
	if !body.Schedule.Enabled || body.Schedule.NextRun == "" {
		t.Errorf("a created task must be enabled and carry its next run: %+v", body.Schedule)
	}

	// It is on disk, and a second read agrees: the response is not the only evidence.
	rec, err := srv.schedules.Load(body.Schedule.ID)
	if err != nil || rec == nil {
		t.Fatalf("the created task is not on disk: (%v, %v)", rec, err)
	}
	if rec.Kind != schedule.KindPlan {
		t.Errorf("Kind = %q, want plan", rec.Kind)
	}
}

// A cadence below the floor is refused: it would start a task faster than an agent turn
// can finish, which stacks runs instead of scheduling them.
func TestCreateScheduleRefusesACadenceBelowTheFloor(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)

	w := postJSON(t, srv, "/v1/schedules", testToken, `{"title":"t","task":"x","every":"5s"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "1m") {
		t.Errorf("the refusal must name the floor it enforced, got: %s", w.Body.String())
	}
}

// Each refusal names what to add, so a client does not have to read the docs to fix a
// request. Same stance as config's requireField.
func TestCreateScheduleRefusesAnIncompleteRequest(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no title", `{"task":"x","every":"30m"}`, "title"},
		{"no task", `{"title":"t","every":"30m"}`, "task"},
		{"no cadence", `{"title":"t","task":"x"}`, "every"},
		{"unreadable cadence", `{"title":"t","task":"x","every":"tomorrow"}`, "every"},
		{"unknown kind", `{"title":"t","task":"x","every":"30m","kind":"shell"}`, "kind"},
		{"unknown session", `{"title":"t","task":"x","every":"30m","session_id":"nope"}`, "nope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newTestServer(t, &fakeService{})
			withSchedules(t, srv, time.Minute)
			w := postJSON(t, srv, "/v1/schedules", testToken, c.body)
			if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 400 or 404: body = %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), c.want) {
				t.Errorf("the refusal must name %q, got: %s", c.want, w.Body.String())
			}
		})
	}
}

// A gateway with no store cannot accept a task: there is nowhere to keep it, and a 201
// for something that was not stored is a lie the front end would draw as a real task.
func TestCreateScheduleWithoutAStoreIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := postJSON(t, srv, "/v1/schedules", testToken, `{"title":"t","task":"x","every":"30m"}`)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}

// postJSON performs a POST with a JSON body. The package's existing get() helper only
// builds GETs, and a new helper is cheaper than reshaping every existing test.
//
// It is called postJSON and NOT post, and that name is load-bearing: the package
// ALREADY has a post helper, at internal/gateway/handlers_test.go:18 --
//
//	func post(t *testing.T, srv *Server, path, body, token string) *httptest.ResponseRecorder
//
// Naming this one `post` is a hard compile error in this package:
//
//	vet: internal/gateway/schedules_test.go:<n>:6: post redeclared in this block
//
// and the tempting alternative -- reuse the existing helper -- is worse than it looks,
// because its argument order is (path, BODY, token) while every call below is written
// (path, token, body). All three are strings, so the swap COMPILES and silently sends the
// token as the request body, failing later with a baffling 400/401 instead of a type error.
// Keep this helper's own order (path, token, body) and keep its distinct name.
//
// Not setting Content-Type is fine: no handler in internal/gateway reads an inbound
// Content-Type. The only Content-Type writes in the package are on RESPONSES
// (server.go:471 and server.go:887).
func postJSON(t *testing.T, srv *Server, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// send is post with a method, for PATCH and DELETE.
func send(t *testing.T, srv *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.BaseURL()+path, r)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func createSchedule(t *testing.T, srv *Server, body string) string {
	t.Helper()
	w := postJSON(t, srv, "/v1/schedules", testToken, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("creating the fixture: status = %d, body = %s", w.Code, w.Body.String())
	}
	var out struct {
		Schedule struct {
			ID string `json:"id"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	return out.Schedule.ID
}

// Pausing is a PATCH, and a PATCH only touches what it sends: a request that carried
// the whole record would make "pause this" able to silently rewrite the task.
func TestUpdateSchedulePausesWithoutTouchingTheTask(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"original","every":"30m"}`)

	w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, `{"enabled":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body = %s", w.Code, w.Body.String())
	}
	rec, err := srv.schedules.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load: (%v, %v)", rec, err)
	}
	if rec.Enabled {
		t.Error("the task is still enabled")
	}
	if rec.Task != "original" {
		t.Errorf("Task = %q, want it untouched: a PATCH that only pauses must not rewrite the task", rec.Task)
	}
}

func TestUpdateScheduleRefusesAnEmptyTitle(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, `{"title":"  "}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestUpdateAndDeleteAnUnknownScheduleAreNotFound(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)

	if w := send(t, srv, http.MethodPatch, "/v1/schedules/nope", testToken, `{"enabled":false}`); w.Code != http.StatusNotFound {
		t.Errorf("PATCH of an unknown task = %d, want 404", w.Code)
	}
	if w := send(t, srv, http.MethodDelete, "/v1/schedules/nope", testToken, ""); w.Code != http.StatusNotFound {
		t.Errorf("DELETE of an unknown task = %d, want 404", w.Code)
	}
}

func TestDeleteScheduleRemovesTheRecord(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	w := send(t, srv, http.MethodDelete, "/v1/schedules/"+id, testToken, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: body = %s", w.Code, w.Body.String())
	}
	rec, err := srv.schedules.Load(id)
	if err != nil || rec != nil {
		t.Fatalf("after DELETE the record is (%v, %v), want (nil, nil)", rec, err)
	}
}

// Running now STARTS a run in the conversation the task names, and the run is real:
// the fake service records that it was asked, which is the evidence a handler alone
// cannot give.
func TestRunScheduleNowStartsARunInItsSession(t *testing.T) {
	started := make(chan string, 1)
	svc := &fakeService{
		plan: func(_ context.Context, prompt string, _ func(string, ...any)) (string, error) {
			started <- prompt
			return "the audit is done", nil
		},
	}
	srv := newTestServer(t, svc)
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"audit the logs","kind":"plan","every":"30m"}`)

	w := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: a run now is started, not waited for: body = %s", w.Code, w.Body.String())
	}
	select {
	case got := <-started:
		if got != "audit the logs" {
			t.Errorf("the run was asked for %q, want the task text", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the run never reached the service: 'run now' answered 202 without running anything")
	}
}

// A conversation with a run already in flight refuses the second one, and the refusal is
// reported rather than swallowed: a scheduled task that silently did not run is the one
// failure a person cannot diagnose.
func TestRunScheduleNowReportsABusySession(t *testing.T) {
	release := make(chan struct{})
	svc := &fakeService{
		task: func(_ context.Context, _ string, _ func(string, ...any)) (string, error) {
			<-release
			return "done", nil
		},
	}
	srv := newTestServer(t, svc)
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	first := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, "")
	if first.Code != http.StatusAccepted {
		t.Fatalf("the first run = %d, want 202", first.Code)
	}
	// Wait until the slot is actually taken, so the second request really races a run.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !srv.sessions[DefaultSession].isRunning() {
		time.Sleep(time.Millisecond)
	}
	second := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, "")
	if second.Code != http.StatusConflict {
		t.Fatalf("the second run = %d, want 409: body = %s", second.Code, second.Body.String())
	}
	close(release)
}

// A scheduled firing with nobody to ask must not hang: the approver refuses at once and
// the refusal says how to run unattended. Without this the run blocks forever holding the
// conversation's one run slot.
func TestAScheduledRunRefusesAnApprovalInsteadOfWaiting(t *testing.T) {
	asked := make(chan error, 1)
	svc := &fakeService{
		approverWrap: func(fn agent.Approver) {
			if fn == nil {
				return
			}
			ok, err := fn(context.Background(), agent.ApprovalRequest{Command: "rm -rf /", Reason: "consequential", Rule: "test"})
			asked <- err
			if ok {
				t.Error("the unattended approver approved a command")
			}
		},
	}
	srv := newTestServer(t, svc)
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)
	postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, "")

	select {
	case err := <-asked:
		if err == nil {
			t.Fatal("the unattended approver returned no error: the agent would read that as a user's refusal, which says nothing about why")
		}
		if !strings.Contains(err.Error(), "enforce=false") {
			t.Errorf("the refusal must say how to run unattended, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the approver was never installed for the scheduled run")
	}
}

// A detached run takes the conversation's ONE slot, which is the property the scheduler
// depends on: two firings into one conversation must not interleave two tasks into one
// transcript. This is the same rule POST /task enforces, reached from a different door.
func TestADetachedRunTakesTheConversationsRunSlot(t *testing.T) {
	release := make(chan struct{})
	svc := &fakeService{
		task: func(_ context.Context, _ string, _ func(string, ...any)) (string, error) {
			<-release
			return "done", nil
		},
	}
	srv := newTestServer(t, svc)
	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("the default conversation is missing")
	}

	if _, started := srv.startDetachedRun(c, "first", schedule.KindTask, srv.unattendedApprover("s1")); !started {
		t.Fatal("the first detached run did not start")
	}
	if _, started := srv.startDetachedRun(c, "second", schedule.KindTask, srv.unattendedApprover("s1")); started {
		t.Error("a second detached run started while the first held the slot: two tasks would interleave into one transcript")
	}
	close(release)
}

// The run is recorded in the conversation, so a client that attaches later sees it: a
// scheduled firing that left nothing behind is a firing nobody can verify.
func TestADetachedRunEndsInTheConversation(t *testing.T) {
	svc := &fakeService{
		task: func(_ context.Context, _ string, _ func(string, ...any)) (string, error) {
			return "the audit is done", nil
		},
	}
	srv := newTestServer(t, svc)
	c, _ := srv.lookup(DefaultSession)

	rn, started := srv.startDetachedRun(c, "audit", schedule.KindTask, srv.unattendedApprover("s1"))
	if !started {
		t.Fatal("the detached run did not start")
	}
	select {
	case <-rn.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the detached run never finished")
	}
	outcome, result, _, _ := rn.outcomeOf()
	if outcome != "done" || result != "the audit is done" {
		t.Fatalf("outcome = %q, result = %q", outcome, result)
	}
}

// A cancelled detached run is reported as CANCELLED, not as a failure: it is something
// the gateway's shutdown asked for, and calling it an error would make a restart look
// like a broken task.
func TestACancelledDetachedRunIsNotAFailure(t *testing.T) {
	svc := &fakeService{
		task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	srv := newTestServer(t, svc)
	c, _ := srv.lookup(DefaultSession)
	rn, started := srv.startDetachedRun(c, "audit", schedule.KindTask, srv.unattendedApprover("s1"))
	if !started {
		t.Fatal("the detached run did not start")
	}
	c.cancelRun()
	select {
	case <-rn.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the run did not end after being cancelled")
	}
	if outcome, _, _, _ := rn.outcomeOf(); outcome != "cancelled" {
		t.Fatalf("outcome = %q, want cancelled", outcome)
	}
}

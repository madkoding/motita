package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

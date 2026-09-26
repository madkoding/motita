package gateway

// The branches of schedules.go that no test entered: the list body, the second and third
// refusals on create and update, the error paths behind the store, run-now's blocking form, and
// firstLine.
//
// Every fixture here is deliberately hostile: a store whose directory is replaced by a file, a
// random source that cannot read, a session id nobody holds. The happy paths were already covered
// by schedules_test.go; what is left is what happens when the answer is "no".

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/schedule"
)

// TestTheScheduleListCarriesEveryTaskAndItsNextRun: the list is the shared body of list, create,
// update and delete, and the next run is computed on the SERVER so a front end does not implement
// the cadence arithmetic a second time - two answers to the same question is how they start
// disagreeing.
func TestTheScheduleListCarriesEveryTaskAndItsNextRun(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	createSchedule(t, srv, `{"title":"first","task":"a","every":"30m"}`)
	createSchedule(t, srv, `{"title":"second","task":"b","every":"1h"}`)

	w := get(t, srv, "/v1/schedules", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Schedules []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			NextRun string `json:"next_run"`
		} `json:"schedules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if len(body.Schedules) != 2 {
		t.Fatalf("schedules = %d, want 2", len(body.Schedules))
	}
	for _, sc := range body.Schedules {
		if sc.ID == "" {
			t.Error("every entry needs its id: it is how the front end addresses it")
		}
		if sc.NextRun == "" {
			t.Errorf("%q has no next_run: computing it here is the whole point", sc.Title)
		} else if _, err := time.Parse(time.RFC3339, sc.NextRun); err != nil {
			t.Errorf("%q next_run = %q, want RFC3339: %v", sc.Title, sc.NextRun, err)
		}
	}
}

// The floor in force when NOTHING was configured is the default of one minute, and it holds
// against the request body and not only against the configuration file. The configured floor has
// its own test in schedules_test.go; this is the branch that only runs when the option is unset.
func TestAnUnsetFloorStillRefusesATooTightCadence(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, 0) // deliberately unset, so the DEFAULT is what answers

	w := postJSON(t, srv, "/v1/schedules", testToken, `{"title":"t","task":"x","every":"30s"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "1m") {
		t.Errorf("the refusal must name the default floor, got %q", w.Body.String())
	}
}

// The body is decoded before anything is written, so a malformed create leaves no task behind.
func TestCreateScheduleRefusesABodyThatIsNotJSON(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)

	if w := postJSON(t, srv, "/v1/schedules", testToken, `{not json`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: body = %s", w.Code, w.Body.String())
	}
	if w := get(t, srv, "/v1/schedules", testToken); !strings.Contains(w.Body.String(), `"schedules":[]`) {
		t.Errorf("a refused create must leave nothing behind, got %q", w.Body.String())
	}
}

// And the same for PATCH: the record must survive a request that could not be read.
func TestUpdateScheduleRefusesABodyThatIsNotJSON(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	if w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, `{not json`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: body = %s", w.Code, w.Body.String())
	}
	rec, err := srv.schedules.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("the task must survive a malformed PATCH: (%v, %v)", rec, err)
	}
	if rec.Title != "t" {
		t.Errorf("Title = %q, want it untouched", rec.Title)
	}
}

// PATCH semantics are only worth anything if the fields it DOES carry land, so each one is sent
// and then read back from the store rather than from the response.
func TestUpdateScheduleAppliesEachFieldItIsGiven(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","kind":"task","every":"30m"}`)

	w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken,
		`{"title":"renamed","task":"do the thing","kind":"plan","every":"2h"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body = %s", w.Code, w.Body.String())
	}
	rec, err := srv.schedules.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load: (%v, %v)", rec, err)
	}
	if rec.Title != "renamed" {
		t.Errorf("Title = %q, want renamed", rec.Title)
	}
	if rec.Task != "do the thing" {
		t.Errorf("Task = %q, want the new text", rec.Task)
	}
	if rec.Kind != schedule.KindPlan {
		t.Errorf("Kind = %q, want %q", rec.Kind, schedule.KindPlan)
	}
	if time.Duration(rec.Every) != 2*time.Hour {
		t.Errorf("Every = %s, want 2h", time.Duration(rec.Every))
	}
}

// The refusals that are not about the title. Each one has to be refused on PATCH as well as on
// create, because a task that already exists is the one a person is most likely to break.
func TestUpdateScheduleRefusesEachBadField(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"a blank task would run nothing", `{"task":"   "}`},
		{"a kind the agent does not have", `{"kind":"telepathy"}`},
		{"a cadence below the floor", `{"every":"1s"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, &fakeService{})
			withSchedules(t, srv, time.Minute)
			id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)
			if rec, err := srv.schedules.Load(id); err != nil || rec == nil {
				t.Fatalf("the fixture must be readable first: (%v, %v)", rec, err)
			}

			w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: body = %s", w.Code, w.Body.String())
			}

			// The second half of the PATCH contract: it either applied the field or refused the whole
			// request, and it never half-applied one. The body is the only thing that changed it.
			rec, err := srv.schedules.Load(id)
			if err != nil || rec == nil {
				t.Fatalf("the task must survive a refused PATCH: (%v, %v)", rec, err)
			}
			if rec.Title != "t" || rec.Task != "x" {
				t.Errorf("a refused PATCH changed the record: Title = %q, Task = %q", rec.Title, rec.Task)
			}
		})
	}
}

// A task fires INTO a conversation that already exists. Moving it to one that does not would
// schedule a run into nowhere, and the run could never be watched.
func TestUpdateScheduleRefusesASessionThatDoesNotExist(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, `{"session_id":"nope"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: body = %s", w.Code, w.Body.String())
	}
	rec, err := srv.schedules.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load: (%v, %v)", rec, err)
	}
	if rec.SessionID != schedule.DefaultSessionID {
		t.Errorf("SessionID = %q, want the original", rec.SessionID)
	}
}

// An unreadable store is reported rather than answered as if the set were empty - and it is made
// unreadable the way reality does it: the directory is replaced by a FILE, so reads fail with
// ENOTDIR. A merely missing directory would mean "nothing here", which is a legitimate answer and
// therefore not a failure at all.
//
// The record is read BEFORE the store is broken, deliberately: the endpoints resolve the {id} first,
// so a store that was already unreadable would be reported by scheduleOf and the handler's own
// error branch would never run. Breaking the store afterwards is what makes this test about the
// handler rather than about the lookup in front of it.
func TestAnUnreadableStoreIsReported(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	st := withScheduleStore(t, srv)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)
	if rec, err := srv.schedules.Load(id); err != nil || rec == nil {
		t.Fatalf("the fixture must be readable first: (%v, %v)", rec, err)
	}
	blockStore(t, filepath.Dir(st.Path(id)))

	// A LIST that cannot read the store answers an empty list rather than a server error: the UI
	// stays drawable, and the failure is reported by the paths that write. The alternative - a list
	// that 500s - would take the whole panel down because one file became unreadable.
	w := get(t, srv, "/v1/schedules", testToken)
	if w.Code != http.StatusOK {
		t.Errorf("an unreadable store must not break the list: status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"schedules":[]`) {
		t.Errorf("the list must be empty and never null, got %q", w.Body.String())
	}
	if w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, `{"enabled":false}`); w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable store must be reported on PATCH: status = %d, body = %s", w.Code, w.Body.String())
	}
	if w := send(t, srv, http.MethodDelete, "/v1/schedules/"+id, testToken, ""); w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable store must be reported on DELETE: status = %d, body = %s", w.Code, w.Body.String())
	}
}

// A task that cannot be PERSISTED is reported as a server error rather than answered as if the
// change had landed: a client that believed it paused a task would be reading a lie.
//
// The store is made unwritable and not unreadable, so this reaches the Save call itself. Running as
// root would defeat the fixture - root ignores the mode - so it is skipped there rather than
// reported as passing.
func TestAnUnwritableStoreIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so this fixture cannot make the store unwritable")
	}
	srv := newTestServer(t, &fakeService{})
	st := withScheduleStore(t, srv)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)
	dir := filepath.Dir(st.Path(id))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("make the store read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, `{"enabled":false}`)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("an unwritable store must be reported on PATCH: status = %d, body = %s", w.Code, w.Body.String())
	}
	if w := send(t, srv, http.MethodDelete, "/v1/schedules/"+id, testToken, ""); w.Code != http.StatusInternalServerError {
		t.Errorf("an unremovable task must be reported on DELETE: status = %d, body = %s", w.Code, w.Body.String())
	}
}

// A record that is present but unparseable is a failure, not a missing task: the two have different
// fixes, and answering 404 for a broken file would send the operator looking for a task that is
// right there.
func TestABrokenRecordIsReportedRatherThanTreatedAsMissing(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	st := withScheduleStore(t, srv)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)
	if err := os.WriteFile(st.Path(id), []byte("{ this is not a schedule"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, `{"enabled":false}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a corrupt record: body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "parse") {
		t.Errorf("the error must say the record could not be parsed, got %q", w.Body.String())
	}
}

// The list is ordered by creation time, so two reads of an unchanged store answer in the same order.
// A directory listing has no order of its own, which is what this pins down: a front end that draws
// a reordered list on every poll looks like it is losing tasks.
func TestTheListIsOrderedByCreation(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	st := withScheduleStore(t, srv)
	for _, title := range []string{"oldest", "middle", "newest"} {
		createSchedule(t, srv, `{"title":"`+title+`","task":"x","every":"30m"}`)
		time.Sleep(2 * time.Millisecond) // Created is what the order is built from
	}

	// Written in reverse so the store's own listing order cannot be what the test sees.
	entries, err := os.ReadDir(filepath.Dir(st.Path("x")))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		rec, err := st.Load(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil || rec == nil {
			t.Fatalf("Load(%q): (%v, %v)", e.Name(), rec, err)
		}
		ids = append(ids, rec.ID)
	}
	if len(ids) != 3 {
		t.Fatalf("records = %d, want 3", len(ids))
	}

	w := get(t, srv, "/v1/schedules", testToken)
	var body struct {
		Schedules []struct {
			Title string `json:"title"`
		} `json:"schedules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	want := []string{"oldest", "middle", "newest"}
	for i, sc := range body.Schedules {
		if sc.Title != want[i] {
			t.Errorf("position %d is %q, want %q: the list must be ordered by creation", i, sc.Title, want[i])
		}
	}
}

// last_run is absent until the task has actually run, and the front end draws its countdown from
// next_run: a zero time formatted into the field would read as 1970 and a countdown from it is
// nonsense.
func TestLastRunAppearsOnlyAfterTheTaskHasRun(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	before := get(t, srv, "/v1/schedules", testToken)
	var fresh []map[string]any
	if err := json.Unmarshal(mustSchedules(t, before), &fresh); err != nil {
		t.Fatal(err)
	}
	if _, present := fresh[0]["last_run"]; present {
		t.Errorf("a task that has never run must not report last_run: %v", fresh[0])
	}

	rec, err := srv.schedules.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load: (%v, %v)", rec, err)
	}
	rec.LastRun = time.Now()
	if err := srv.schedules.Save(*rec); err != nil {
		t.Fatal(err)
	}

	after := get(t, srv, "/v1/schedules", testToken)
	var ran []map[string]any
	if err := json.Unmarshal(mustSchedules(t, after), &ran); err != nil {
		t.Fatal(err)
	}
	got, ok := ran[0]["last_run"].(string)
	if !ok || got == "" {
		t.Fatalf("a task that has run must report last_run, got %v", ran[0]["last_run"])
	}
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Errorf("last_run = %q, want RFC3339: %v", got, err)
	}
}

// startScheduler is the wiring that turns the watcher into a running loop on a gateway, and its
// firer is the part worth pinning: it starts a real run in the task's conversation and records the
// outcome. A due task is made to fire by moving its Created into the past, so the test does not
// wait on a cadence.
func TestTheSchedulerFiresADueTaskAndRecordsTheOutcome(t *testing.T) {
	svc := &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "the audit finished\nsecond line", nil
	}}
	srv := newTestServer(t, svc, func(o *Options) { o.ScheduleTick = 5 * time.Millisecond })
	st := withScheduleStore(t, srv)
	id := createSchedule(t, srv, `{"title":"nightly","task":"audit","every":"1h"}`)

	rec, err := st.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load: (%v, %v)", rec, err)
	}
	rec.Created = time.Now().Add(-2 * time.Hour) // due now
	if err := st.Save(*rec); err != nil {
		t.Fatal(err)
	}

	srv.startScheduler()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := st.Load(id)
		if err == nil && got != nil && got.RunCount > 0 {
			if !strings.Contains(got.LastOutcome, "the audit finished") {
				t.Errorf("LastOutcome = %q, want the run's first line", got.LastOutcome)
			}
			if strings.Contains(got.LastOutcome, "second line") {
				t.Errorf("LastOutcome = %q, want ONE line: firstLine exists for this", got.LastOutcome)
			}
			if got.LastRun.IsZero() {
				t.Error("a firing must record when it ran: it is what stops it firing again")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the scheduler never fired the due task")
}

// A task whose session was deleted is recorded as failed rather than repaired: creating a
// conversation for it would put a run in a place the user never opened. The firing must not start a
// run, and it must say why.
func TestTheSchedulerRecordsATaskWhoseSessionIsGone(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	st := withScheduleStore(t, srv)
	id := createSchedule(t, srv, `{"title":"orphan","task":"x","every":"1h"}`)

	rec, err := st.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load: (%v, %v)", rec, err)
	}
	rec.SessionID = "a-session-that-was-deleted"
	rec.Created = time.Now().Add(-2 * time.Hour)
	if err := st.Save(*rec); err != nil {
		t.Fatal(err)
	}

	// FireDue is the pass Run performs, and it is synchronous - so this needs no ticker.
	w := schedule.NewWatcher(st, srv.schedulerFirer(), nil)
	if fired := w.FireDue(context.Background()); len(fired) != 1 {
		t.Fatalf("fired = %v, want the due task", fired)
	}
	got, err := st.Load(id)
	if err != nil || got == nil {
		t.Fatalf("Load: (%v, %v)", got, err)
	}
	if !strings.Contains(got.LastOutcome, "no session") {
		t.Errorf("LastOutcome = %q, want it to name the missing session", got.LastOutcome)
	}
	if got.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1: the pass records the attempt either way", got.RunCount)
	}
}

// A firing whose run was cancelled is NOT a failed task: the run ended, deliberately, and saying
// "the run was cancelled" is the difference between an operator looking for a bug and reading the
// record.
func TestTheSchedulerRecordsACancelledRunAsCancelled(t *testing.T) {
	svc := &fakeService{task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	srv := newTestServer(t, svc)
	st := withScheduleStore(t, srv)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"1h"}`)
	makeDue(t, st, id, "")

	c, ok := srv.lookup(schedule.DefaultSessionID)
	if !ok {
		t.Fatal("the default conversation must exist")
	}
	w := schedule.NewWatcher(st, srv.schedulerFirer(), nil)
	fired := make(chan []string, 1)
	go func() { fired <- w.FireDue(context.Background()) }()
	// The firer blocks until the run ends, so the run is cancelled from here once it is in flight.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, inFlight := c.currentRun(); inFlight {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if _, inFlight := c.currentRun(); !inFlight {
		t.Fatal("the firing never started a run")
	}
	c.cancelRun()
	select {
	case ids := <-fired:
		if len(ids) != 1 {
			t.Fatalf("fired = %v, want the one due task", ids)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the firing never returned")
	}

	got, err := st.Load(id)
	if err != nil || got == nil {
		t.Fatalf("Load: (%v, %v)", got, err)
	}
	if !strings.Contains(got.LastOutcome, "cancelled") {
		t.Errorf("LastOutcome = %q, want it to say the run was cancelled", got.LastOutcome)
	}
}

// A firing whose run FAILED keeps the error in the record: a schedule that failed silently is a
// schedule nobody can repair.
func TestTheSchedulerRecordsAFailedRun(t *testing.T) {
	svc := &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "", errors.New("the model was unreachable")
	}}
	srv := newTestServer(t, svc)
	st := withScheduleStore(t, srv)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"1h"}`)
	makeDue(t, st, id, "")

	w := schedule.NewWatcher(st, srv.schedulerFirer(), nil)
	if fired := w.FireDue(context.Background()); len(fired) != 1 {
		t.Fatalf("fired = %v, want the one due task", fired)
	}
	got, err := st.Load(id)
	if err != nil || got == nil {
		t.Fatalf("Load: (%v, %v)", got, err)
	}
	if !strings.Contains(got.LastOutcome, "failed") {
		t.Errorf("LastOutcome = %q, want it to say the run failed", got.LastOutcome)
	}
	if !strings.Contains(got.LastOutcome, "unreachable") {
		t.Errorf("LastOutcome = %q, want the reason: the operator has to be able to fix it", got.LastOutcome)
	}
}

// A firing that finds the conversation already running is recorded as a skip rather than joined:
// a conversation has ONE run slot, and a scheduled task that queued behind a person's turn would
// be a task that runs whenever the person happens to stop typing.
func TestTheSchedulerSkipsATaskWhoseSessionIsBusy(t *testing.T) {
	release := make(chan struct{})
	svc := &fakeService{task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "done", nil
	}}
	srv := newTestServer(t, svc)
	st := withScheduleStore(t, srv)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"1h"}`)
	// Long enough that the run started below is still in flight when the pass fires the task.
	rec, err := st.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load: (%v, %v)", rec, err)
	}
	rec.Created = time.Now().Add(-2 * time.Hour)
	if err := st.Save(*rec); err != nil {
		t.Fatal(err)
	}

	c, ok := srv.lookup(schedule.DefaultSessionID)
	if !ok {
		t.Fatal("the default conversation must exist")
	}
	if _, started := srv.startDetachedRun(c, "someone is typing", schedule.KindTask, srv.unattendedApprover("other")); !started {
		t.Fatal("the occupying run did not start")
	}
	defer close(release)

	w := schedule.NewWatcher(st, srv.schedulerFirer(), nil)
	if fired := w.FireDue(context.Background()); len(fired) != 1 {
		t.Fatalf("fired = %v, want the due task recorded as skipped", fired)
	}
	got, err := st.Load(id)
	if err != nil || got == nil {
		t.Fatalf("Load: (%v, %v)", got, err)
	}
	if !strings.Contains(got.LastOutcome, "already in progress") {
		t.Errorf("LastOutcome = %q, want it to say the session was busy", got.LastOutcome)
	}
}

// The resolution the watcher runs at comes from the option, and an unset or nonsense one is handed
// on as-is: the FLOOR for it lives in schedule.Watcher, which replaces a non-positive tick with its
// own default. Normalising here too would be a second copy of one clamp, and this repo has already
// paid for keeping one rule in two places (see firstLine's parser note, and the 100%-per-package
// gate that two implementations of "has tests" managed to disagree about).
func TestTheConfiguredTickIsHandedToTheWatcher(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withScheduleStore(t, srv)
	srv.startScheduler() // a zero tick must not panic: the watcher owns the fallback

	srv2 := newTestServer(t, &fakeService{}, func(o *Options) { o.ScheduleTick = -time.Second })
	withScheduleStore(t, srv2)
	srv2.startScheduler() // neither must a negative one
}

// makeDue brings a task's origin into the past so it is due on the next pass, without the test
// waiting on a cadence. sessionID, when given, also moves the task to another conversation.
func makeDue(t *testing.T, st *schedule.Store, id, sessionID string) {
	t.Helper()
	rec, err := st.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load: (%v, %v)", rec, err)
	}
	rec.Created = time.Now().Add(-2 * time.Hour)
	if sessionID != "" {
		rec.SessionID = sessionID
	}
	if err := st.Save(*rec); err != nil {
		t.Fatal(err)
	}
}

// Moving a task to a conversation that DOES exist is the case the interface uses, and the id it
// accepts has to be the one it then stores: a PATCH that answered 200 while keeping the old
// conversation would leave the task firing somewhere the user is not looking.
func TestUpdateScheduleMovesATaskToAnotherSession(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.NewService = func() (Service, error) { return &fakeService{}, nil }
	})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)
	second := createSessionIn(t, srv, "") // a real conversation beside the default one

	w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, `{"session_id":"`+second.ID+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body = %s", w.Code, w.Body.String())
	}
	rec, err := srv.schedules.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load: (%v, %v)", rec, err)
	}
	if rec.SessionID != second.ID {
		t.Errorf("SessionID = %q, want %q: the task must fire where it was moved to", rec.SessionID, second.ID)
	}
}

// A record that exists but cannot be READ is reported rather than answered as a missing task: a
// store on a mount that went away fails exactly here, and "I cannot read it" and "it is not there"
// have different fixes. The file is made unreadable rather than removed, so Load fails instead of
// reporting a clean miss.
func TestAnUnreadableRecordIsReportedRatherThanTreatedAsMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file, so this fixture cannot make the record unreadable")
	}
	srv := newTestServer(t, &fakeService{})
	st := withScheduleStore(t, srv)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)
	if err := os.Chmod(st.Path(id), 0o000); err != nil {
		t.Fatalf("make the record unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(st.Path(id), 0o600) })

	w := send(t, srv, http.MethodPatch, "/v1/schedules/"+id, testToken, `{"enabled":false}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: body = %s", w.Code, w.Body.String())
	}
}

// startScheduler on a gateway with no store is a no-op rather than a nil dereference: the feature
// is optional, and a process that did not configure it must still start.
//
// The tick is deliberately tiny here. The guard exists to avoid starting a goroutine that would
// read a nil store, and that goroutine only reads it on its first tick - so at the default
// resolution a mistake in the guard would not surface for half a minute. A short tick makes the
// wrongly-started pass happen inside the test, where it panics instead of hiding.
func TestTheSchedulerIsANoOpWithoutAStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ScheduleTick = 5 * time.Millisecond })
	if srv.schedules != nil {
		t.Fatal("the fixture assumes no store was configured")
	}
	srv.startScheduler()
	time.Sleep(50 * time.Millisecond) // long enough for a watcher that should not exist to read the store

	// And a nil logger is replaced rather than dereferenced: a caller that has not wired logging
	// yet still runs.
	srv2 := newTestServer(t, &fakeService{})
	withScheduleStore(t, srv2)
	if srv2.opts.Log != nil {
		t.Fatal("the fixture assumes logging was not wired")
	}
	if firer := srv2.schedulerFirer(); firer == nil {
		t.Fatal("the scheduler needs a firer to run at all")
	}
}

// mustSchedules pulls the raw schedule array out of a list response.
func mustSchedules(t *testing.T, w *httptest.ResponseRecorder) []byte {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body = %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Schedules json.RawMessage `json:"schedules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	return envelope.Schedules
}

// A task that cannot be persisted is reported rather than answered with 201 and lost on the next
// restart. The store is made unwritable and not unreadable, so this reaches the Save call itself.
// Running as root would defeat the fixture - root ignores the mode - so it is skipped there rather
// than reported as passing.
func TestCreatingAScheduleRefusesAnUnwritableStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so this fixture cannot make the store unwritable")
	}
	srv := newTestServer(t, &fakeService{})
	st := withScheduleStore(t, srv)
	dir := filepath.Dir(st.Path("anything"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("make the store read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	w := postJSON(t, srv, "/v1/schedules", testToken, `{"title":"t","task":"x","every":"30m"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the task cannot be stored: body = %s", w.Code, w.Body.String())
	}
}

// An id that cannot be formed is reported rather than answered with an empty id, which no client
// could address afterwards.
func TestCreatingAScheduleWithUnanswerableRandomness(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	restore := randReader
	randReader = failingReader{}
	t.Cleanup(func() { randReader = restore })

	w := postJSON(t, srv, "/v1/schedules", testToken, `{"title":"t","task":"x","every":"30m"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: body = %s", w.Code, w.Body.String())
	}
}

// Without a store the capability is ABSENT rather than the record, so every endpoint answers 501:
// a client that read 404 would go looking for a task that could never exist.
func TestScheduleEndpointsWithoutAStoreAnswer501(t *testing.T) {
	srv := newTestServer(t, &fakeService{}) // no store at all

	cases := []struct{ name, method, path, body string }{
		{"create", http.MethodPost, "/v1/schedules", `{"title":"t","task":"x","every":"30m"}`},
		{"update", http.MethodPatch, "/v1/schedules/anything", `{"enabled":false}`},
		{"delete", http.MethodDelete, "/v1/schedules/anything", ""},
		{"run now", http.MethodPost, "/v1/schedules/anything/run", ""},
	}
	for _, tc := range cases {
		w := send(t, srv, tc.method, tc.path, testToken, tc.body)
		if w.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501: body = %s", tc.name, w.Code, w.Body.String())
		}
	}
}

// A run is STARTED, not finished, so it is accepted with a handle; and "run it now" is a person
// overriding the clock once, not a decision to move the schedule.
func TestRunScheduleNowAcceptsAndLeavesTheCadenceAlone(t *testing.T) {
	srv := newTestServer(t, &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "the result", nil
	}})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)
	before, err := srv.schedules.Load(id)
	if err != nil || before == nil {
		t.Fatalf("Load: (%v, %v)", before, err)
	}

	w := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: body = %s", w.Code, w.Body.String())
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if body.RunID == "" {
		t.Error("the answer must carry the run id: it is how the client follows the run")
	}

	after, err := srv.schedules.Load(id)
	if err != nil || after == nil {
		t.Fatalf("Load: (%v, %v)", after, err)
	}
	if !after.Next(after.Created).Equal(before.Next(before.Created)) {
		t.Error("running a task now must not move its schedule")
	}
}

// An optional body may carry {"wait": true}; a body that is not JSON is a client error rather than
// something to ignore, and it must not start a run.
func TestRunScheduleNowRefusesAMalformedBody(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	w := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, `{not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: body = %s", w.Code, w.Body.String())
	}
}

// The blocking form exists for a script that wants the result, and it answers with the outcome
// rather than with a handle.
func TestRunScheduleNowWithWaitAnswersWithTheOutcome(t *testing.T) {
	srv := newTestServer(t, &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "the result", nil
	}})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	w := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, `{"wait":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Outcome string `json:"outcome"`
		Result  string `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if body.Outcome != "done" {
		t.Errorf("outcome = %q, want done", body.Outcome)
	}
	if body.Result != "the result" {
		t.Errorf("result = %q, want the run's answer", body.Result)
	}
}

// firstLine keeps one line of a result for the record: a whole transcript in a JSON field is a
// field nobody reads.
func TestFirstLineKeepsOneLineAndTruncates(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a single line passes through", "done", "done"},
		{"the first line is kept", "first\nsecond\nthird", "first"},
		{"surrounding space is trimmed", "  spaced  \nrest", "spaced"},
		{"a long line is truncated", strings.Repeat("x", 250), strings.Repeat("x", 200) + "…"},
		// The cut happens BEFORE the trim, so leading space spends part of the budget: a truncated
		// line keeps 200 characters minus the indent it was given. Recorded rather than wished
		// away, because the alternative (trim first) would move the ellipsis on every line.
		{"the cut comes before the trim", "  " + strings.Repeat("y", 250), strings.Repeat("y", 198) + "…"},
		{"empty stays empty", "", ""},
	}
	for _, tc := range cases {
		if got := firstLine(tc.in); got != tc.want {
			t.Errorf("%s: firstLine(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// withScheduleStore installs a store a test can then break, so the error paths behind it are
// reachable. withSchedules hides the same thing behind t.TempDir for the happy paths.
func withScheduleStore(t *testing.T, srv *Server) *schedule.Store {
	t.Helper()
	st, err := schedule.Open(filepath.Join(t.TempDir(), "schedules"))
	if err != nil {
		t.Fatalf("schedule.Open: %v", err)
	}
	srv.schedules = st
	return st
}

// blockStore puts a FILE where a directory is, which is how a store becomes unreadable in a way
// that is a failure rather than an empty answer.
func blockStore(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove the store directory: %v", err)
	}
	if err := os.WriteFile(dir, []byte("in the way"), 0o600); err != nil {
		t.Fatalf("block the store directory: %v", err)
	}
}

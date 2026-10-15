package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/schedule"
)

// loadSchedule reads one task's record from the store, failing the test when it is not there.
func loadSchedule(t *testing.T, srv *Server, id string) *schedule.Schedule {
	t.Helper()
	rec, err := srv.schedules.Load(id)
	if err != nil || rec == nil {
		t.Fatalf("Load(%s) = (%v, %v)", id, rec, err)
	}
	return rec
}

// A task run by hand has run: once its run ends, the record carries it like a firing of the
// watcher, so the list does not say "never ran" next to a result the person just read.
func TestRunScheduleNowRecordsTheRun(t *testing.T) {
	srv := newTestServer(t, &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "the result\nmore", nil
	}})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	before := time.Now()
	if w := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, ""); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	srv.scheduleWG.Wait()

	rec := loadSchedule(t, srv, id)
	if rec.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1", rec.RunCount)
	}
	if rec.LastRun.Before(before) {
		t.Errorf("LastRun = %v, want the moment it was started (after %v)", rec.LastRun, before)
	}
	if rec.LastOutcome != "the run finished: the result" {
		t.Errorf("LastOutcome = %q", rec.LastOutcome)
	}
}

// The blocking form records the run before it answers, and a run that failed is recorded with
// the reason.
func TestRunScheduleNowWithWaitRecordsAFailure(t *testing.T) {
	srv := newTestServer(t, &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "", errors.New("the model was unreachable")
	}})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)

	if w := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, `{"wait":true}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	rec := loadSchedule(t, srv, id)
	if rec.RunCount != 1 || !strings.Contains(rec.LastOutcome, "the model was unreachable") {
		t.Errorf("record = (%d, %q), want one failed run with its reason", rec.RunCount, rec.LastOutcome)
	}
}

// A task whose session is gone is 404: nothing is busy, the place it fires into does not exist.
// Nothing runs and nothing is recorded.
func TestRunScheduleNowOfADeletedSessionIsNotFound(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"orphan","task":"x","every":"1h"}`)
	rec := loadSchedule(t, srv, id)
	rec.SessionID = "a-session-that-was-deleted"
	if err := srv.schedules.Save(*rec); err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "a-session-that-was-deleted") {
		t.Errorf("the error must name the missing session: %s", w.Body.String())
	}
	srv.scheduleWG.Wait()
	if got := loadSchedule(t, srv, id); got.RunCount != 0 || !got.LastRun.IsZero() {
		t.Errorf("nothing ran, yet the record says %d runs at %v", got.RunCount, got.LastRun)
	}
}

// A busy session is still 409, and the refused run is not recorded.
func TestRunScheduleNowDoesNotRecordARefusedRun(t *testing.T) {
	release := make(chan struct{})
	srv := newTestServer(t, &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		<-release
		return "done", nil
	}})
	withSchedules(t, srv, time.Minute)
	id := createSchedule(t, srv, `{"title":"t","task":"x","every":"30m"}`)
	c := srv.conversationOf(DefaultSession)
	if !c.takeRunSlot() {
		t.Fatal("the slot of an idle session must be free")
	}
	w := postJSON(t, srv, "/v1/schedules/"+id+"/run", testToken, "")
	c.releaseRunSlot()
	close(release)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	srv.scheduleWG.Wait()
	if got := loadSchedule(t, srv, id); got.RunCount != 0 {
		t.Errorf("a refused run was recorded: %d", got.RunCount)
	}
}

// describeRun gives each way a run ends its sentence.
func TestDescribeRun(t *testing.T) {
	cases := []struct {
		outcome, result, errText, want string
		fails                          bool
	}{
		{"done", "ok\nrest", "", "the run finished: ok", false},
		{"cancelled", "", "", "the run was cancelled", false},
		{"error", "", "boom", "the run failed: boom", true},
	}
	for _, tc := range cases {
		rn := &run{outcome: tc.outcome, result: tc.result, errText: tc.errText}
		got, err := describeRun(rn)
		if tc.fails {
			if err == nil || err.Error() != tc.want {
				t.Errorf("%s: err = %v, want %q", tc.outcome, err, tc.want)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: (%q, %v), want %q", tc.outcome, got, err, tc.want)
		}
	}
}

// recordProblem names the two ways a run is not written, and says nothing when it was.
func TestRecordProblem(t *testing.T) {
	if got := recordProblem(true, nil); got != "" {
		t.Errorf("a recorded run reported %q", got)
	}
	if got := recordProblem(false, nil); !strings.Contains(got, "deleted while it ran") {
		t.Errorf("a deleted task: %q", got)
	}
	if got := recordProblem(true, errors.New("disk full")); !strings.Contains(got, "disk full") {
		t.Errorf("a store failure: %q", got)
	}
}

// A task deleted while it ran stays deleted, and the gateway's log says why its run was not
// written; without a log the same end is silent rather than a crash.
func TestRecordManualRunOfADeletedTaskIsLogged(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "gateway.log")
	log, err := logx.New(logx.Options{Path: logPath, Level: logx.Info})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Log = log })
	withSchedules(t, srv, time.Minute)
	finished := &run{outcome: "done", done: make(chan struct{})}
	close(finished.done)

	srv.recordManualRun("s-gone", time.Now(), finished)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "deleted while it ran") {
		t.Errorf("the log does not say why the run was not recorded: %s", data)
	}
	if rec, _ := srv.schedules.Load("s-gone"); rec != nil {
		t.Error("a deleted task was written back")
	}

	srv.opts.Log = nil
	srv.recordManualRun("s-gone", time.Now(), finished)
}

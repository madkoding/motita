package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/logx"
)

// --- the next firing ---------------------------------------------------------

// Next is the answer a front end shows as "next run at 14:00", so it must name the
// firing that is actually coming: the origin is the creation until the task has run and
// the last run afterwards, exactly as Due measures it. Two copies of the cadence rule
// that disagree is the drift this method exists to prevent.
func TestTheNextFiringIsTheCadenceAfterTheLastRun(t *testing.T) {
	created := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	s := Schedule{Every: Duration(time.Hour), Enabled: true, Created: created}

	if got := s.Next(created); !got.Equal(created.Add(time.Hour)) {
		t.Errorf("the first firing is one cadence after creation: got %s", got)
	}

	// Once it has run, the origin is the last run and not the creation.
	lastRun := created.Add(5 * time.Hour)
	s.LastRun = lastRun
	if got := s.Next(lastRun.Add(10 * time.Minute)); !got.Equal(lastRun.Add(time.Hour)) {
		t.Errorf("a task that has run is measured from its last run: got %s", got)
	}

	// Asked about a moment several cadences ahead, it answers the NEXT firing and not
	// the first one after the origin: that is what "next run" means.
	if got := s.Next(lastRun.Add(3*time.Hour + 30*time.Minute)); !got.Equal(lastRun.Add(4 * time.Hour)) {
		t.Errorf("the next firing after a later moment is the following cadence: got %s", got)
	}

	// A cadence of zero or less has no next firing, and the moment it was given is the
	// only honest answer. Inventing a time would draw a next run for a task that never
	// fires, which is the reading Due already refuses.
	zero := Schedule{Every: 0, Enabled: true, Created: created}
	if got := zero.Next(created); !got.Equal(created) {
		t.Errorf("a schedule with no cadence has no next firing: got %s, want %s", got, created)
	}
}

// --- the record reader -------------------------------------------------------

// The cadence travels as a duration STRING, so a number is not a cadence that happens to
// be written differently: it is a file written against a different contract, and
// accepting it would let 1800000000000 and "30m" both mean something.
func TestACadenceWrittenAsANumberIsRefused(t *testing.T) {
	var s Schedule
	err := json.Unmarshal([]byte(`{"id":"s1","every":1800000000000}`), &s)
	if err == nil {
		t.Fatal("a cadence written as nanoseconds was accepted")
	}
	if !contains(err.Error(), "must be written as a string") {
		t.Errorf("the error must say how a cadence is written, got: %v", err)
	}
	if s.Every != 0 {
		t.Errorf("a refused cadence left %s behind, want no cadence at all", time.Duration(s.Every))
	}
}

// --- the store's failure paths -----------------------------------------------

// A directory that cannot be created is a store that cannot be returned. Continuing
// would hand the caller a store whose every save disappears.
func TestOpenRefusesAPathItCannotCreate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("this is a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(file)
	if err == nil {
		t.Fatalf("Open accepted a path under a file: %+v", st)
	}
	if !contains(err.Error(), "could not create the schedule directory") {
		t.Errorf("the error must name what could not be created, got: %v", err)
	}
}

// A record that cannot be written is reported rather than swallowed: a schedule the
// user created and the disk refused is a schedule the user has to be told about.
func TestSaveReportsARecordItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// The temporary path is taken by a directory, so the write cannot land.
	if err := os.MkdirAll(filepath.Join(dir, "blocked.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	err = st.Save(Schedule{ID: "blocked", Every: Duration(time.Hour)})
	if err == nil {
		t.Fatal("Save reported success on a record it could not write")
	}
	if !contains(err.Error(), "could not write the schedule") {
		t.Errorf("the error must name the record it could not write, got: %v", err)
	}
}

// A record that was written but could not be moved into place is reported too. The
// rename is what makes the write atomic, so a rename that failed means the record did
// not land: saying otherwise would leave a schedule the user believes was saved.
func TestSaveReportsARecordItCannotMoveIntoPlace(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// The destination is a directory that is not empty, so the record cannot be renamed
	// onto it. The temporary write itself still succeeds, which is the point: the two
	// failures are different and only this one proves the rename is checked.
	if err := os.MkdirAll(filepath.Join(dir, "taken.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "taken.json", "inside"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = st.Save(Schedule{ID: "taken", Every: Duration(time.Hour)})
	if err == nil {
		t.Fatal("Save reported success on a record it could not move into place")
	}
	if !contains(err.Error(), "could not save the schedule") {
		t.Errorf("the error must name the record it could not save, got: %v", err)
	}
}

// A record whose time cannot be encoded is reported too: a schedule with an
// impossible date is a file the operator has to fix, and a silent success would leave a
// task that never fires with no explanation.
func TestSaveRefusesAScheduleItCannotEncode(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// A year past 9999 is outside what JSON can carry.
	s := Schedule{ID: "impossible", Every: Duration(time.Hour),
		Created: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := st.Save(s); err == nil {
		t.Fatal("Save reported success on a schedule it could not encode")
	} else if !contains(err.Error(), "could not encode the schedule") {
		t.Errorf("the error must name what could not be encoded, got: %v", err)
	}
}

// A named record that exists and cannot be read is an ERROR, and not the (nil, nil) a
// caller reads as "there is no such task": the two are different facts with different
// fixes, and reporting the second when the first is true loses a task the user has.
func TestLoadReportsARecordItCannotRead(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The record's path is a directory: it is there, and it cannot be read as a record.
	if err := os.MkdirAll(filepath.Join(dir, "s1.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load("s1")
	if err == nil {
		t.Fatalf("an unreadable record was reported as %+v", got)
	}
	if !contains(err.Error(), "could not read the schedule") {
		t.Errorf("the error must name what it could not read, got: %v", err)
	}
}

// One record that goes away between the listing and the read must not hide the rest.
// That window is real - a task deleted from another window while the list is being
// drawn - and the answer is the records that are still there.
func TestLoadAllSkipsARecordThatCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustSave(t, st, Schedule{ID: "here", Title: "t", Every: Duration(time.Hour)})
	// A record that points at nothing: listed, and unreadable.
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "vanished.json")); err != nil {
		t.Fatal(err)
	}

	all, err := st.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "here" {
		t.Fatalf("LoadAll returned %+v, want only the record that can be read", all)
	}
}

// --- the watcher's wiring ----------------------------------------------------

// A caller that has not wired logging yet must still see the lines rather than losing
// them: falling back to nil would turn a missing logger into a panic at the first warn.
func TestAWatcherWithNoLoggerUsesTheGlobalOne(t *testing.T) {
	w, _ := newTestWatcher(t, (&recordingFirer{}).fire)
	plain, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bare := NewWatcher(plain, (&recordingFirer{}).fire, nil)
	if bare.log == nil {
		t.Fatal("a watcher built without a logger has none")
	}
	if bare.log != logx.Global() {
		t.Errorf("a watcher built without a logger did not take the global one")
	}
	// The tick and the clock are the defaults a real deployment runs on.
	if w.tick != watcherTick {
		t.Errorf("a new watcher ticks at %s, want %s", w.tick, watcherTick)
	}
	if w.now == nil {
		t.Error("a new watcher has no clock")
	}
}

// A watcher whose tick was never set must still tick: a zero interval would make
// time.NewTicker panic, and a process that cannot start its loop has no scheduling at
// all. The default is the answer, and an already-cancelled context proves the loop
// still returns.
func TestTheWatchLoopFallsBackToTheDefaultTick(t *testing.T) {
	w, _ := newTestWatcher(t, (&recordingFirer{}).fire)
	w.tick = 0

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stopped := make(chan struct{})
	go func() { defer close(stopped); w.Run(ctx) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Run with no tick did not return after its context was cancelled")
	}
}

// A firing whose record cannot be written is reported, and it is still counted as
// fired: the task RAN, and telling the caller otherwise would be the wrong fact. The
// line in the log is what makes the unwritten record discoverable.
func TestAFiringThatCannotBeRecordedIsReported(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	f := &recordingFirer{result: "the run finished"}

	logDir := t.TempDir()
	logger, err := logx.New(logx.Options{Path: filepath.Join(logDir, "watch.log"), Level: logx.Info})
	if err != nil {
		t.Fatalf("logx.New: %v", err)
	}
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w := NewWatcher(st, f.fire, logger)
	w.now = func() time.Time { return now }

	mustSave(t, st, Schedule{ID: "unwritable", Title: "u", Task: "t", Kind: KindTask,
		Every: Duration(time.Hour), Enabled: true, Created: now.Add(-2 * time.Hour)})
	// The record's temporary path is a directory, so the write after the firing fails.
	if err := os.MkdirAll(filepath.Join(st.dir, "unwritable.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}

	fired := w.FireDue(context.Background())
	if len(fired) != 1 || fired[0] != "unwritable" {
		t.Fatalf("FireDue fired %v, want the task that ran", fired)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	line, err := os.ReadFile(filepath.Join(logDir, "watch.log"))
	if err != nil {
		t.Fatalf("the warning was not written: %v", err)
	}
	if !contains(string(line), "could not record the firing") {
		t.Fatalf("the unwritten record is not reported in the log: %s", line)
	}
}

// The firer's error is what the record keeps, and it is the caller's text rather than a
// classification of it: the sentence the user reads is the one that says how to fix it.
func TestAFailedFiringKeepsTheErrorTheCallerGave(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	f := &recordingFirer{result: "ignored", err: errors.New("no such model")}
	w, st := newTestWatcher(t, f.fire)
	w.now = func() time.Time { return now }

	mustSave(t, st, Schedule{ID: "failing", Title: "f", Task: "t", Kind: KindTask,
		Every: Duration(time.Hour), Enabled: true, Created: now.Add(-2 * time.Hour)})

	w.FireDue(context.Background())
	rec, err := st.Load("failing")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.LastOutcome != "no such model" {
		t.Errorf("LastOutcome = %q, want the caller's error text", rec.LastOutcome)
	}
	if rec.RunCount != 1 {
		t.Errorf("RunCount = %d, want the failed firing counted as a run", rec.RunCount)
	}
	if !rec.LastRun.Equal(now) {
		t.Errorf("LastRun = %s, want %s: a failed firing must not fire again at once", rec.LastRun, now)
	}
}

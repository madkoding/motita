package schedule

import (
	"encoding/json"
	"testing"
	"time"
)

// A schedule created now must not fire the moment it is created: its first firing is
// one cadence later. Getting this wrong turns "every 6h" into "immediately, and then
// every 6h", which is the one firing a user did not ask for.
func TestTheFirstFiringIsOneCadenceAfterCreation(t *testing.T) {
	created := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	s := Schedule{Every: Duration(time.Hour), Enabled: true, Created: created}

	if s.Due(created) {
		t.Error("a schedule just created is already due")
	}
	if s.Due(created.Add(59 * time.Minute)) {
		t.Error("a schedule fired before its first cadence had elapsed")
	}
	if !s.Due(created.Add(time.Hour)) {
		t.Error("a schedule did not fire when its cadence had elapsed")
	}
}

// After the first firing the clock is the LAST RUN, not the creation: a gateway that
// restarted twice in a day must not catch up on firings it missed, and a task that
// took longer than its cadence must not fire twice in a row.
func TestTheCadenceIsMeasuredFromTheLastRun(t *testing.T) {
	created := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	lastRun := created.Add(time.Hour)
	s := Schedule{Every: Duration(time.Hour), Enabled: true, Created: created, LastRun: lastRun}

	if s.Due(lastRun.Add(30 * time.Minute)) {
		t.Error("a schedule fired again while its cadence was still running")
	}
	if !s.Due(lastRun.Add(time.Hour)) {
		t.Error("a schedule did not fire one cadence after its last run")
	}
}

// A disabled task is never due, whatever the clock says. This is what the pause
// button means, and a paused task that keeps firing is worse than no button.
func TestADisabledScheduleIsNeverDue(t *testing.T) {
	created := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	s := Schedule{Every: Duration(time.Hour), Enabled: false, Created: created}
	if s.Due(created.Add(1000 * time.Hour)) {
		t.Error("a disabled schedule was due")
	}
}

// A zero cadence would fire on every tick, forever. It is refused at the door (the
// gateway validates against config's min_every), and Due must not treat it as "now"
// in the meantime.
func TestAZeroCadenceIsNeverDue(t *testing.T) {
	created := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	s := Schedule{Every: 0, Enabled: true, Created: created}
	if s.Due(created.Add(time.Hour)) {
		t.Error("a schedule with no cadence was due")
	}
}

// The cadence is stored and served as a DURATION STRING ("30m"), never as the
// integer a time.Duration marshals to by default. Nanoseconds in a JSON file is a
// number nobody can read or edit, and this file is meant to be editable by hand.
func TestTheCadenceIsWrittenAsADurationString(t *testing.T) {
	s := Schedule{ID: "s1", Title: "backup", Task: "do it", Kind: "task",
		SessionID: DefaultSessionID, Every: Duration(90 * time.Minute), Enabled: true}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `"every":"1h30m0s"`; !contains(string(data), want) {
		t.Errorf("the record holds %s, want it to hold %s", string(data), want)
	}

	var back Schedule
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if time.Duration(back.Every) != 90*time.Minute {
		t.Errorf("round trip changed the cadence: %s", time.Duration(back.Every))
	}
}

// A cadence that cannot be read is refused rather than silently zeroed: a zero
// cadence is a task that never fires, which looks exactly like a broken feature.
func TestAnUnreadableCadenceIsRefused(t *testing.T) {
	var s Schedule
	err := json.Unmarshal([]byte(`{"id":"s1","every":"tomorrow"}`), &s)
	if err == nil {
		t.Fatal("an unreadable cadence was accepted")
	}
	if !contains(err.Error(), "tomorrow") {
		t.Errorf("the error must quote what it could not read, got: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

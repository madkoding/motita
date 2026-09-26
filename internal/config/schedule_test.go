package config

import (
	"strings"
	"testing"
	"time"
)

// The schedule block must be complete on its own: a task that fires on its own is
// exactly the kind of setting an operator sets once and then trusts, so a default
// that fires nothing, or a cadence nobody can read, is a bug that hides for weeks.
func TestTheScheduleBlockHasUsableDefaults(t *testing.T) {
	d := Default().Schedule
	if !d.Enabled {
		t.Error("scheduled tasks are shipped disabled: the interface has a window for them, so the default must be on")
	}
	if d.Tick != 30*time.Second {
		t.Errorf("schedule.tick default = %s, want 30s", d.Tick)
	}
	if d.MinEvery != time.Minute {
		t.Errorf("schedule.min_every default = %s, want 1m: a cadence below a minute would fire a task faster than an agent turn can finish", d.MinEvery)
	}
}

// A negative cadence is a number nobody meant, and reading it as the minimum would
// hide the typo that produced it. Same stance the gateway takes for max_sessions.
//
// One case per setting: validateSchedule refuses each in its own branch, so a case
// missing here is a rule of the block that no test would notice the loss of.
func TestANegativeScheduleSettingIsRefused(t *testing.T) {
	cases := []struct {
		name     string
		modify   func(*Config)
		contains string
	}{
		{"a negative tick", func(c *Config) { c.Schedule.Tick = -time.Second }, "schedule.tick"},
		{"a negative minimum cadence", func(c *Config) { c.Schedule.MinEvery = -time.Minute }, "schedule.min_every"},
		{"a negative run cap", func(c *Config) { c.Schedule.MaxRunsKept = -1 }, "schedule.max_runs_kept"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.modify(&c)
			err := c.ValidateWithoutKey()
			if err == nil {
				t.Fatal("a negative schedule setting was accepted")
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("the error must name the offending setting, got: %v", err)
			}
		})
	}
}

// The variables are the documented way to configure a container, and the
// MOTITA_<BLOCK>_<FIELD> rule is asserted for every scalar setting elsewhere. These
// are the three this block adds.
func TestTheScheduleBlockComesFromTheEnvironment(t *testing.T) {
	t.Setenv("MOTITA_SCHEDULE_ENABLED", "false")
	t.Setenv("MOTITA_SCHEDULE_TICK", "5s")
	t.Setenv("MOTITA_SCHEDULE_MIN_EVERY", "2m")
	t.Setenv("MOTITA_SCHEDULE_MAX_RUNS_KEPT", "25")

	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if c.Schedule.Enabled {
		t.Error("MOTITA_SCHEDULE_ENABLED=false did not turn the block off")
	}
	if c.Schedule.Tick != 5*time.Second {
		t.Errorf("MOTITA_SCHEDULE_TICK was ignored: tick = %s", c.Schedule.Tick)
	}
	if c.Schedule.MinEvery != 2*time.Minute {
		t.Errorf("MOTITA_SCHEDULE_MIN_EVERY was ignored: min_every = %s", c.Schedule.MinEvery)
	}
	if c.Schedule.MaxRunsKept != 25 {
		t.Errorf("MOTITA_SCHEDULE_MAX_RUNS_KEPT was ignored: max_runs_kept = %d", c.Schedule.MaxRunsKept)
	}
}

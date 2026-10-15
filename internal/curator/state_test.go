package curator

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/usage"
)

// writeState writes a curator state file with the given last_run_at, the way an earlier
// process would have left it.
func writeState(t *testing.T, path string, at time.Time) {
	t.Helper()
	data, err := json.Marshal(curatorState{LastRunAt: at})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestMaybeRunHonoursTheIntervalAcrossRestarts is the regression: a new process has no
// in-memory last run, and before MaybeRun read last_run_at back from the state file every
// session start ran a pass, so interval_hours had no effect across runs.
func TestMaybeRunHonoursTheIntervalAcrossRestarts(t *testing.T) {
	now := time.Now()
	c, procs, _, advance := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"unused": agentEntry(now.Add(-20 * 24 * time.Hour)),
	})
	writeState(t, c.cfg.StateFile, now.Add(-time.Hour))

	if err := c.MaybeRun(context.Background()); err != nil {
		t.Fatalf("MaybeRun: %v", err)
	}
	if got := procs.Usage.Get("unused").State; got != usage.StateActive {
		t.Fatalf("a pass ran an hour after the previous process ran one: state = %q", got)
	}

	// Once the interval has passed since the PERSISTED run, the pass runs.
	advance(now.Add(168 * time.Hour))
	if err := c.MaybeRun(context.Background()); err != nil {
		t.Fatalf("MaybeRun past the interval: %v", err)
	}
	if got := procs.Usage.Get("unused").State; got != usage.StateStale {
		t.Errorf("the pass did not run once the interval had passed: state = %q", got)
	}
}

// TestAStaleStateFileDoesNotBlockAPass: a state file older than the interval is a pass that
// is due, not one that just happened.
func TestAStaleStateFileDoesNotBlockAPass(t *testing.T) {
	now := time.Now()
	c, procs, _, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"unused": agentEntry(now.Add(-20 * 24 * time.Hour)),
	})
	writeState(t, c.cfg.StateFile, now.Add(-200*time.Hour))

	if err := c.MaybeRun(context.Background()); err != nil {
		t.Fatalf("MaybeRun: %v", err)
	}
	if got := procs.Usage.Get("unused").State; got != usage.StateStale {
		t.Errorf("a due pass was skipped: state = %q", got)
	}
}

// TestAnUnusableStateFileReadsAsNeverRan: no state file, a corrupt one, a timestamp from the
// future and a path that cannot be read all cost at most one early pass, never a curator that
// stops running.
func TestAnUnusableStateFileReadsAsNeverRan(t *testing.T) {
	cases := map[string]func(t *testing.T, c *Curator, now time.Time){
		"not configured": func(_ *testing.T, c *Curator, _ time.Time) { c.cfg.StateFile = "" },
		"missing":        func(_ *testing.T, _ *Curator, _ time.Time) { /* nothing written: the file does not exist */ },
		"corrupt": func(t *testing.T, c *Curator, _ time.Time) {
			if err := os.WriteFile(c.cfg.StateFile, []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"future": func(t *testing.T, c *Curator, now time.Time) {
			writeState(t, c.cfg.StateFile, now.Add(48*time.Hour))
		},
		"unreadable": func(t *testing.T, c *Curator, _ time.Time) {
			if err := os.Mkdir(c.cfg.StateFile, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			now := time.Now()
			c, _, _, advance := testCurator(t, config.Curator{}, nil)
			advance(now)
			setup(t, c, now)
			if got := c.persistedLastRun(); !got.IsZero() {
				t.Errorf("persistedLastRun = %v, want the zero time", got)
			}
		})
	}
}

// TestStatusReportsAPersistedLastRun: `motita curator status` is a fresh process, so the last
// run it reports has to come from the state file.
func TestStatusReportsAPersistedLastRun(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 0, 0, time.Local)
	c, _, _, advance := testCurator(t, config.Curator{}, nil)
	advance(when.Add(time.Hour))
	writeState(t, c.cfg.StateFile, when)
	if got := c.Status(); !contains(got, "last run: 2026-03-04 05:06") {
		t.Errorf("Status did not report the persisted run:\n%s", got)
	}
}

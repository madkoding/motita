package curator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// The deterministic pass is the part of the curator nobody can watch: it moves documents
// and flips lifecycle states on a schedule, and its rules (14 days, 30 days, pinned,
// provenance) are exactly the kind of thing that is "obviously right" until it archives
// something a user needed. So every rule here is asserted on the FILESYSTEM and on the
// ledger, never on the report the code produced about itself.
//
// The clock is the seam that makes it possible: the thresholds are in DAYS, and a test
// that waited for them would not exist.

// writeLedger writes a usage sidecar by hand.
//
// It writes the FILE rather than driving the ledger's API on purpose: usage.Ledger has
// bumps (View/Use/Patch/SetState/SetPinned) but no setter for an Entry, and the pass decides
// on CreatedAt and LastUsedAt, which no bump lets a test place in the past. The file is the
// public shape of that state (see usage.Open), so writing it is using the interface, not
// reaching around one.
func writeLedger(t *testing.T, dir string, entries map[string]usage.Entry) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		t.Fatalf("marshalling the usage ledger: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".usage.json"), body, 0o600); err != nil {
		t.Fatalf("writing the usage ledger: %v", err)
	}
}

// agentEntry is an agent-created, already-used skill whose last activity was at when.
//
// CreatedBy is what the pass is allowed to touch, and the activity time is what it measures
// idle time against, so both are set explicitly: a test that relied on the package's own
// bumps would be testing the bumps.
func agentEntry(when time.Time) usage.Entry {
	return usage.Entry{
		UseCount:      3,
		ViewCount:     3,
		PatchCount:    1,
		CreatedAt:     when.Add(-time.Hour),
		LastUsedAt:    when,
		LastViewedAt:  when,
		LastPatchedAt: when,
		CreatedBy:     usage.ByAgent,
		State:         usage.StateActive,
	}
}

// testCurator builds a curator over a real directory and a clock the test owns, plus a
// reopen that re-reads the sidecar after the scenario has been written.
func testCurator(t *testing.T, cfg config.Curator, entries map[string]usage.Entry) (*Curator, *procedures.Store, string, func(time.Time)) {
	t.Helper()
	dir := t.TempDir()
	cfg.Enabled = true
	cfg.IntervalHours = 168
	cfg.StaleAfterDays = 14
	cfg.ArchiveAfterDays = 30
	cfg.StateFile = filepath.Join(dir, "curator-state.json")

	lib := skills.New(dir)
	for name := range entries {
		if _, err := lib.Save(name, "# "+name+"\n\nbody\n"); err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}
	writeLedger(t, dir, entries)

	led, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatalf("usage.Open: %v", err)
	}
	procs := &procedures.Store{Library: lib, Usage: led}

	c := New(cfg, procs, nil, nil, nil)
	now := time.Now()
	c.now = func() time.Time { return now }
	return c, procs, dir, func(when time.Time) { now = when }
}

// TestAnUnusedAgentSkillGoesStaleAfterTheThreshold: the first transition, and the one that
// happens first in time. It marks, it does not move.
func TestAnUnusedAgentSkillGoesStaleAfterTheThreshold(t *testing.T) {
	now := time.Now()
	c, procs, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"unused": agentEntry(now.Add(-20 * 24 * time.Hour)),
	})

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if report.Stale != 1 || report.Archived != 0 {
		t.Errorf("report = %d stale, %d archived; want 1 and 0", report.Stale, report.Archived)
	}
	if got := procs.Usage.Get("unused").State; got != usage.StateStale {
		t.Errorf("state = %q, want %q", got, usage.StateStale)
	}
	// Marked is not moved: the document is still where it was.
	if _, err := os.Stat(filepath.Join(dir, "unused.md")); err != nil {
		t.Errorf("a stale skill must stay in the library: %v", err)
	}
}

// TestAnUnusedSkillIsArchivedAfterTheArchiveThreshold: the maximum destructive action, and
// the assertion that it is not a delete.
func TestAnUnusedSkillIsArchivedAfterTheArchiveThreshold(t *testing.T) {
	now := time.Now()
	c, procs, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"forgotten": agentEntry(now.Add(-40 * 24 * time.Hour)),
	})

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if report.Archived != 1 {
		t.Errorf("Archived = %d, want 1", report.Archived)
	}
	if _, err := os.Stat(filepath.Join(dir, "forgotten.md")); !os.IsNotExist(err) {
		t.Errorf("the document is still in the library: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".archive", "forgotten.md")); err != nil {
		t.Errorf("the document was not archived, it was LOST: %v", err)
	}
	if got := procs.Usage.Get("forgotten").State; got != usage.StateArchived {
		t.Errorf("state = %q, want %q", got, usage.StateArchived)
	}
	names, err := c.ListArchived()
	if err != nil || len(names) != 1 || names[0] != "forgotten" {
		t.Errorf("ListArchived = %v, %v; want [forgotten]", names, err)
	}
	// And it is gone from the index, which is what the model reads.
	all, err := procs.Library.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.Name == "forgotten" {
			t.Error("an archived skill must not be offered to the model")
		}
	}
}

// TestAForegroundSkillIsNeverTouched: a skill a PERSON wrote is off-limits to autonomous
// curation. This is the rule that protects the user's own work from a background goroutine.
func TestAForegroundSkillIsNeverTouched(t *testing.T) {
	now := time.Now()
	e := agentEntry(now.Add(-90 * 24 * time.Hour))
	e.CreatedBy = usage.ByForeground
	c, procs, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{"mine": e})

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if report.Stale != 0 || report.Archived != 0 {
		t.Errorf("report touched a user-owned skill: %d stale, %d archived", report.Stale, report.Archived)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.md")); err != nil {
		t.Errorf("a user-owned skill was moved: %v", err)
	}
	if got := procs.Usage.Get("mine").State; got != usage.StateActive {
		t.Errorf("a user-owned skill's state changed to %q", got)
	}
}

// TestAPinnedSkillIsExempt: pinning is the user's veto over the whole schedule, and it has to
// work at ANY age — a pin that only held back a skill that was not old enough would be a pin
// that fails exactly when it is needed.
func TestAPinnedSkillIsExempt(t *testing.T) {
	now := time.Now()
	e := agentEntry(now.Add(-90 * 24 * time.Hour))
	e.Pinned = true
	c, procs, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{"pinned": e})

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if report.Stale != 0 || report.Archived != 0 {
		t.Errorf("a pinned skill was transitioned: %d stale, %d archived", report.Stale, report.Archived)
	}
	if _, err := os.Stat(filepath.Join(dir, "pinned.md")); err != nil {
		t.Errorf("a pinned skill was moved: %v", err)
	}
	if got := procs.Usage.Get("pinned").State; got != usage.StateActive {
		t.Errorf("a pinned skill's state changed to %q", got)
	}
}

// TestANeverUsedSkillKeepsItsGracePeriod: a skill that was written and never reached for
// gets stale_after_days of grace before it can be judged at all. Without it, the pass would
// archive everything a session wrote on the day it wrote it.
func TestANeverUsedSkillKeepsItsGracePeriod(t *testing.T) {
	now := time.Now()
	c, procs, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"fresh": {
			CreatedAt: now.Add(-24 * time.Hour),
			CreatedBy: usage.ByAgent,
			State:     usage.StateActive,
		},
	})

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if report.Stale != 0 || report.Archived != 0 {
		t.Errorf("a young never-used skill was judged: %d stale, %d archived", report.Stale, report.Archived)
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh.md")); err != nil {
		t.Errorf("a young skill was moved: %v", err)
	}
	_ = procs
}

// TestARecentPatchCountsAsActivity: "unused" means nobody touched it, and a corrected
// procedure was touched. Measuring idle time from the last USE alone would archive a skill
// on the day somebody fixed it.
func TestARecentPatchCountsAsActivity(t *testing.T) {
	now := time.Now()
	e := agentEntry(now.Add(-40 * 24 * time.Hour))
	e.LastPatchedAt = now.Add(-time.Hour)
	c, _, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{"fixed": e})

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if report.Archived != 0 {
		t.Errorf("a skill patched an hour ago was archived: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dir, "fixed.md")); err != nil {
		t.Errorf("a recently patched skill was moved: %v", err)
	}
}

// TestRunWritesTheStateFile: the state file is how the next process knows when the last pass
// ran, which is what the interval is measured against.
func TestRunWritesTheStateFile(t *testing.T) {
	c, _, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{})
	if _, err := c.RunWith(context.Background(), RunOptions{}); err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "curator-state.json"))
	if err != nil {
		t.Fatalf("the state file was not written: %v", err)
	}
	var state struct {
		LastRunAt   time.Time `json:"last_run_at"`
		RunCount    int       `json:"run_count"`
		LastSummary string    `json:"last_run_summary"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("the state file is not the documented JSON: %v", err)
	}
	if state.LastRunAt.IsZero() {
		t.Error("the state file does not record when the pass ran")
	}
	if state.LastSummary == "" {
		t.Error("the state file does not summarise the pass")
	}
}

// TestStatusReportsTheThresholdsAndTheCounts: `motita curator status` is the only way to see
// what the schedule WILL do, so the numbers it prints are the contract.
func TestStatusReportsTheThresholdsAndTheCounts(t *testing.T) {
	now := time.Now()
	c, _, _, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"active":  agentEntry(now),
		"stale":   agentEntry(now.Add(-20 * 24 * time.Hour)),
		"gone":    agentEntry(now.Add(-40 * 24 * time.Hour)),
		"private": func() usage.Entry { e := agentEntry(now); e.CreatedBy = usage.ByForeground; return e }(),
	})
	if _, err := c.RunWith(context.Background(), RunOptions{}); err != nil {
		t.Fatalf("RunWith: %v", err)
	}

	got := c.Status()
	for _, want := range []string{
		"curator: ENABLED",
		"every 168h",
		"14d unused",
		"30d unused",
		"off (prune-only)",
		// Only agent-created skills are counted as managed, so foreground work cannot make
		// the number look like the curator has more room to act than it does: four entries
		// in the ledger, three of them the curator's business.
		"curator-managed skills: 3 total (agent-created=3)",
		"active     1",
		"stale      1",
		"archived   1",
	} {
		if !contains(got, want) {
			t.Errorf("Status() does not mention %q:\n%s", want, got)
		}
	}
}

// TestMaybeRunRespectsTheInterval: the whole reason the pass is cheap is that it runs at most
// once per interval. Running it on every invocation would make every session pay for it.
//
// The observable is the LEDGER, not the state file's timestamp: mtime granularity is a
// property of the filesystem, and a test that depends on it fails on a machine with a coarse
// clock for reasons that have nothing to do with the rule. The skill is reset to active
// between calls so that a pass which DID run would leave a mark: an already-stale skill is not
// actionable, and a test that could not tell "did not run" from "had nothing to do" would
// prove nothing.
func TestMaybeRunRespectsTheInterval(t *testing.T) {
	now := time.Now()
	c, procs, _, advance := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"unused": agentEntry(now.Add(-20 * 24 * time.Hour)),
	})

	if err := c.MaybeRun(context.Background()); err != nil {
		t.Fatalf("MaybeRun: %v", err)
	}
	if got := procs.Usage.Get("unused").State; got != usage.StateStale {
		t.Fatalf("the first pass did not run: state = %q", got)
	}

	// Inside the interval the pass must not run. The skill is ACTIVE again, so a pass that
	// ran would mark it stale immediately and be caught here.
	procs.Usage.SetState("unused", usage.StateActive)
	if err := c.MaybeRun(context.Background()); err != nil {
		t.Fatalf("the second MaybeRun: %v", err)
	}
	if got := procs.Usage.Get("unused").State; got != usage.StateActive {
		t.Error("a MaybeRun inside the interval ran the pass anyway")
	}

	// Past the interval it runs again. That is the half of the rule that matters: a curator
	// that only ever ran once would stop working silently.
	advance(now.Add(169 * time.Hour))
	if err := c.MaybeRun(context.Background()); err != nil {
		t.Fatalf("MaybeRun past the interval: %v", err)
	}
	if got := procs.Usage.Get("unused").State; got != usage.StateStale {
		t.Errorf("a MaybeRun past the interval did not run the pass: state = %q", got)
	}
}

// TestMaybeRunDoesNothingWhenDisabled: `enabled: false` is a user saying "not on this
// machine", and it has to mean nothing happens at all.
func TestMaybeRunDoesNothingWhenDisabled(t *testing.T) {
	now := time.Now()
	cfg := config.Curator{Enabled: false}
	c, _, dir, _ := testCurator(t, cfg, map[string]usage.Entry{
		"unused": agentEntry(now.Add(-90 * 24 * time.Hour)),
	})
	c.cfg.Enabled = false

	if err := c.MaybeRun(context.Background()); err != nil {
		t.Fatalf("MaybeRun: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "curator-state.json")); !os.IsNotExist(err) {
		t.Error("a disabled curator ran a pass")
	}
	if _, err := os.Stat(filepath.Join(dir, "unused.md")); err != nil {
		t.Errorf("a disabled curator moved a document: %v", err)
	}
}

// TestPinUnpinAndRestoreRoundTrip: the three commands a user has over the schedule, each
// asserted on the state it leaves rather than on the message it prints.
func TestPinUnpinAndRestoreRoundTrip(t *testing.T) {
	now := time.Now()
	c, procs, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"mine": agentEntry(now),
	})

	if err := c.Pin("mine"); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if !procs.Usage.Get("mine").Pinned {
		t.Error("Pin did not record the pin")
	}
	if err := c.Unpin("mine"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if procs.Usage.Get("mine").Pinned {
		t.Error("Unpin did not remove the pin")
	}

	// The pin survives a reload, which is the point of putting it in a file.
	reloaded, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Pin("mine"); err != nil {
		t.Fatal(err)
	}
	reloaded, _ = usage.Open(filepath.Join(dir, ".usage.json"))
	if !reloaded.Get("mine").Pinned {
		t.Error("the pin was not persisted")
	}

	// Archive + Restore, the recoverable pair.
	if err := procs.Library.Archive("mine"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	procs.Usage.SetState("mine", usage.StateArchived)
	if err := c.Restore("mine"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.md")); err != nil {
		t.Errorf("Restore did not bring the document back: %v", err)
	}
	if got := procs.Usage.Get("mine").State; got != usage.StateActive {
		t.Errorf("state after Restore = %q, want %q", got, usage.StateActive)
	}
}

// TestPinningWithoutALedgerIsRefused: a curator with no ledger cannot remember a pin, so it
// has to say so rather than reporting success and losing the user's decision.
func TestPinningWithoutALedgerIsRefused(t *testing.T) {
	c := New(config.Curator{}, &procedures.Store{Library: skills.New(t.TempDir()), Usage: nil}, nil, nil, nil)
	if err := c.Pin("x"); err == nil {
		t.Error("Pin with no ledger must be refused")
	}
	if err := c.Unpin("x"); err == nil {
		t.Error("Unpin with no ledger must be refused")
	}
}

// TestThePassToleratesAMissingLedger: the curator and the background review are disabled
// without telemetry, but nothing else breaks - the library still answers.
func TestThePassToleratesAMissingLedger(t *testing.T) {
	dir := t.TempDir()
	c := New(config.Curator{Enabled: true, StaleAfterDays: 14, ArchiveAfterDays: 30},
		&procedures.Store{Library: skills.New(dir), Usage: nil}, nil, nil, nil)
	c.cfg.StateFile = filepath.Join(dir, "state.json")

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("a pass with no ledger must be a no-op, not a failure: %v", err)
	}
	if report.Stale != 0 || report.Archived != 0 {
		t.Errorf("a pass with no ledger changed something: %+v", report)
	}
}

// TestTheFirstPassIsNeverBlockedByTheInterval: lastRun is the zero time until something runs,
// and the zero time must not be read as "just now".
func TestTheFirstPassIsNeverBlockedByTheInterval(t *testing.T) {
	now := time.Now()
	c, _, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"unused": agentEntry(now.Add(-90 * 24 * time.Hour)),
	})
	if err := c.MaybeRun(context.Background()); err != nil {
		t.Fatalf("MaybeRun: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".archive", "unused.md")); err != nil {
		t.Errorf("the first pass was skipped, as if it had just run: %v", err)
	}
}

// contains is strings.Contains behind a name that reads like the assertion it serves.
func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

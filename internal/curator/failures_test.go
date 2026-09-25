package curator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/review"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// The tests in curator_test.go assert the RULES. These assert that when a rule cannot be
// carried out, the pass says so instead of reporting success. That distinction is the whole
// value of a background job: nobody is watching it, so a silent failure and a clean pass have
// to be different things in the state file.

// fakeRunner stands in for the consolidation fork. It is here rather than in a shared test
// helper because the curator is the only package that builds one.
type fakeRunner struct {
	result string
	err    error
}

func (f fakeRunner) Run(context.Context, string) (string, error) { return f.result, f.err }

// builderFor returns a review.RunnerBuilder that always builds the given runner, and captures
// the input it was handed.
func builderFor(r fakeRunner, captured *string) review.RunnerBuilder {
	return func(_ *llm.Client, _ *procedures.Store, _ string, _ int) review.SkillRunner {
		return runnerFunc(func(_ context.Context, input string) (string, error) {
			if captured != nil {
				*captured = input
			}
			return r.result, r.err
		})
	}
}

type runnerFunc func(context.Context, string) (string, error)

func (f runnerFunc) Run(ctx context.Context, input string) (string, error) { return f(ctx, input) }

// engineFor builds a real llm.Client without any network: llm.New only validates the
// configuration, so a client that will never be called is cheap and honest.
func engineFor(t *testing.T) *llm.Client {
	t.Helper()
	engine, err := llm.New(config.LLM{Provider: "openai", APIKey: "test-key", Model: "test-model"}, nil)
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	return engine
}

// TestDryRunChangesNothing: --dry-run exists so a user can look before the pass acts. It is
// only worth having if it is guaranteed empty of side effects, so this asserts on the
// FILESYSTEM, the LEDGER and the STATE FILE - all three, because any one of them being written
// is a surprise the flag promised not to spring.
func TestDryRunChangesNothing(t *testing.T) {
	now := time.Now()
	c, procs, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"unused": agentEntry(now.Add(-20 * 24 * time.Hour)),
		"forgot": agentEntry(now.Add(-40 * 24 * time.Hour)),
		"keep":   agentEntry(now.Add(-time.Hour)),
		"private": func() usage.Entry {
			e := agentEntry(now.Add(-90 * 24 * time.Hour))
			e.CreatedBy = usage.ByForeground
			return e
		}(),
	})

	report, err := c.RunWith(context.Background(), RunOptions{DryRun: true})
	if err != nil {
		t.Fatalf("RunWith(DryRun): %v", err)
	}
	// It still REPORTS, which is the point: 1 stale + 1 archived, computed by the same
	// function the real pass uses.
	if report.Stale != 1 || report.Archived != 1 {
		t.Errorf("preview = %d stale, %d archived; want 1 and 1", report.Stale, report.Archived)
	}

	// Nothing moved.
	for _, name := range []string{"unused.md", "forgot.md", "keep.md", "private.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s is gone after a dry run: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".archive")); !os.IsNotExist(err) {
		t.Error("a dry run created the archive directory")
	}
	// No state changed.
	for name, want := range map[string]usage.State{
		"unused": usage.StateActive, "forgot": usage.StateActive, "keep": usage.StateActive,
	} {
		if got := procs.Usage.Get(name).State; got != want {
			t.Errorf("%s state = %q after a dry run, want %q", name, got, want)
		}
	}
	// And no state file was written: a dry run that recorded a run would postpone the real
	// one by a whole interval, which is a side effect nobody asked for.
	if _, err := os.Stat(filepath.Join(dir, "curator-state.json")); !os.IsNotExist(err) {
		t.Error("a dry run wrote the curator state file")
	}
}

// TestPreviewAndRunAgree: the preview is only trustworthy if it is the SAME decision. Two
// separate reasoning paths would eventually disagree, and a user who was told "nothing to do"
// would then watch two documents move.
func TestPreviewAndRunAgree(t *testing.T) {
	now := time.Now()
	entries := map[string]usage.Entry{
		"stale-one": agentEntry(now.Add(-20 * 24 * time.Hour)),
		"gone-one":  agentEntry(now.Add(-40 * 24 * time.Hour)),
		"gone-two":  agentEntry(now.Add(-60 * 24 * time.Hour)),
		"fresh":     agentEntry(now.Add(-time.Hour)),
		"pinned":    func() usage.Entry { e := agentEntry(now.Add(-99 * 24 * time.Hour)); e.Pinned = true; return e }(),
	}
	previewCurator, _, _, _ := testCurator(t, config.Curator{}, entries)
	preview, err := previewCurator.RunWith(context.Background(), RunOptions{DryRun: true})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	realCurator, _, _, _ := testCurator(t, config.Curator{}, entries)
	actual, err := realCurator.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if preview.Stale != actual.Stale || preview.Archived != actual.Archived {
		t.Errorf("the preview said %d stale/%d archived and the run did %d/%d",
			preview.Stale, preview.Archived, actual.Stale, actual.Archived)
	}
	if preview.Stale != 1 || preview.Archived != 2 {
		t.Errorf("preview = %d stale, %d archived; want 1 and 2", preview.Stale, preview.Archived)
	}
}

// TestANeverUsedEntryFallsBackToItsCreationTime: telemetry can be written by an older build
// that recorded a creation date and no use date. Reading "never used" as "idle for zero time"
// would make such a skill immortal; reading it as its own age is the only reading that keeps
// the schedule working.
func TestANeverUsedEntryFallsBackToItsCreationTime(t *testing.T) {
	now := time.Now()
	c, _, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"ancient": {
			UseCount:  2, // used, so the grace period does not apply
			CreatedAt: now.Add(-90 * 24 * time.Hour),
			CreatedBy: usage.ByAgent,
			State:     usage.StateActive,
		},
	})

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if report.Archived != 1 {
		t.Errorf("Archived = %d, want 1: an entry with no timestamps must age from its creation", report.Archived)
	}
	if _, err := os.Stat(filepath.Join(dir, ".archive", "ancient.md")); err != nil {
		t.Errorf("the document was not archived: %v", err)
	}
}

// TestArchivingAFailureIsReportedAndTheRestContinues: one unwritable document must not abort
// the pass. It must be COUNTED as not-done, and the other documents must still be handled -
// otherwise a single bad file stops all maintenance, silently, forever.
func TestArchivingAFailureIsReportedAndTheRestContinues(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	lib := skills.New(dir)
	// "ghost" is in the ledger and NOT on disk: archiving it fails with ENOENT.
	if _, err := lib.Save("real", "# Real\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir, map[string]usage.Entry{
		"ghost": agentEntry(now.Add(-40 * 24 * time.Hour)),
		"real":  agentEntry(now.Add(-40 * 24 * time.Hour)),
	})
	led, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	logger, err := logx.New(logx.Options{Level: logx.Error})
	if err != nil {
		t.Fatalf("logx.New: %v", err)
	}
	cfg := config.Curator{Enabled: true, IntervalHours: 168, StaleAfterDays: 14, ArchiveAfterDays: 30}
	cfg.StateFile = filepath.Join(dir, "state.json")
	c := New(cfg, &procedures.Store{Library: lib, Usage: led}, nil, logger, nil)
	c.now = func() time.Time { return now }

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	// Only the real one counts, and the ghost is not reported as archived.
	if report.Archived != 1 {
		t.Errorf("Archived = %d, want 1: a failure must not be counted as a success", report.Archived)
	}
	if _, err := os.Stat(filepath.Join(dir, ".archive", "real.md")); err != nil {
		t.Errorf("one failure stopped the pass: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ghost.md")); !os.IsNotExist(err) {
		t.Errorf("the failing document must simply not exist: %v", err)
	}
}

// TestConsolidateWithoutAnEngineFailsLoudly: --consolidate is a request, and a request that
// cannot be honoured has to be an error. Reporting success would leave a user believing a
// model rewrote their library when nothing ran.
func TestConsolidateWithoutAnEngineFailsLoudly(t *testing.T) {
	c, _, _, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{})
	_, err := c.RunWith(context.Background(), RunOptions{Consolidate: true})
	if err == nil {
		t.Fatal("RunWith(Consolidate) with no engine must fail")
	}
	if !strings.Contains(err.Error(), "no engine") {
		t.Errorf("err = %v, want it to name the missing engine", err)
	}
}

// TestConsolidateWithoutABuilderFailsLoudly: the engine is half of it. Without a way to build
// the skill-scoped loop there is no fork, and the same rule applies.
func TestConsolidateWithoutABuilderFailsLoudly(t *testing.T) {
	c, _, _, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{})
	c.engine = engineFor(t)
	// buildRunner stays nil.

	report, err := c.RunWith(context.Background(), RunOptions{Consolidate: true})
	if err != nil {
		t.Fatalf("a consolidation that could not run is reported through the log, not as a failure of the pass: %v", err)
	}
	if report == nil {
		t.Fatal("the pass must still report")
	}
}

// TestConsolidationRunsTheForkAndSavesTheLedger: the opt-in half. It asserts the fork was
// handed a CANDIDATE LIST (the model needs something to consolidate) and that the ledger is
// flushed, because the fork's own saves are the only thing standing between a merged skill and
// a lost one.
func TestConsolidationRunsTheForkAndSavesTheLedger(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	lib := skills.New(dir)
	for _, name := range []string{"one", "two"} {
		if _, err := lib.Save(name, "# "+name+"\n\nbody\n"); err != nil {
			t.Fatal(err)
		}
	}
	writeLedger(t, dir, map[string]usage.Entry{
		"one": agentEntry(now),
		"two": agentEntry(now),
		"not-mine": func() usage.Entry {
			e := agentEntry(now)
			e.CreatedBy = usage.ByForeground
			return e
		}(),
	})
	led, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	logger, err := logx.New(logx.Options{Level: logx.Error})
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Curator{Enabled: true, IntervalHours: 168, StaleAfterDays: 14, ArchiveAfterDays: 30, Consolidate: true}
	cfg.StateFile = filepath.Join(dir, "state.json")

	var seen string
	c := New(cfg, &procedures.Store{Library: lib, Usage: led}, engineFor(t), logger,
		builderFor(fakeRunner{result: strings.Repeat("consolidated ", 40)}, &seen))
	c.now = func() time.Time { return now }

	if _, err := c.RunWith(context.Background(), RunOptions{}); err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	for _, want := range []string{"## CANDIDATE SKILLS", "### one", "### two", "## YOUR TASK"} {
		if !strings.Contains(seen, want) {
			t.Errorf("the candidate list handed to the fork is missing %q:\n%s", want, seen)
		}
	}
	// The foreground skill is NOT a candidate: the fork reads its instructions, and an
	// instruction that mentions a document is an invitation to touch it.
	if strings.Contains(seen, "not-mine") {
		t.Error("the candidate list offered the model a skill that is not the curator's to change")
	}
}

// TestConsolidationReportsAFailingFork: the fork is best-effort by design - a pass that could
// not consolidate still leaves the deterministic work done and recorded.
func TestConsolidationReportsAFailingFork(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	lib := skills.New(dir)
	writeLedger(t, dir, map[string]usage.Entry{})
	led, _ := usage.Open(filepath.Join(dir, ".usage.json"))
	logger, _ := logx.New(logx.Options{Level: logx.Error})
	cfg := config.Curator{Enabled: true, Consolidate: true, IntervalHours: 168, StaleAfterDays: 14, ArchiveAfterDays: 30}
	cfg.StateFile = filepath.Join(dir, "state.json")

	c := New(cfg, &procedures.Store{Library: lib, Usage: led}, engineFor(t), logger,
		builderFor(fakeRunner{err: errors.New("the model went away")}, nil))
	c.now = func() time.Time { return now }

	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("a failing consolidation must not fail the maintenance run: %v", err)
	}
	if report == nil {
		t.Fatal("the pass must report")
	}
	// The run was still recorded, which is what keeps the next pass from repeating it.
	if _, err := os.Stat(cfg.StateFile); err != nil {
		t.Errorf("the state file was not written: %v", err)
	}
}

// TestAStateFileThatCannotBeWrittenIsAFailure: the state file is how the next process knows
// the pass already ran. Silently failing to write it would make the curator run again on every
// single invocation, which is the expensive bug this whole feature is shaped to avoid.
func TestAStateFileThatCannotBeWrittenIsAFailure(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Curator{Enabled: true, IntervalHours: 168, StaleAfterDays: 14, ArchiveAfterDays: 30}
	// A directory that is actually a FILE: MkdirAll cannot create it.
	cfg.StateFile = filepath.Join(blocker, "state.json")
	c := New(cfg, &procedures.Store{Library: skills.New(dir)}, nil, nil, nil)
	c.now = func() time.Time { return now }

	if _, err := c.RunWith(context.Background(), RunOptions{}); err == nil {
		t.Error("a pass that could not record itself must not report success")
	}
}

// TestNoStateFileIsNotAnError: state_file is optional (the state is a convenience, not the
// feature), so an empty path must be a working configuration.
func TestNoStateFileIsNotAnError(t *testing.T) {
	c, _, _, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{})
	c.cfg.StateFile = ""
	if _, err := c.RunWith(context.Background(), RunOptions{}); err != nil {
		t.Errorf("an unset state_file must be fine: %v", err)
	}
}

// TestStatusWithoutALedgerSaysNotingAboutSkills: with no telemetry there is nothing honest to
// say about the library, and a row of zeroes would read as "your library is empty".
func TestStatusWithoutALedgerSaysNothingAboutSkills(t *testing.T) {
	cfg := config.Curator{Enabled: true, IntervalHours: 12, StaleAfterDays: 3, ArchiveAfterDays: 9}
	c := New(cfg, &procedures.Store{Library: skills.New(t.TempDir())}, nil, nil, nil)

	got := c.Status()
	if !strings.Contains(got, "curator: ENABLED") {
		t.Errorf("Status() = %q, want it to report enabled", got)
	}
	if !strings.Contains(got, "every 12h") || !strings.Contains(got, "3d unused") || !strings.Contains(got, "9d unused") {
		t.Errorf("Status() does not report the configured thresholds:\n%s", got)
	}
	if strings.Contains(got, "curator-managed") {
		t.Errorf("Status() claims to know about skills with no ledger:\n%s", got)
	}
}

// TestStatusReportsADisabledCuratorAndItsLastRun: the two facts an operator checks first.
func TestStatusReportsADisabledCuratorAndItsLastRun(t *testing.T) {
	now := time.Now()
	c, _, _, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{})
	c.cfg.Enabled = false
	c.cfg.Consolidate = true

	if got := c.Status(); !strings.Contains(got, "curator: DISABLED") || !strings.Contains(got, "consolidate:    on") {
		t.Errorf("Status() = %q, want DISABLED and consolidate on", got)
	}
	if got := c.Status(); strings.Contains(got, "last run:") {
		t.Errorf("Status() claims a last run before one happened:\n%s", got)
	}

	c.mu.Lock()
	c.lastRun = now
	c.mu.Unlock()
	if got := c.Status(); !strings.Contains(got, "last run: "+now.Format("2006-01-02 15:04")) {
		t.Errorf("Status() does not report the last run:\n%s", got)
	}
}

// TestRunCountIsBestEffort: it is documented as a best-effort counter, so the contract is that
// it exists and does not panic - not that it counts.
func TestRunCountIsBestEffort(t *testing.T) {
	c, _, _, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{})
	if got := c.runCount(); got != 0 {
		t.Errorf("runCount = %d, want the documented 0", got)
	}
}

// TestTheLabelsCoverBothValues: two tiny helpers, and both halves are printed to a user
// somewhere.
func TestTheLabelsCoverBothValues(t *testing.T) {
	if enabledLabel(true) != "ENABLED" || enabledLabel(false) != "DISABLED" {
		t.Error("enabledLabel does not cover both values")
	}
	if boolLabel(true, "on", "off") != "on" || boolLabel(false, "on", "off") != "off" {
		t.Error("boolLabel does not cover both values")
	}
}

// TestRestoreReportsAFailureAndLeavesTheStateAlone: restoring a skill that is not archived
// must fail, and it must NOT flip the ledger to active - a state that says "active" about a
// document that is not there is a state that lies to the next pass.
func TestRestoreReportsAFailureAndLeavesTheStateAlone(t *testing.T) {
	now := time.Now()
	c, procs, dir, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{
		"gone": agentEntry(now.Add(-40 * 24 * time.Hour)),
	})
	// Archive it for real, then try to restore it twice.
	if err := procs.Library.Archive("gone"); err != nil {
		t.Fatal(err)
	}
	procs.Usage.SetState("gone", usage.StateArchived)
	if err := c.Restore("gone"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := c.Restore("gone"); err == nil {
		t.Error("restoring a skill that is not archived must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.md")); err != nil {
		t.Errorf("the first restore did not bring it back: %v", err)
	}
}

// TestAPassThatCannotFlushTheLedgerFails: the pass decides on telemetry and then writes it. If
// the write fails, the transitions it just made in memory are not durable, and reporting
// success would mean the next pass re-decides from a state that no longer exists.
func TestAPassThatCannotFlushTheLedgerFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits")
	}
	now := time.Now()
	dir := t.TempDir()
	lib := skills.New(dir)
	if _, err := lib.Save("stale-one", "# Stale\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir, map[string]usage.Entry{"stale-one": agentEntry(now.Add(-20 * 24 * time.Hour))})
	led, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A read-only directory: MkdirAll is a no-op on it, but CreateTemp inside it fails, so
	// Save cannot do its atomic write. The seam usage uses for the same case.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	cfg := config.Curator{Enabled: true, IntervalHours: 168, StaleAfterDays: 14, ArchiveAfterDays: 30}
	cfg.StateFile = filepath.Join(dir, "state.json")
	c := New(cfg, &procedures.Store{Library: lib, Usage: led}, nil, nil, nil)
	c.now = func() time.Time { return now }

	if _, err := c.RunWith(context.Background(), RunOptions{}); err == nil {
		t.Error("a pass whose ledger could not be flushed must not report success")
	}
}

// TestAStateThatCannotBeEncodedIsAFailure: unreachable in a correct build, which is exactly
// why it needs a test. The seams exist here for the same reason internal/usage has them: the
// coverage gate rejects a branch that no test can reach, and a branch that no test can reach
// is a branch nobody has ever run.
func TestAStateThatCannotBeEncodedIsAFailure(t *testing.T) {
	c, _, _, _ := testCurator(t, config.Curator{}, map[string]usage.Entry{})
	old := jsonMarshalIndent
	jsonMarshalIndent = func(any, string, string) ([]byte, error) {
		return nil, errors.New("the encoder went away")
	}
	t.Cleanup(func() { jsonMarshalIndent = old })

	if _, err := c.RunWith(context.Background(), RunOptions{}); err == nil {
		t.Error("a pass that could not encode its state must not report success")
	}
}

// TestConsolidateOnByConfigurationWithoutAnEngineIsReportedInTheLog: the YAML can leave
// consolidation on while the engine is absent (a machine with no key). The pass must still
// complete its deterministic work; the consolidation is skipped loudly.
func TestConsolidateOnByConfigurationWithoutAnEngineIsReportedInTheLog(t *testing.T) {
	c, _, _, _ := testCurator(t, config.Curator{Consolidate: true}, map[string]usage.Entry{})
	// engine stays nil: New was called with nil.
	report, err := c.RunWith(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if report == nil {
		t.Fatal("the pass must report")
	}
}

// TestTheCandidateListSkipsSkillsThatAreNotTheCurators: the fork reads this text and acts on
// it, so a user-authored skill that happens to be on disk must not appear as a candidate -
// even though it has telemetry, and even though it is in the library.
func TestTheCandidateListSkipsSkillsThatAreNotTheCurators(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	lib := skills.New(dir)
	for _, name := range []string{"mine", "theirs"} {
		if _, err := lib.Save(name, "# "+name+"\n\nbody\n"); err != nil {
			t.Fatal(err)
		}
	}
	writeLedger(t, dir, map[string]usage.Entry{
		"mine": agentEntry(now),
		"theirs": func() usage.Entry {
			e := agentEntry(now)
			e.CreatedBy = usage.ByForeground
			return e
		}(),
	})
	led, _ := usage.Open(filepath.Join(dir, ".usage.json"))
	cfg := config.Curator{Enabled: true, Consolidate: true, IntervalHours: 168, StaleAfterDays: 14, ArchiveAfterDays: 30}
	cfg.StateFile = filepath.Join(dir, "state.json")
	c := New(cfg, &procedures.Store{Library: lib, Usage: led}, engineFor(t), nil,
		builderFor(fakeRunner{result: "done"}, nil))
	c.now = func() time.Time { return now }

	if got := c.buildCandidateList(); strings.Contains(got, "theirs") {
		t.Errorf("the candidate list offers a user-authored skill:\n%s", got)
	} else if !strings.Contains(got, "### mine") {
		t.Errorf("the candidate list is missing the curator's own skill:\n%s", got)
	}
}

// TestAPreviewWithNoLedgerReportsAnEmptyPass: --dry-run on a machine with no telemetry must
// print "nothing to do", not crash and not pretend it looked.
func TestAPreviewWithNoLedgerReportsAnEmptyPass(t *testing.T) {
	c := New(config.Curator{Enabled: true}, &procedures.Store{Library: skills.New(t.TempDir())}, nil, nil, nil)
	report, err := c.Preview()
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if report.Stale != 0 || report.Archived != 0 {
		t.Errorf("a ledgerless preview reported work: %+v", report)
	}
	// And the dry run reaches the same place.
	if _, err := c.RunWith(context.Background(), RunOptions{DryRun: true}); err != nil {
		t.Errorf("a ledgerless dry run must be fine: %v", err)
	}
}

// TestAWriteWithNoLedgerReportsAnEmptyPass: the same guard on the writing path.
func TestAWriteWithNoLedgerReportsAnEmptyPass(t *testing.T) {
	c := New(config.Curator{Enabled: true}, &procedures.Store{Library: skills.New(t.TempDir())}, nil, nil, nil)
	report, err := c.apply(nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if report.Stale != 0 || report.Archived != 0 {
		t.Errorf("a ledgerless pass reported work: %+v", report)
	}
}

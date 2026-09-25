// Package curator implements background maintenance for agent-created skills.
//
// Two phases, same as Hermes:
//  1. Deterministic (no LLM): skills unused for stale_after_days become stale;
//     skills unused for archive_after_days are moved to .archive/ (recoverable).
//  2. LLM consolidation (opt-in): a forked agent reviews all agent-created
//     skills, identifies prefix clusters, and merges overlapping skills into
//     class-level umbrellas.
//
// Only skills marked created_by="agent" (created by the background review fork)
// are eligible for curation. User-authored and foreground-created skills are
// off-limits. Pinned skills are exempt from all auto-transitions. The curator
// never deletes — the maximum destructive action is archival.
package curator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/review"
	"github.com/madkoding/motita/internal/usage"
)

// Curator runs the periodic maintenance pass.
type Curator struct {
	cfg         config.Curator
	procs       *procedures.Store
	engine      *llm.Client // for the LLM consolidation pass (may be nil)
	log         *logx.Logger
	now         func() time.Time
	mu          sync.Mutex
	lastRun     time.Time
	buildRunner review.RunnerBuilder
}

// New creates a curator. The engine is used only for the opt-in LLM
// consolidation pass; the deterministic pass needs no LLM. buildRunner is the
// factory for the consolidation fork's SkillRunner (same as the review fork's).
func New(cfg config.Curator, procs *procedures.Store, engine *llm.Client, log *logx.Logger, buildRunner review.RunnerBuilder) *Curator {
	return &Curator{
		cfg:         cfg,
		procs:       procs,
		engine:      engine,
		log:         log,
		now:         time.Now,
		buildRunner: buildRunner,
	}
}

// MaybeRun triggers a curation pass if enough time has passed since the last
// run. Called on session start (like Hermes CLI). The deterministic pass is
// cheap (no LLM); the LLM consolidation only runs if cfg.Consolidate is true.
func (c *Curator) MaybeRun(ctx context.Context) error {
	c.mu.Lock()
	if !c.cfg.Enabled {
		c.mu.Unlock()
		return nil
	}
	if c.now().Sub(c.lastRun) < time.Duration(c.cfg.IntervalHours)*time.Hour {
		c.mu.Unlock()
		return nil
	}
	c.lastRun = c.now()
	c.mu.Unlock()

	return c.Run(ctx)
}

// RunOptions are the knobs of one maintenance run.
type RunOptions struct {
	// Consolidate forces the LLM pass even when the configuration leaves it off.
	Consolidate bool
	// DryRun reports what a pass would do and changes NOTHING at all.
	DryRun bool
}

// RunWith runs one pass with options.
//
// Run is RunWith with the zero value, so a caller that already used Run keeps exactly the
// behaviour it had: the switches are additive.
func (c *Curator) RunWith(ctx context.Context, opts RunOptions) (*PassReport, error) {
	if opts.DryRun {
		// A preview does not consolidate either: the consolidation pass writes to the
		// library through a model, and "change nothing" has to mean nothing.
		return c.Preview()
	}

	report, err := c.deterministicPass()
	if err != nil {
		return report, err
	}
	if c.log != nil {
		c.log.Info("curator deterministic pass", "stale", report.Stale, "archived", report.Archived)
	}

	switch {
	case opts.Consolidate && c.engine == nil:
		// Asked for and impossible: reported rather than skipped, because a silent skip
		// reads as "the consolidation ran and found nothing to merge".
		return report, fmt.Errorf("consolidation was asked for and no engine is configured for it")
	case c.cfg.Consolidate || opts.Consolidate:
		if err := c.consolidationPass(ctx); err != nil {
			if c.log != nil {
				c.log.Warn("curator consolidation failed", "error", err)
			}
		}
	}

	return report, c.saveState(report)
}

// Run executes one curation pass: deterministic transitions, then optional LLM
// consolidation. It is RunWith with the zero value.
func (c *Curator) Run(ctx context.Context) error {
	_, err := c.RunWith(ctx, RunOptions{})
	return err
}

// PassReport summarises one deterministic pass.
type PassReport struct {
	Stale    int
	Archived int
	RunAt    time.Time
}

// action is one transition the deterministic pass decided on.
//
// The decision is separate from the ACT, and that separation is the whole reason a dry run
// is trustworthy: a preview that reasoned on its own would eventually disagree with what the
// real pass does, which is the one thing a preview exists to prevent.
type action struct {
	name    string
	archive bool // false means "mark stale"
}

// decide returns the transitions the pass would take, in a stable order so a report and a
// preview are comparable.
//
// It reads and does not write. Every rule of the deterministic pass lives here:
//
//   - only created_by="agent" skills, because a person's own work is not the background's
//     to tidy;
//   - pinned skills are exempt, at ANY age;
//   - a skill that was never used gets stale_after_days of grace before it is judged at all;
//   - "idle" is measured from the last USE or the last PATCH, whichever is later, because a
//     procedure somebody just corrected was touched.
func (c *Curator) decide() []action {
	if c.procs.Usage == nil {
		return nil
	}
	entries := c.procs.Usage.All()
	staleAfter := time.Duration(c.cfg.StaleAfterDays) * 24 * time.Hour
	archiveAfter := time.Duration(c.cfg.ArchiveAfterDays) * 24 * time.Hour

	var out []action
	for name, entry := range entries {
		if entry.CreatedBy != usage.ByAgent {
			continue
		}
		if entry.Pinned {
			continue
		}

		// Never archive a never-used skill younger than stale_after_days.
		age := c.now().Sub(entry.CreatedAt)
		if entry.UseCount == 0 && age < staleAfter {
			continue
		}

		lastActivity := entry.LastUsedAt
		if entry.LastPatchedAt.After(lastActivity) {
			lastActivity = entry.LastPatchedAt
		}
		if lastActivity.IsZero() {
			lastActivity = entry.CreatedAt
		}
		idle := c.now().Sub(lastActivity)

		switch {
		case idle >= archiveAfter && entry.State != usage.StateArchived:
			out = append(out, action{name: name, archive: true})
		case idle >= staleAfter && entry.State == usage.StateActive:
			out = append(out, action{name: name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// apply performs the actions and counts them.
//
// It is the only writer, so it is also where "no telemetry" is handled: a curator with no
// ledger has nothing to decide on and nothing to persist, and every caller gets the same
// answer instead of each of them guarding.
func (c *Curator) apply(actions []action) (*PassReport, error) {
	if c.procs.Usage == nil {
		return &PassReport{RunAt: c.now()}, nil
	}
	report := &PassReport{RunAt: c.now()}
	for _, a := range actions {
		if a.archive {
			if err := c.archiveSkill(a.name); err != nil {
				if c.log != nil {
					c.log.Warn("failed to archive skill", "skill", a.name, "error", err)
				}
				continue
			}
			report.Archived++
			continue
		}
		c.procs.Usage.SetState(a.name, usage.StateStale)
		report.Stale++
	}
	if err := c.procs.Usage.Save(); err != nil {
		return report, err
	}
	return report, nil
}

// Preview reports what a pass would do, and touches nothing.
//
// It is the only honest answer to "what is this about to do to my library", and it is a
// DRY RUN: the decision comes from decide, the same function the real pass uses.
func (c *Curator) Preview() (*PassReport, error) {
	report := &PassReport{RunAt: c.now()}
	for _, a := range c.decide() {
		if a.archive {
			report.Archived++
			continue
		}
		report.Stale++
	}
	return report, nil
}

func (c *Curator) deterministicPass() (*PassReport, error) {
	return c.apply(c.decide())
}

func (c *Curator) archiveSkill(name string) error {
	if err := c.procs.Library.Archive(name); err != nil {
		return err
	}
	c.procs.Usage.SetState(name, usage.StateArchived)
	if c.log != nil {
		c.log.Info("skill archived", "skill", name)
	}
	return nil
}

// Restore moves an archived skill back to the active directory.
func (c *Curator) Restore(name string) error {
	if err := c.procs.Library.Restore(name); err != nil {
		return err
	}
	c.procs.Usage.SetState(name, usage.StateActive)
	return c.procs.Usage.Save()
}

// Pin marks a skill as exempt from all auto-transitions.
func (c *Curator) Pin(name string) error {
	if c.procs.Usage == nil {
		return fmt.Errorf("no usage ledger configured")
	}
	c.procs.Usage.SetPinned(name, true)
	return c.procs.Usage.Save()
}

// Unpin removes the pin.
func (c *Curator) Unpin(name string) error {
	if c.procs.Usage == nil {
		return fmt.Errorf("no usage ledger configured")
	}
	c.procs.Usage.SetPinned(name, false)
	return c.procs.Usage.Save()
}

// ListArchived returns the names of skills in .archive/.
func (c *Curator) ListArchived() ([]string, error) { return c.procs.Library.Archived() }

// Status returns a human-readable summary for the CLI.
func (c *Curator) Status() string {
	var b strings.Builder
	fmt.Fprintf(&b, "curator: %s\n", enabledLabel(c.cfg.Enabled))
	fmt.Fprintf(&b, "  interval:       every %dh\n", c.cfg.IntervalHours)
	fmt.Fprintf(&b, "  stale after:    %dd unused\n", c.cfg.StaleAfterDays)
	fmt.Fprintf(&b, "  archive after:  %dd unused\n", c.cfg.ArchiveAfterDays)
	fmt.Fprintf(&b, "  consolidate:    %s\n", boolLabel(c.cfg.Consolidate, "on", "off (prune-only)"))
	if c.procs.Usage != nil {
		entries := c.procs.Usage.All()
		managed, active, stale, archived := 0, 0, 0, 0
		for _, e := range entries {
			if e.CreatedBy == usage.ByAgent {
				managed++
				switch e.State {
				case usage.StateActive:
					active++
				case usage.StateStale:
					stale++
				case usage.StateArchived:
					archived++
				}
			}
		}
		fmt.Fprintf(&b, "\ncurator-managed skills: %d total (agent-created=%d)\n", managed, managed)
		fmt.Fprintf(&b, "  active     %d\n", active)
		fmt.Fprintf(&b, "  stale      %d\n", stale)
		fmt.Fprintf(&b, "  archived   %d\n", archived)
	}
	c.mu.Lock()
	if !c.lastRun.IsZero() {
		fmt.Fprintf(&b, "\nlast run: %s\n", c.lastRun.Format("2006-01-02 15:04"))
	}
	c.mu.Unlock()
	return b.String()
}

// jsonMarshalIndent is the one call to json.MarshalIndent in this package, held in a variable
// so that a test can make it fail. The state it encodes is always JSON-safe (time.Time, int,
// string), so the error is unreachable in a correct build — the same shape internal/usage
// documents for the same reason.
var jsonMarshalIndent = func(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

func (c *Curator) saveState(report *PassReport) error {
	state := curatorState{
		LastRunAt:   report.RunAt,
		RunCount:    c.runCount(),
		LastSummary: fmt.Sprintf("auto: %d stale, %d archived; llm: %s", report.Stale, report.Archived, boolLabel(c.cfg.Consolidate, "ran", "skipped")),
	}
	if c.cfg.StateFile == "" {
		return nil
	}
	data, err := jsonMarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(c.cfg.StateFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.cfg.StateFile, data, 0o600)
}

func (c *Curator) runCount() int {
	// Not tracked precisely; this is a best-effort counter.
	return 0
}

type curatorState struct {
	LastRunAt   time.Time `json:"last_run_at"`
	RunCount    int       `json:"run_count"`
	LastSummary string    `json:"last_run_summary"`
}

func (c *Curator) consolidationPass(ctx context.Context) error {
	if c.engine == nil {
		return fmt.Errorf("no engine configured for LLM consolidation")
	}
	if c.buildRunner == nil {
		return fmt.Errorf("no runner builder configured for LLM consolidation")
	}
	runner := c.buildRunner(c.engine, c.procs, curatorSystemPrompt, 16)

	candidates := c.buildCandidateList()
	result, err := runner.Run(ctx, candidates)
	if err != nil {
		return err
	}
	if c.log != nil {
		preview := result
		if len(preview) > 300 {
			preview = preview[:300] + "…"
		}
		c.log.Info("curator consolidation complete", "result", preview)
	}
	return c.procs.Usage.Save()
}

func (c *Curator) buildCandidateList() string {
	entries := c.procs.Usage.All()
	var b strings.Builder
	b.WriteString("## CANDIDATE SKILLS\n\n")
	all, _ := c.procs.Library.List()
	for _, s := range all {
		e := entries[s.Name]
		if e.CreatedBy != usage.ByAgent {
			continue // only curator-managed
		}
		fmt.Fprintf(&b, "### %s\n", s.Name)
		fmt.Fprintf(&b, "Title: %s\n", s.Title)
		fmt.Fprintf(&b, "Summary: %s\n", s.Summary)
		fmt.Fprintf(&b, "UseCount: %d, PatchCount: %d, State: %s\n",
			e.UseCount, e.PatchCount, e.State)
		fmt.Fprintf(&b, "LastUsed: %s, Created: %s\n\n",
			e.LastUsedAt.Format("2006-01-02"), e.CreatedAt.Format("2006-01-02"))
	}
	b.WriteString(curatorInstruction)
	return b.String()
}

const curatorSystemPrompt = `You are the skill CURATOR of an autonomous agent. This is an
UMBRELLA-BUILDING consolidation pass, not a passive audit. You have four tools:
list_skills, search_skills, read_skill, and save_skill. You have NO filesystem
access and NO command execution.

## Goal

The skill collection should be a LIBRARY OF CLASS-LEVEL INSTRUCTIONS. A
collection of hundreds of narrow skills where each captures one session's
specific bug is a FAILURE. One broad umbrella skill with labeled subsections
beats five narrow siblings for discoverability.

## Hard rules

1. DO NOT touch skills you did not create (created_by != "agent").
2. DO NOT delete skills. save_skill replaces a file, but do not remove files
   you cannot absorb into an umbrella first.
3. DO NOT touch pinned skills.
4. DO NOT use usage counters as a reason to skip consolidation. Judge overlap
   on CONTENT, not on use_count.

## How to work

1. Scan the candidate list. Identify PREFIX CLUSTERS (skills sharing a domain
   keyword).
2. For each cluster with 2+ members, ask: "Would a maintainer write this as N
   separate skills, or as one skill with N labeled subsections?" When the
   answer is the latter, MERGE.
3. Three ways to consolidate:
   a. MERGE INTO EXISTING UMBRELLA — patch it to add sections for each sibling.
   b. CREATE A NEW UMBRELLA — no member is broad enough.
   c. DEMOTE — fold narrow-but-valuable depth into the umbrella's body.
4. When merging: DISTILL, do not copy. Absorbed content becomes rules. The same
   lesson stated twice becomes one rule. Drop incident narration.

## Read-before-write (ENFORCED)

Before patching an existing skill, call read_skill on it FIRST.`

const curatorInstruction = `
## YOUR TASK

Process every obvious cluster. Merge overlapping skills into umbrellas. If you
end the pass with fewer than 5 archives, you stopped too early. Write a summary
of what you consolidated.`

func enabledLabel(b bool) string {
	if b {
		return "ENABLED"
	}
	return "DISABLED"
}

func boolLabel(b bool, on, off string) string {
	if b {
		return on
	}
	return off
}

# Motita Self-Improvement Design

Three pathways for skill creation + maintenance, adapted from Hermes' architecture
to motita's single-process, stdlib-only, i386 Go constraints.

## Architecture overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                         app.go (entry)                              │
│                                                                     │
│  procedures.Open(cfg) ──► Store { Library, Ledger, Usage }         │
│                                                                     │
│  runPlan / runAgent                                                  │
│     │                                                                │
│     ▼                                                                │
│  Planner.Run(ctx, input) ──► final answer                           │
│     │                                                                │
│     ▼  (post-turn, goroutine)                                       │
│  Review.Run(ctx, transcript, store) ──► skill patches               │
│                                                                     │
│  Curator.Run(ctx, store, engine) ◄── (periodic, idle-triggered)    │
│     ├── deterministic: stale/archive by inactivity                  │
│     └── LLM consolidation (opt-in): umbrella-building               │
└─────────────────────────────────────────────────────────────────────┘
```

New packages:

| Package | Purpose | Lines (est.) |
|---|---|---|
| `internal/usage` | Per-skill telemetry: use/view/patch counts, timestamps, `created_by`, state | ~250 |
| `internal/review` | Post-turn background fork: replays transcript, patches skills | ~200 |
| `internal/curator` | Deterministic stale/archive + optional LLM consolidation | ~350 |

Modified packages:

| Package | Change |
|---|---|
| `internal/config` | Add `review:` and `curator:` blocks to `config.yaml` |
| `internal/procedures` | `Store` gains a `Usage *usage.Ledger` field |
| `internal/plan` | `Planner` bumps usage telemetry on skill tool calls; calls `Review.Run` after `finalize` |
| `internal/skills` | `Library` gains `.archive/` awareness + `Archive`/`Restore` methods |

---

## 1. Usage tracking (`internal/usage`)

A sidecar JSON file — `.usage.json` — next to the skills directory, same pattern
as `reward.Ledger`'s `.scores.json`. One entry per skill, keyed by sanitised name.

### Data model

```go
package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CreatedBy is the provenance marker: who created this skill.
// "agent"      — the background review fork (curator-managed).
// "foreground" — a user-directed save_skill during conversation (user-owned).
// ""           — pre-dates the marker, or a builtin (unmanaged).
type CreatedBy string

const (
	ByAgent      CreatedBy = "agent"
	ByForeground CreatedBy = "foreground"
)

// State is the curator lifecycle position.
type State string

const (
	StateActive   State = "active"
	StateStale    State = "stale"
	StateArchived State = "archived"
)

// Entry is one skill's telemetry.
type Entry struct {
	UseCount      int       `json:"use_count"`
	ViewCount     int       `json:"view_count"`
	PatchCount    int       `json:"patch_count"`
	LastUsedAt    time.Time `json:"last_used_at"`
	LastViewedAt  time.Time `json:"last_viewed_at"`
	LastPatchedAt time.Time `json:"last_patched_at"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     CreatedBy `json:"created_by"`
	State         State     `json:"state"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
}

// Ledger is the usage sidecar. Thread-safe, atomic saves, same pattern as
// reward.Ledger.
type Ledger struct {
	Path   string
	Now    func() time.Time
	mu     sync.Mutex
	entries map[string]Entry
	dirty   bool
}

func Open(path string) (*Ledger, error) {
	l := &Ledger{
		Path:    path,
		Now:     time.Now,
		entries: map[string]Entry{},
	}
	if path == "" {
		return l, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return l, nil
	}
	if err := json.Unmarshal(data, &(struct {
		Entries map[string]Entry `json:"entries"`
	}{l.entries})); err != nil {
		return nil, err
	}
	return l, nil
}

// BumpView records a read_skill call.
func (l *Ledger) BumpView(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[name]
	if e.CreatedAt.IsZero() {
		e.CreatedAt = l.Now()
	}
	e.ViewCount++
	e.LastViewedAt = l.Now()
	l.entries[name] = e
	l.dirty = true
}

// BumpUse records a skill being loaded into context (search result returned or
// read_skill completed).
func (l *Ledger) BumpUse(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[name]
	if e.CreatedAt.IsZero() {
		e.CreatedAt = l.Now()
	}
	e.UseCount++
	e.LastUsedAt = l.Now()
	l.entries[name] = e
	l.dirty = true
}

// BumpPatch records a save_skill call. Sets CreatedBy if the entry is new.
func (l *Ledger) BumpPatch(name string, by CreatedBy) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[name]
	if e.CreatedAt.IsZero() {
		e.CreatedAt = l.Now()
	}
	if e.CreatedBy == "" && by != "" {
		e.CreatedBy = by
	}
	e.PatchCount++
	e.LastPatchedAt = l.Now()
	e.State = StateActive // patching reactivates
	l.entries[name] = e
	l.dirty = true
}

// Get returns the entry for a skill, or a zero Entry if none exists.
func (l *Ledger) Get(name string) Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries[name]
}

// All returns a snapshot of all entries (for the curator).
func (l *Ledger) All() map[string]Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]Entry, len(l.entries))
	for k, v := range l.entries {
		out[k] = v
	}
	return out
}

// SetState updates a skill's lifecycle state.
func (l *Ledger) SetState(name string, s State) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[name]
	e.State = s
	if s == StateArchived {
		t := l.Now()
		e.ArchivedAt = &t
	} else {
		e.ArchivedAt = nil
	}
	l.entries[name] = e
	l.dirty = true
}

// IsCuratorManaged returns true if the skill may be touched by autonomous
// curation. Mirrors Hermes' rule: only created_by="agent" qualifies.
func (l *Ledger) IsCuratorManaged(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[name]
	return ok && e.CreatedBy == ByAgent
}

// Save writes the ledger atomically if it has changed since the last save.
func (l *Ledger) Save() error {
	l.mu.Lock()
	if !l.dirty || l.Path == "" {
		l.mu.Unlock()
		return nil
	}
	l.dirty = false
	entries := make(map[string]Entry, len(l.entries))
	for k, v := range l.entries {
		entries[k] = v
	}
	l.mu.Unlock()

	dir := filepath.Dir(l.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]any{"entries": entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".usage.*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), l.Path)
}
```

### Integration into `procedures.Store`

```go
// procedures.go — modified Store
type Store struct {
	Library *skills.Library
	Ledger  *reward.Ledger
	Usage   *usage.Ledger  // NEW
}

func Open(cfg config.Config, log *logx.Logger) *Store {
	dir := cfg.Skills.Dir
	if dir == "" {
		dir = config.Default().Skills.Dir
	}
	lib := skills.New(dir)
	lib.Builtins = true
	if cfg.Skills.MaxFileBytes > 0 {
		lib.MaxFileBytes = cfg.Skills.MaxFileBytes
	}
	st := &Store{Library: lib}
	// ... existing ledger open ...
	// NEW: usage sidecar
	ul, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		if log != nil {
			log.Warn("the usage ledger could not be read; curator is off", "error", err)
		}
	} else {
		st.Usage = ul
	}
	return st
}
```

### Integration into `plan.Planner` tool dispatch

In `plan.go`'s `runTool`, the `read_skill`, `search_skills`, and `save_skill`
cases bump the usage ledger:

```go
case "read_skill":
	// ... existing logic ...
	if p.usage != nil {
		p.usage.BumpView(name)
		p.usage.BumpUse(name)  // reading a skill = using it
	}
case "search_skills":
	// ... existing logic, after results are found ...
	if p.usage != nil {
		for _, s := range results {
			p.usage.BumpUse(s.Name)
		}
	}
case "save_skill":
	// ... existing save logic ...
	if p.usage != nil {
		by := usage.ByForeground
		if p.isReviewFork {
			by = usage.ByAgent
		}
		p.usage.BumpPatch(name, by)
	}
```

The `Planner` struct gains:
```go
usage      *usage.Ledger
isReviewFork bool  // true when this Planner is a background review fork
```

---

## 2. Background review fork (`internal/review`)

A **separate Planner** that runs after the main turn completes. It replays the
conversation transcript with a review-specific prompt and the skill tools only.

### Key design decisions

1. **Separate goroutine, not inline.** The user's answer is delivered first;
   the review runs concurrently and writes skills silently.
2. **Separate LLM client, optionally cheaper.** Config can route the review to
   a different provider/model (e.g. `deepseek-v4.1-flash` for the review while
   the main conversation uses a heavier model).
3. **Separate session.** The review fork gets its own `session.Session` — it
   does not pollute the main conversation.
4. **Tool whitelist.** The fork only gets `list_skills`, `search_skills`,
   `read_skill`, `save_skill` — no filesystem, no command execution.
5. **Provenance marker.** `save_skill` calls from the fork set
   `created_by: "agent"`, making them curator-managed.
6. **Cancellation.** When the user sends a new message, the review is
   cancelled (context timeout). Best-effort: partial writes are safe because
   `save_skill` is atomic.

### Config

```yaml
# Added to config.yaml
review:
  enabled: true           # false = skip post-turn reviews
  interval: 15            # review after every N tool-iterations without a save_skill
  timeout: 120s           # max wall time for one review
  max_iterations: 8       # max tool loops in the review fork
  llm:                    # optional: route to a cheaper model
    # provider: ollama
    # model: deepseek-v4.1-flash
    # base_url: https://ollama.com/v1
    # api_key: ${MOTITA_REVIEW_API_KEY}  # falls back to main key if unset
```

### Implementation

```go
package review

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/plan"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/session"
)

// Review is the post-turn background fork.
type Review struct {
	cfg      config.Review
	engine   *llm.Client   // separate client (may be a cheaper model)
	procs    *procedures.Store
	log      *logx.Logger
	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
}

func New(cfg config.Review, engine *llm.Client, procs *procedures.Store, log *logx.Logger) *Review {
	return &Review{cfg: cfg, engine: engine, procs: procs, log: log}
}

// MaybeRun triggers a review if enough tool-iterations have elapsed since the
// last save_skill. Called from the Planner's finalize path. Non-blocking: the
// review runs in a goroutine and the caller returns immediately.
func (r *Review) MaybeRun(parentCtx context.Context, transcript []llm.Message, itersSinceSkill int) {
	if !r.cfg.Enabled {
		return
	}
	if itersSinceSkill < r.cfg.Interval {
		return
	}

	// Cancel any in-flight review: a new turn takes priority.
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.Timeout)
	r.cancel = cancel
	r.running = true
	r.mu.Unlock()

	go r.run(ctx, transcript)
}

func (r *Review) run(ctx context.Context, transcript []llm.Message) {
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	// Build a fresh planner with review-only tools.
	// The review prompt is a stripped operationalPrompt: no file tools,
	// no command execution, just the skill library.
	reviewPrompt := reviewSystemPrompt

	runner := plan.New(r.engine, nil). // nil CommandRunner: no shell access
		WithLoops(r.cfg.MaxIterations).
		WithLibrary(r.procs.Library).
		WithReward(r.procs.Ledger).
		WithSoul(reviewPrompt)

	// Mark this planner as a review fork so save_skill sets created_by=agent.
	runner.IsReviewFork = true
	if r.procs.Usage != nil {
		runner.SetUsage(r.procs.Usage)
	}

	// Feed the transcript as a single user message asking for review.
	userMsg := buildReviewInput(transcript)
	result, err := runner.Run(ctx, userMsg)
	if err != nil {
		if r.log != nil {
			r.log.Warn("background review failed", "error", err)
		}
		return
	}
	if r.log != nil && !strings.Contains(strings.ToLower(result), "nothing to save") {
		r.log.Info("background review complete", "result", result[:200])
	}

	// Persist usage telemetry.
	if r.procs.Usage != nil {
		if err := r.procs.Usage.Save(); err != nil {
			r.log.Warn("usage ledger save failed", "error", err)
		}
	}
}

func buildReviewInput(transcript []llm.Message) string {
	var b strings.Builder
	b.WriteString("## CONVERSATION TRANSCRIPT\n\n")
	b.WriteString("The following is the conversation that just completed. ")
	b.WriteString("Review it for skill updates.\n\n")
	for _, m := range transcript {
		role := m.Role
		if m.ToolCallID != "" {
			role = "tool"
		}
		fmt.Fprintf(&b, "### %s\n%s\n\n", role, m.Content)
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "[tool call: %s(%s)]\n", tc.Function.Name, tc.Function.Arguments)
			}
		}
	}
	b.WriteString(reviewInstruction)
	return b.String()
}

// IsRunning reports whether a review is in flight (for graceful shutdown).
func (r *Review) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Wait blocks until any in-flight review completes (for shutdown).
func (r *Review) Wait(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		if !r.IsRunning() || time.Now().After(deadline) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
```

### Review prompt

```go
const reviewSystemPrompt = `You are the skill review pass of an autonomous agent.
Your ONLY job is to update the skill library based on the conversation that just
completed. You have four tools: list_skills, search_skills, read_skill, and
save_skill. You have NO filesystem access and NO command execution.

## When to act

Act if ANY of these signals fired in the conversation:
- The user corrected the agent's style, tone, format, verbosity, or workflow.
- A non-trivial technique, fix, workaround, or debugging path emerged.
- A skill that was loaded turned out wrong, missing a step, or outdated.
- The agent discovered a procedure that would save time next time.

## Preference order (pick the earliest that fits)

1. PATCH a skill that was loaded or read in the conversation. Call read_skill
   first to get the current content, modify it, then save_skill with the same
   name (it replaces the file).
2. SEARCH for an existing skill that covers the territory and patch it.
3. CREATE a new class-level skill only when nothing exists.

## Skill format

Each skill is one markdown file. The name is lowercase, hyphenated, at the
CLASS level — never a PR number, error string, date, or "fix-X" session artifact.

Structure:
  # Skill Title
  One line: when to use this skill.
  ## Procedure
  1. Step one (concrete command or tool call)
  2. Step two
  ## Pitfalls
  - Rule + one clause of WHY (the mechanism), imperative.

## What NOT to capture

- Environment-dependent failures (missing binaries, unconfigured credentials).
- Negative claims about tools ("X is broken").
- Session-specific transient errors that resolved before the conversation ended.
- One-off task narratives.
- Unresolved failures: if nothing worked, do NOT write the dead ends as a
  "reliable workflow".

## Read-before-write (ENFORCED)

Before patching an existing skill, call read_skill on it FIRST. A save_skill
without a prior read_skill for an existing skill is refused.

If nothing is worth saving, say "Nothing to save." and stop.`

const reviewInstruction = `
## YOUR TASK

Review the transcript above. If a skill should be created or patched, do it now
using your tools. If nothing warrants a skill update, say "Nothing to save." and
stop. Act on real signal — a pass that does nothing when there is signal is a
missed learning opportunity, but "Nothing to save." is valid when the session
was smooth.`
```

### Integration into `plan.Planner.Run`

After `finalize`, before returning:

```go
// plan.go — in Run(), after the final answer is produced

func (p *Planner) Run(ctx context.Context, input string) (string, error) {
	// ... existing loop ...
	answer := p.finalize(reply.Content)

	// NEW: trigger background review if configured.
	if p.review != nil {
		p.review.MaybeRun(ctx, sess.Messages(), p.itersSinceSkill)
	}

	return answer, nil
}
```

The `Planner` struct gains:
```go
review          *review.Review
itersSinceSkill int  // bumped each tool iteration, reset on save_skill
```

And in `runTool`, the `save_skill` case resets the counter:
```go
case "save_skill":
	// ... existing save ...
	p.itersSinceSkill = 0
```

And in the tool-call loop, after each iteration:
```go
p.itersSinceSkill++
```

### Config struct

```go
// config.go — new block
type Review struct {
	Enabled       bool          `yaml:"enabled"`
	Interval      int           `yaml:"interval"`
	Timeout       time.Duration `yaml:"timeout"`
	MaxIterations int           `yaml:"max_iterations"`
	LLM           *LLM          `yaml:"llm"`  // nil = reuse main engine
}

// Config gains:
type Config struct {
	// ... existing fields ...
	Review Review `yaml:"review"`
	Curator Curator `yaml:"curator"`
}
```

### Engine construction in `app.go`

```go
// app.go — in runPlan / runAgent, after building the main engine

var reviewEngine *llm.Client
if cfg.Review.LLM != nil && cfg.Review.LLM.APIKey != "" {
	reviewEngine, err = op.newEngine(*cfg.Review.LLM, log)
	if err != nil {
		log.Warn("review engine could not be built; reviews use the main engine", "error", err)
	}
}
if reviewEngine == nil {
	reviewEngine = engine  // fall back to the main engine
}

reviewer := review.New(cfg.Review, reviewEngine, procs, log)

planner := plan.New(engine, ag).
	WithLibrary(procs.Library).
	WithReward(procs.Ledger).
	WithReview(reviewer).  // NEW
	// ...
```

---

## 3. Curator (`internal/curator`)

Two phases, same as Hermes:
1. **Deterministic** (no LLM): mark stale, archive by inactivity.
2. **LLM consolidation** (opt-in): umbrella-building fork.

### Config

```yaml
curator:
  enabled: true
  interval_hours: 168        # 7 days between runs
  min_idle_minutes: 120      # only run after 2h idle
  stale_after_days: 14       # mark stale after 14d unused
  archive_after_days: 30     # move to .archive/ after 30d unused
  consolidate: false         # LLM umbrella-building pass (opt-in)
```

### Deterministic pass

```go
package curator

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

type Curator struct {
	cfg   config.Curator
	procs *procedures.Store
	log   *logx.Logger
	now   func() time.Time

	lastRunAt time.Time
	mu        sync.Mutex
}

func New(cfg config.Curator, procs *procedures.Store, log *logx.Logger) *Curator {
	return &Curator{
		cfg:   cfg,
		procs: procs,
		log:   log,
		now:   time.Now,
	}
}

// MaybeRun triggers a curation pass if enough time has passed and the agent
// has been idle. Called on session start (like Hermes) or via a CLI command.
func (c *Curator) MaybeRun(ctx context.Context) error {
	c.mu.Lock()
	if !c.cfg.Enabled {
		c.mu.Unlock()
		return nil
	}
	if time.Since(c.lastRunAt) < c.cfg.IntervalHours*time.Hour {
		c.mu.Unlock()
		return nil
	}
	c.lastRunAt = c.now()
	c.mu.Unlock()

	return c.Run(ctx)
}

// Run executes one curation pass: deterministic transitions, then optional
// LLM consolidation.
func (c *Curator) Run(ctx context.Context) error {
	if err := c.deterministicPass(); err != nil {
		return err
	}
	if c.cfg.Consolidate {
		return c.consolidationPass(ctx)
	}
	return nil
}

func (c *Curator) deterministicPass() error {
	if c.procs.Usage == nil {
		return nil
	}
	entries := c.procs.Usage.All()
	staleAfter := time.Duration(c.cfg.StaleAfterDays) * 24 * time.Hour
	archiveAfter := time.Duration(c.cfg.ArchiveAfterDays) * 24 * time.Hour

	for name, entry := range entries {
		// Only touch curator-managed skills.
		if entry.CreatedBy != usage.ByAgent {
			continue
		}
		// Never archive a skill younger than stale_after_days with 0 uses.
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
			if err := c.archiveSkill(name); err != nil {
				c.log.Warn("failed to archive skill", "skill", name, "error", err)
			}
		case idle >= staleAfter && entry.State == usage.StateActive:
			c.procs.Usage.SetState(name, usage.StateStale)
			c.log.Info("skill marked stale", "skill", name, "idle", idle)
		}
	}
	return c.procs.Usage.Save()
}

func (c *Curator) archiveSkill(name string) error {
	dir := c.procs.Library.Dir
	src := filepath.Join(dir, name+".md")
	archiveDir := filepath.Join(dir, ".archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(archiveDir, name+".md")
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	c.procs.Usage.SetState(name, usage.StateArchived)
	c.log.Info("skill archived", "skill", name)
	return nil
}

// Restore moves an archived skill back to the active directory.
func (c *Curator) Restore(name string) error {
	dir := c.procs.Library.Dir
	src := filepath.Join(dir, ".archive", name+".md")
	dst := filepath.Join(dir, name+".md")
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	c.procs.Usage.SetState(name, usage.StateActive)
	return c.procs.Usage.Save()
}
```

### LLM consolidation pass

```go
func (c *Curator) consolidationPass(ctx context.Context) error {
	// Build a review fork with the curator-specific prompt.
	// Same pattern as review.Review but with a different system prompt.
	runner := plan.New(c.engine, nil).
		WithLoops(16).
		WithLibrary(c.procs.Library).
		WithReward(c.procs.Ledger).
		WithSoul(curatorSystemPrompt)
	runner.IsReviewFork = true
	if c.procs.Usage != nil {
		runner.SetUsage(c.procs.Usage)
	}

	// Build the candidate list: all curator-managed skills with their metadata.
	candidates := c.buildCandidateList()
	result, err := runner.Run(ctx, candidates)
	if err != nil {
		return err
	}
	c.log.Info("curator consolidation complete", "result", result[:300])
	return c.procs.Usage.Save()
}

func (c *Curator) buildCandidateList() string {
	// List all skills with usage metadata, formatted for the LLM.
	entries := c.procs.Usage.All()
	var b strings.Builder
	b.WriteString("## CANDIDATE SKILLS\n\n")
	all, _ := c.procs.Library.List()
	for _, s := range all {
		e := entries[s.Name]
		if e.CreatedBy != usage.ByAgent {
			continue  // only curator-managed
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
```

### Curator prompt (abbreviated — full version in the design)

```go
const curatorSystemPrompt = `You are the skill CURATOR. This is an UMBRELLA-BUILDING
consolidation pass, not a passive audit. You have list_skills, search_skills,
read_skill, and save_skill. You have NO filesystem access.

## Goal

The skill collection should be a LIBRARY OF CLASS-LEVEL INSTRUCTIONS. A
collection of hundreds of narrow skills where each captures one session's
specific bug is a FAILURE. One broad umbrella skill with labeled subsections
beats five narrow siblings for discoverability.

## Hard rules

1. DO NOT touch skills you did not create (created_by != "agent").
2. DO NOT delete skills. Archiving is the maximum destructive action.
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
```

### CLI commands

```bash
motita curator status        # last run, counts, LRU top 5
motita curator run           # trigger a run now
motita curator run --consolidate  # force LLM consolidation
motita curator run --dry-run      # preview only
motita curator pin <skill>   # protect from auto-transitions
motita curator unpin <skill>
motita curator restore <skill>  # un-archive
motita curator list-archived
```

### Idle detection

Motita has no persistent gateway process for idle timing. Two approaches:

1. **Session-start check** (like Hermes CLI): on every `motita` invocation,
   check if `interval_hours` has elapsed since `lastRunAt` (stored in
   `~/.motita/.curator_state`). If yes, run the deterministic pass before
   the user's task starts. Cheap (no LLM) and non-blocking for the main task.

2. **Gateway hook** (if `motita serve` is running): the gateway already tracks
   session activity. A goroutine checks `min_idle_minutes` every 5 minutes and
   runs the curator when the machine is quiet.

```go
// .curator_state — JSON, beside the config
{
  "last_run_at": "2026-09-25T00:00:00Z",
  "last_run_summary": "auto: 0 stale, 0 archived; llm: skipped",
  "run_count": 3
}
```

---

## 4. System prompt for the three pathways

This block goes into `~/.motita/SOUL.md` (or the embedded `soul.md`) as an
extension of the `## Your library of procedures` section, replacing the
existing "When to save" paragraph:

```markdown
## Skill Creation — Three Pathways

You have a procedure library: markdown files in your skills directory. A skill
is the instructions for doing a class of task the most efficient and correct
way — the procedure, the commands that work, the order, the pitfalls that cost
time. A future session should be able to follow it and produce the right result
on the first try. Skills are NOT session logs, NOT reports of what you did, NOT
narratives of what happened. They are reusable instructions.

### Skill Format

Each skill is one file: `<name>.md` in the skills directory. The name is
lowercase, hyphenated, at the CLASS level — never a PR number, an error string,
a date, or "fix-X-debug-Y". The file structure:

    # Skill Title

    One line: when to use this skill.

    ## Procedure
    1. Step one (concrete command or tool call)
    2. Step two
    3. ...

    ## Pitfalls
    - Rule + one clause of WHY (the mechanism), imperative.
      "Grep the test tree for the SYMBOL before widening a helper signature —
      hand-rolled mocks reimplement the old shape and fail on a shard you did not run."
      NOT: "in session XYZ we hit a bug because..."

Rules for content:
- A pitfall is a generalizable rule + one clause of WHY. Not a story.
- No PR/issue numbers, dates, ticket IDs, or quoted user text as content.
- The same lesson learned twice is ONE rule. Search the skill before adding.
- Not a duplicate of what the environment already teaches (AGENTS.md, tool schemas).
- Never save: environment-dependent failures (missing binaries, unconfigured
  credentials), negative claims about tools ("X is broken"), one-off task
  narratives, or unresolved failures (if nothing worked, do NOT write the dead
  ends as a "reliable workflow").

### Pathway 1 — Foreground (during the conversation)

You decide to save or patch a skill DURING the conversation, when one of these
signals fires:

- You worked out a non-trivial workflow worth repeating (multi-step, with
  commands that worked and pitfalls you hit).
- The user corrected your approach, style, format, or sequence of steps.
  Encode the correction as a pitfall or explicit step.
- You hit errors or dead ends and found the working path.
- A skill you loaded turned out to be wrong, missing a step, or outdated.
  Patch it NOW with save_skill (it replaces the file).

Preference order (pick the earliest that fits):
1. PATCH a skill you already loaded or read this session.
2. SEARCH for an existing skill that covers the territory and patch it.
3. CREATE a new class-level skill only when nothing exists.

When you save: use save_skill with a class-level name and the full markdown body.
When you patch: read_skill first, modify, save_skill with the same name (it
replaces). Never append "UPDATE: actually..." — edit the sentence that misled.

### Pathway 2 — Background Review (after the task completes)

After you deliver your final answer, a background review pass runs
automatically. It replays the conversation and patches the skill library
using the same tools. You do not control this pass — it runs in a separate
session with its own context window and optionally a cheaper model.

The review pass uses the same signals as Pathway 1 and the same preference
order. Skills it creates are marked `created_by: agent`, making them eligible
for automatic maintenance (the curator).

If the session ran smoothly with no corrections and no new technique, the
review says "Nothing to save." and stops — that is a valid outcome.

### Pathway 3 — Curator Consolidation (periodic maintenance)

Periodically (default: every 7 days when the agent is idle), a curator pass
maintains the library:

1. DETERMINISTIC (no LLM): skills unused for 14 days are marked STALE; skills
   unused for 30 days are moved to .archive/ (recoverable). Never-used skills
   get a grace period equal to stale_after_days before they can be archived.
2. LLM CONSOLIDATION (opt-in): the curator reviews all agent-created skills,
   identifies prefix clusters, and merges overlapping skills into class-level
   umbrellas with labeled subsections. Distills content into rules, never
   copies verbatim.

Archived skills are recoverable with `motita curator restore <name>`. Pinned
skills are exempt from all auto-transitions.
```

---

## 5. Implementation order

| Phase | Package(s) | Est. effort | Depends on |
|---|---|---|---|
| 1 | `internal/usage` | 250 LOC + tests | nothing |
| 2 | `internal/config` (add structs) | 30 LOC | nothing |
| 3 | `internal/procedures` (Store.Usage) | 15 LOC | phase 1, 2 |
| 4 | `internal/plan` (telemetry bumps) | 40 LOC | phase 3 |
| 5 | `internal/skills` (Archive/Restore) | 50 LOC | nothing |
| 6 | `internal/review` | 200 LOC + tests | phase 4 |
| 7 | `internal/curator` | 350 LOC + tests | phase 5, 6 |
| 8 | `internal/app` (wiring) | 60 LOC | phase 6, 7 |
| 9 | SOUL.md prompt update | ~2 KB text | nothing |
| 10 | CLI subcommands | 100 LOC | phase 7 |

Total: ~1100 LOC + tests. Phases 1-5 can land independently (no behavior change
without phase 6). Phase 6 enables Pathway 2. Phase 7 enables Pathway 3.
Phase 9 is a file edit, no code.
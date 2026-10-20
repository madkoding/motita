package config

import "time"

// ---------------------------------------------------------------------------
// Self-improvement configuration
// ---------------------------------------------------------------------------

// Review configures the background self-improvement fork: after a turn
// completes, a separate planner replays the transcript and patches the skill
// library. It runs in its own goroutine with optionally a cheaper LLM.
type Review struct {
	Enabled       bool          `yaml:"enabled"`
	Interval      int           `yaml:"interval"` // review after N tool-iterations without a save_skill
	Timeout       time.Duration `yaml:"timeout"`
	MaxIterations int           `yaml:"max_iterations"`
	LLM           *LLM          `yaml:"llm"` // nil = reuse the main engine
}

// Curator configures periodic skill library maintenance: deterministic
// stale/archive transitions (no LLM) and optional LLM consolidation.
type Curator struct {
	Enabled       bool `yaml:"enabled"`
	IntervalHours int  `yaml:"interval_hours"`
	// MinIdleMinutes is deprecated and ignored: the automatic pass runs when a session starts,
	// and no gateway-side scheduler measures idleness. Still read so existing files keep
	// loading; Deprecations reports it.
	MinIdleMinutes   int    `yaml:"min_idle_minutes"`
	StaleAfterDays   int    `yaml:"stale_after_days"`
	ArchiveAfterDays int    `yaml:"archive_after_days"`
	Consolidate      bool   `yaml:"consolidate"`
	StateFile        string `yaml:"state_file"`
}

// Package procedures builds the two things that travel together: the agent's library of
// procedure documents, and the ledger that records how each one has worked out.
//
// It exists because building them is not the private business of one front end. The prompts
// that promise the model a library are shipped in internal/config and used by EVERY path — the
// interactive interface, the command line's task run, the command line's plan run — while the
// wiring lived in exactly one of them. The other two told the model it had a shelf of
// procedures and then answered "no procedure library is configured" when it reached for one,
// which burns the turn and teaches the model that the tools it was given do not work.
//
// So the construction is here, in one place, and every caller uses it. A front end may still
// cache the result; what it must not do is have its own idea of what the library is.
//
// The ledger lives NEXT TO the library, in the same directory, so one thing to copy or back up
// carries both the procedures and what has been learned about them.
package procedures

import (
	"path/filepath"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/reward"
	"github.com/madkoding/starlight/internal/skills"
)

// Store is the library and its ledger, as one thing.
type Store struct {
	// Library is always present: a store without a library would be a store that cannot
	// answer the tools the prompt advertises.
	Library *skills.Library
	// Ledger is nil when it could not be read, and the library then ranks exactly as it did
	// before the feature existed. An absent ledger is a working library, not a broken one —
	// see Open for why it is not an error.
	Ledger *reward.Ledger
}

// Open builds the store named by a configuration.
//
// An unset directory falls back to the home rather than to the working directory: the library
// is starlight's own state, and a configuration that names nothing must not scatter it through
// whatever project the user happens to be standing in.
//
// A ledger that cannot be read is reported and then IGNORED, not returned as an error. The
// scores are the only record of what the user thought of the library, so silently starting
// empty would hide that they were lost; but refusing to build the library over it would cost
// the procedures too, and a library without its scores still works. The caller gets the library
// and a nil ledger, which is exactly what "the value-based ranking is off" means.
func Open(cfg config.Config, log *logx.Logger) *Store {
	dir := cfg.Skills.Dir
	if dir == "" {
		dir = config.Default().Skills.Dir
	}

	lib := skills.New(dir)
	// The shipped procedures are part of what the agent sees: they describe the tools it was
	// given and the guardrails it will meet, and a fresh install would otherwise start with an
	// empty shelf and a model deriving both from refusals.
	lib.Builtins = true
	if cfg.Skills.MaxFileBytes > 0 {
		lib.MaxFileBytes = cfg.Skills.MaxFileBytes
	}

	st := &Store{Library: lib}
	led, err := reward.Open(filepath.Join(dir, ".scores.json"))
	if err != nil {
		if log != nil {
			log.Warn("the reward ledger could not be read; value-based ranking is off", "error", err)
		}
		return st
	}
	st.Ledger = led
	// The library reads the ledger for its tie-breaks, so the search prefers the procedure
	// that has actually worked when two of them fit the question equally well.
	lib.Scorer = led
	return st
}

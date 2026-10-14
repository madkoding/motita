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
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/projectskills"
	"github.com/madkoding/motita/internal/reward"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
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
	// Usage is the per-skill telemetry sidecar: view/use/patch counts, provenance
	// markers, and lifecycle state. Nil when it could not be read; the curator
	// and the background review fork are disabled without it.
	Usage *usage.Ledger
}

// Open builds the store named by a configuration.
//
// An unset directory falls back to the home rather than to the working directory: the library
// is motita's own state, and a configuration that names nothing must not scatter it through
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

	// Usage telemetry sidecar: per-skill counts, provenance, and lifecycle state.
	// An absent or unreadable ledger disables the curator and the background review
	// fork, but the library still works — the feature degrades to "no telemetry".
	ul, err := usage.Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		if log != nil {
			log.Warn("the usage ledger could not be read; curator and review are off", "error", err)
		}
		return st
	}
	st.Usage = ul
	// The library ASKS the ledger which documents are turned off, at the doors the model
	// reaches the library through. It is wired here because this is where the two sidecars
	// meet the one directory, and because it is the only place that can: internal/skills
	// must not import internal/usage.
	st.Library.Hidden = ul.Disabled
	return st
}

// projectLedgers are a project's own telemetry, opened once per process: two sessions of one
// project must share ONE in-memory ledger over the file, or each would save its view over the
// other's (see UseStore in internal/tui).
type projectLedgers struct {
	reward *reward.Ledger
	usage  *usage.Ledger
}

// projectKey is what a project's ledgers are cached under: the file and the shared ledgers they
// are layered over (a test may open several shared stores over one directory).
type projectKey struct {
	dir    string
	reward *reward.Ledger
	usage  *usage.Ledger
}

var (
	projectMu  sync.Mutex
	projectLed = map[projectKey]projectLedgers{}
)

// Scoped is the store a session of a project works with: the shared library with the project's
// documents layered over it (projectskills.Scope), and ledgers of the project's own layered over
// the shared ones, so a verdict, a count, a pin or a switch-off of a project's document stays in
// that project while the shared documents keep their one record. An empty projectDir is the
// shared store itself, unscoped.
//
// The project's ledgers live in motita's own state, beside the shared ones, under
// .projects/<key>: never in the project's checkout, where they would be a change to commit.
func Scoped(shared *Store, projectDir string) *Store {
	out := *shared
	if shared.Library == nil {
		return &out
	}
	lib := *shared.Library
	projectskills.Scope(&lib, projectDir)
	out.Library = &lib
	if strings.TrimSpace(projectDir) == "" {
		return &out
	}
	dir := filepath.Join(shared.Library.Root(), ProjectsDir, projectskills.Key(projectDir))
	led := projectLedgersFor(projectKey{dir: dir, reward: shared.Ledger, usage: shared.Usage}, lib.InScope,
		sharedHas(shared.Library))
	if led.reward != nil {
		out.Ledger = led.reward
		lib.Scorer = led.reward
	}
	if led.usage != nil {
		out.Usage = led.usage
		lib.Hidden = led.usage.Disabled
	}
	return &out
}

// ProjectsDir is where the projects' own ledgers are kept, inside the shared library directory.
// It is a dot-name, so the library's index never reads it.
const ProjectsDir = ".projects"

// projectLedgersFor opens (once) the ledgers of the project at key.dir, layered over the shared
// ones. A ledger the shared store does not have is not opened for the project either, and one
// that cannot be read leaves the project on the shared one: a missing record is a working
// library, as Open says.
//
// When they are first opened, what the shared ledgers recorded about the project's documents
// before projects had ledgers of their own moves to the project (Adopt): it stays shared too only
// while the shared shelf still has a document of that name.
func projectLedgersFor(key projectKey, owns, keep func(string) bool) projectLedgers {
	projectMu.Lock()
	defer projectMu.Unlock()
	if led, ok := projectLed[key]; ok {
		return led
	}
	var led projectLedgers
	if key.reward != nil {
		if r, err := reward.Open(filepath.Join(key.dir, ".scores.json")); err == nil {
			led.reward = r.Over(key.reward, owns)
			if led.reward.Adopt(keep) {
				_ = led.reward.Save()
			}
		}
	}
	if key.usage != nil {
		if u, err := usage.Open(filepath.Join(key.dir, ".usage.json")); err == nil {
			led.usage = u.Over(key.usage, owns)
			if led.usage.Adopt(keep) {
				_ = led.usage.Save()
			}
		}
	}
	projectLed[key] = led
	return led
}

// sharedHas reports whether the shared shelf itself, not a project, has a document of a name, live
// or archived.
func sharedHas(lib *skills.Library) func(string) bool {
	return func(name string) bool {
		for _, p := range []string{name + ".md", filepath.Join(skills.ArchiveDir, name+".md")} {
			if _, err := os.Lstat(filepath.Join(lib.Root(), p)); err == nil {
				return true
			}
		}
		return false
	}
}

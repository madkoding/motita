// Package usage tracks per-skill telemetry for the curator: how often each
// skill is viewed, used, and patched, plus the provenance marker that decides
// whether autonomous curation may touch it.
//
// It is a sidecar JSON file — .usage.json — that lives next to the skills
// directory, the same pattern as reward.Ledger's .scores.json. One entry per
// skill, keyed by the sanitised skill name. The file is never inlined into a
// skill's markdown body: the body is text the model writes and rewrites, and
// embedding state in it would mean the next save_skill silently wipes the
// history.
//
// The provenance marker (created_by) is a POLICY flag, not an authorship claim.
// "agent" means "autonomous curation may touch this"; "foreground" means
// "a user-directed save_skill created this, and it is off-limits to the
// curator". The marker is set by the caller at save_skill time: the background
// review fork sets "agent", a foreground conversation sets "foreground".
package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CreatedBy is the provenance marker: who created this skill.
type CreatedBy string

const (
	// ByAgent means the background review fork created the skill, and it is
	// curator-managed: the deterministic pass may mark it stale or archive it,
	// and the LLM consolidation pass may merge it into an umbrella.
	ByAgent CreatedBy = "agent"
	// ByForeground means a user-directed save_skill created the skill during a
	// conversation. It is user-owned and off-limits to autonomous curation.
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
	UseCount      int        `json:"use_count"`
	ViewCount     int        `json:"view_count"`
	PatchCount    int        `json:"patch_count"`
	LastUsedAt    time.Time  `json:"last_used_at"`
	LastViewedAt  time.Time  `json:"last_viewed_at"`
	LastPatchedAt time.Time  `json:"last_patched_at"`
	CreatedAt     time.Time  `json:"created_at"`
	CreatedBy     CreatedBy  `json:"created_by"`
	State         State      `json:"state"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	Pinned        bool       `json:"pinned,omitempty"`
	// Disabled means the user turned this skill off: the agent no longer sees it in
	// the index or the search, and autonomous curation leaves it alone. The document
	// stays on disk and stays listed in the interface, so "off" is one click away
	// from "on" and nothing about it is lost.
	Disabled bool `json:"disabled,omitempty"`
}

// Ledger is the usage sidecar. Thread-safe, atomic saves.
type Ledger struct {
	Path    string
	Now     func() time.Time
	mu      sync.Mutex
	entries map[string]Entry
	dirty   bool

	// parent and owns make this a LAYER: a project's own ledger over the shared one. A name this
	// ledger has an entry for, or one owns claims (a document in the project's own directory), is
	// answered here; every other name is the shared ledger's. See Over.
	parent *Ledger
	owns   func(name string) bool
}

// Over makes l the ledger of a scope layered over shared: the scope's own documents (owns) are
// counted, pinned and switched off here, and the shared documents a scoped session reads stay
// on the shared ledger, so the curator still sees them used. It returns l.
//
// It is what keeps one project's telemetry out of another's: two projects with a document of the
// same name used to share one entry, so turning it off in one hid it in the other.
func (l *Ledger) Over(shared *Ledger, owns func(name string) bool) *Ledger {
	l.parent, l.owns = shared, owns
	return l
}

// route is the ledger that answers for name: l itself, or the shared one it is layered over.
func (l *Ledger) route(name string) *Ledger {
	if l.parent == nil {
		return l
	}
	l.mu.Lock()
	_, mine := l.entries[name]
	l.mu.Unlock()
	if mine || (l.owns != nil && l.owns(name)) {
		return l
	}
	return l.parent
}

// Open loads the ledger at path, or returns an empty one if there is nothing
// there. A ledger that cannot be read is reported rather than silently
// replaced: losing the history would lose the provenance markers that protect
// user-owned skills from autonomous curation.
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
		return nil, fmt.Errorf("could not read the usage ledger %s: %w", path, err)
	}
	if len(data) == 0 {
		return l, nil
	}
	var wrapper struct {
		Entries map[string]Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("the usage ledger %s is not valid JSON: %w", path, err)
	}
	if wrapper.Entries != nil {
		l.entries = wrapper.Entries
	}
	return l, nil
}

// BumpView records a read_skill call.
func (l *Ledger) BumpView(name string) {
	if t := l.route(name); t != l {
		t.BumpView(name)
		return
	}
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

// BumpUse records a skill being loaded into context: a read_skill completed. A search
// only returns summaries nobody acted on, so it does not count.
func (l *Ledger) BumpUse(name string) {
	if t := l.route(name); t != l {
		t.BumpUse(name)
		return
	}
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
	if t := l.route(name); t != l {
		t.BumpPatch(name, by)
		return
	}
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
	if t := l.route(name); t != l {
		return t.Get(name)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries[name]
}

// All returns a snapshot of all entries (for the curator).
func (l *Ledger) All() map[string]Entry {
	out := map[string]Entry{}
	if l.parent != nil {
		// The shared entries first, the scope's over them: a document of the scope shadows the
		// shared one of the same name, as it does in the library.
		out = l.parent.All()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, v := range l.entries {
		out[k] = v
	}
	return out
}

// SetState updates a skill's lifecycle state.
func (l *Ledger) SetState(name string, s State) {
	if t := l.route(name); t != l {
		t.SetState(name, s)
		return
	}
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

// SetPinned marks a skill as exempt from all auto-transitions.
func (l *Ledger) SetPinned(name string, pinned bool) {
	if t := l.route(name); t != l {
		t.SetPinned(name, pinned)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[name]
	e.Pinned = pinned
	l.entries[name] = e
	l.dirty = true
}

// SetDisabled turns a skill off or back on.
//
// It is deliberately NOT a lifecycle state: a disabled skill is not stale and not
// archived, it is a document the user asked the agent to stop using. Recording it as
// a state would make the curator's transitions and the user's decision the same field,
// and the first automatic pass would move a skill the user had only switched off.
func (l *Ledger) SetDisabled(name string, disabled bool) {
	if t := l.route(name); t != l {
		t.SetDisabled(name, disabled)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[name]
	e.Disabled = disabled
	l.entries[name] = e
	l.dirty = true
}

// Disabled reports whether a skill was turned off. A name with no entry is not
// disabled: asking the question must not create telemetry for a skill nobody has.
func (l *Ledger) Disabled(name string) bool {
	if t := l.route(name); t != l {
		return t.Disabled(name)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries[name].Disabled
}

// Forget removes an entry and its history.
//
// It exists for the one operation that makes a name stop existing: a deleted document leaves no
// subject for the counts to describe, and a survivor would be handed to the curator as a skill
// that cannot be found. A name re-created later must start from zero rather than arrive carrying
// the counters and complaints of the one that was deleted.
//
// Forgetting a name that has no entry is not an error: the caller asked for an absence and an
// absence is what it has. It must not create one either.
func (l *Ledger) Forget(name string) {
	if t := l.route(name); t != l {
		t.Forget(name)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.entries[name]; !ok {
		return
	}
	delete(l.entries, name)
	l.dirty = true
}

// IsCuratorManaged returns true if the skill may be touched by autonomous
// curation. Mirrors Hermes' rule: only created_by="agent" qualifies.
func (l *Ledger) IsCuratorManaged(name string) bool {
	if t := l.route(name); t != l {
		return t.IsCuratorManaged(name)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[name]
	return ok && e.CreatedBy == ByAgent && !e.Pinned
}

// jsonMarshalIndent is the one call to json.MarshalIndent, held in a variable so
// that a test can make it fail. The entries are always JSON-safe (int, time.Time,
// string, bool), so the error is unreachable in a correct build — exactly the
// shape the coverage gate rejects.
var jsonMarshalIndent = func(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// osCreateTemp is the one call to os.CreateTemp, held in a variable so that a
// test can make it return a file that fails on Write or Close. Like the seams in
// internal/webui, this makes an otherwise-unreachable failure branch reachable.
var osCreateTemp = os.CreateTemp

// closeFile is the one call to tmp.Close, held in a variable so that a test can
// make Close fail after a successful Write. Close on a regular file essentially
// never fails, so the error path is unreachable without the seam.
var closeFile = func(f *os.File) error { return f.Close() }

// Save writes the ledger atomically if it has changed since the last save, and the shared one a
// scope is layered over, where the shared documents' counts went.
func (l *Ledger) Save() error {
	err := l.save()
	if l.parent != nil {
		if perr := l.parent.Save(); err == nil {
			err = perr
		}
	}
	return err
}

// save writes this ledger alone.
func (l *Ledger) save() error {
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
		return fmt.Errorf("could not create the usage ledger directory: %w", err)
	}
	data, err := jsonMarshalIndent(map[string]any{"entries": entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := osCreateTemp(dir, ".usage.*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		closeFile(tmp)
		return err
	}
	if err := closeFile(tmp); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), l.Path)
}

// Adopt moves to a layer what the shared ledger it is layered over recorded, before projects had
// ledgers of their own, about the scope's documents: an entry for a name owns claims and this
// layer has none yet is copied here, and left on the shared ledger only when keep says the shared
// shelf still has a document of that name. It reports whether anything moved; both ledgers are
// then saved by the next Save.
func (l *Ledger) Adopt(keep func(name string) bool) bool {
	if l.parent == nil || l.owns == nil {
		return false
	}
	moved := false
	for name, e := range l.parent.All() {
		l.mu.Lock()
		_, mine := l.entries[name]
		take := !mine && l.owns(name)
		if take {
			l.entries[name] = e
			l.dirty, moved = true, true
		}
		l.mu.Unlock()
		if take && (keep == nil || !keep(name)) {
			l.parent.Forget(name)
		}
	}
	return moved
}

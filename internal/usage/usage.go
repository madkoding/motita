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
}

// Ledger is the usage sidecar. Thread-safe, atomic saves.
type Ledger struct {
	Path    string
	Now     func() time.Time
	mu      sync.Mutex
	entries map[string]Entry
	dirty   bool
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

// BumpUse records a skill being loaded into context (a search result returned
// or a read_skill completed). Distinct from BumpView because a search returns
// many summaries without the user reading the full procedure, and the curator
// cares about both signals.
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

// SetPinned marks a skill as exempt from all auto-transitions.
func (l *Ledger) SetPinned(name string, pinned bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[name]
	e.Pinned = pinned
	l.entries[name] = e
	l.dirty = true
}

// IsCuratorManaged returns true if the skill may be touched by autonomous
// curation. Mirrors Hermes' rule: only created_by="agent" qualifies.
func (l *Ledger) IsCuratorManaged(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[name]
	return ok && e.CreatedBy == ByAgent && !e.Pinned
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
		return fmt.Errorf("could not create the usage ledger directory: %w", err)
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

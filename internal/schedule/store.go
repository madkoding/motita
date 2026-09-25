package schedule

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store persists scheduled tasks to disk as one JSON file per task.
//
// The shape is deliberately the same as the project and session stores in the gateway:
// a mutex around the directory, a write to a temporary file followed by a rename, and a
// load that skips what it cannot read. Three stores that behave the same way are three
// stores a reader only has to learn once - and the atomic write is the property that
// matters, because a process killed mid-save must not leave a truncated record behind.
type Store struct {
	mu  sync.Mutex
	dir string
}

// Open roots a store at dir, creating it if it does not exist.
//
// A store whose directory is missing would report itself as working and silently drop
// every save, so a failure to create it is a failure to return a store.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("the schedule store needs a directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create the schedule directory %q: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Path is where one task's record lives. It is exported because a front end that
// shows an operator where to look should not rebuild the rule.
func (st *Store) Path(id string) string { return filepath.Join(st.dir, id+".json") }

// Save writes one task, atomically.
func (st *Store) Save(s Schedule) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode the schedule %q: %w", s.ID, err)
	}
	tmp := st.Path(s.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("could not write the schedule %q: %w", s.ID, err)
	}
	if err := os.Rename(tmp, st.Path(s.ID)); err != nil {
		return fmt.Errorf("could not save the schedule %q: %w", s.ID, err)
	}
	return nil
}

// Load reads one task. It returns (nil, nil) when there is no such record, and an
// ERROR when there is one that cannot be read: "does not exist" and "exists but is
// broken" are different answers with different fixes.
func (st *Store) Load(id string) (*Schedule, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	data, err := os.ReadFile(st.Path(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the schedule %q: %w", id, err)
	}
	var s Schedule
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("could not parse the schedule %q: %w", id, err)
	}
	return &s, nil
}

// LoadAll reads every task, sorted by creation time so a list is stable between reads.
func (st *Store) LoadAll() ([]Schedule, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	entries, err := os.ReadDir(st.dir)
	if err != nil {
		return nil, fmt.Errorf("could not read the schedule directory %q: %w", st.dir, err)
	}
	var out []Schedule
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(st.dir, e.Name()))
		if err != nil {
			continue // vanished or unreadable: skipped, not fatal
		}
		var s Schedule
		if err := json.Unmarshal(data, &s); err != nil {
			continue // corrupt: skipped, not fatal - one bad record must not hide the rest
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created.Equal(out[j].Created) {
			return out[i].ID < out[j].ID
		}
		return out[i].Created.Before(out[j].Created)
	})
	return out, nil
}

// Delete removes one task's record. Deleting what is not there is the outcome the
// caller asked for, so it is not an error.
func (st *Store) Delete(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	err := os.Remove(st.Path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

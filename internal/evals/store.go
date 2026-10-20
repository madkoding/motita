package evals

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// keepRuns is how many runs the store remembers; older ones are dropped.
const keepRuns = 20

// Store keeps the runs, newest last, in one JSON file. With no path it keeps them in memory only.
type Store struct {
	mu   sync.Mutex
	path string
	runs []Run
}

// OpenStore reads the runs saved at path. A missing or unreadable file starts empty: results are a
// convenience, never a reason for the gateway not to start.
func OpenStore(path string) *Store {
	s := &Store{path: path}
	if path == "" {
		return s
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &s.runs)
	}
	return s
}

// Runs are the remembered runs, oldest first.
func (s *Store) Runs() []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Run(nil), s.runs...)
}

// Last is the newest run, nil before the first.
func (s *Store) Last() *Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runs) == 0 {
		return nil
	}
	last := s.runs[len(s.runs)-1]
	return &last
}

// Add remembers a run and writes the file. The run is kept in memory even when the write fails.
func (s *Store) Add(r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, r)
	if len(s.runs) > keepRuns {
		s.runs = s.runs[len(s.runs)-keepRuns:]
	}
	if s.path == "" {
		return nil
	}
	raw, _ := json.Marshal(s.runs)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ErrNoAnswer is returned when no task could run at all: the model did not answer, so nothing
// about it was learned and nothing is stored.
var ErrNoAnswer = errors.New("the model did not answer any task")

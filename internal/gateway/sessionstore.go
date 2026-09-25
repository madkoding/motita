package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

// sessionStore persists conversations to disk so they survive a restart.
//
// Each conversation is one JSON file under a directory the caller chooses
// (usually ~/.motita/sessions). The store is the ONE place that reads and
// writes those files, so the conversation struct does not have to know about
// the filesystem: it hands the store its transcript and title, and the store
// puts them where a later process will find them.
//
// The store is safe for concurrent use: a mutex guards the directory, and the
// conversation's own lock guards the fields the store reads. The store never
// holds the conversation lock while doing I/O — it snapshots under the lock
// and writes the snapshot after releasing it.
type sessionStore struct {
	mu  sync.Mutex
	dir string
}

// sessionRecord is the JSON shape of one persisted conversation.
type sessionRecord struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	ProjectID string    `json:"project_id,omitempty"`
	Created   time.Time `json:"created"`
	LastUsed  time.Time `json:"last_used"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
	// Running, when true, means the session was mid-run when the gateway
	// shut down (e.g. for an upgrade). The new process reads this and
	// re-submits LastTask as a task or plan to resume the work.
	Running  bool                 `json:"running,omitempty"`
	LastTask string               `json:"last_task,omitempty"`
	LastKind string               `json:"last_kind,omitempty"`
	Turns    []agent.DialogueTurn `json:"turns"`
}

// newSessionStore creates a store rooted at dir. The directory is created if
// it does not exist; a failure to create it is a failure to return a store,
// because a store whose directory is missing would silently drop every save.
func newSessionStore(dir string) (*sessionStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("the session store needs a directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create the session directory %q: %w", dir, err)
	}
	return &sessionStore{dir: dir}, nil
}

// path returns the filename for one conversation.
func (st *sessionStore) path(id string) string {
	return filepath.Join(st.dir, id+".json")
}

// save writes one conversation to disk. It snapshots the fields under the
// conversation's own lock and then writes, so the lock is never held during
// I/O.
func (st *sessionStore) save(c *conversation) error {
	c.stateMu.Lock()
	rec := sessionRecord{
		ID:        c.id,
		Title:     c.title,
		ProjectID: c.projectID,
		Created:   c.created,
		LastUsed:  c.lastUsed,
		Running:   c.running,
		LastTask:  c.lastTask,
		LastKind:  c.lastKind,
	}
	c.stateMu.Unlock()

	// The transcript and config come from the service, which has its own
	// concurrency protection.
	if c.svc != nil {
		rec.Turns = c.svc.Transcript()
		cfg := c.svc.Config()
		rec.Provider = cfg.LLM.Provider
		rec.Model = cfg.LLM.Model
		rec.Workspace = cfg.Agent.WorkspaceDir
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode the session %q: %w", c.id, err)
	}
	tmp := st.path(c.id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("could not write the session %q: %w", c.id, err)
	}
	if err := os.Rename(tmp, st.path(c.id)); err != nil {
		return fmt.Errorf("could not save the session %q: %w", c.id, err)
	}
	return nil
}

// loadAll reads every persisted conversation from disk, sorted by last-used
// descending so the most recent session is first.
func (st *sessionStore) loadAll() ([]sessionRecord, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	entries, err := os.ReadDir(st.dir)
	if err != nil {
		return nil, fmt.Errorf("could not read the session directory %q: %w", st.dir, err)
	}
	var out []sessionRecord
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(st.dir, e.Name()))
		if err != nil {
			continue // a file that vanished or is unreadable is skipped, not fatal
		}
		var rec sessionRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue // a corrupt file is skipped, not fatal
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	return out, nil
}

// delete removes one conversation's file from disk.
func (st *sessionStore) delete(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	err := os.Remove(st.path(id))
	if os.IsNotExist(err) {
		return nil // already gone: the outcome the caller asked for
	}
	return err
}

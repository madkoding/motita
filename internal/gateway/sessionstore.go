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
	// ProjectDir is the project's OWN checkout, which differs from Workspace
	// for a session that has its own worktree. It is persisted because the
	// restore path needs it to re-create that worktree and to know what the
	// session's branch should be compared against; without it a restarted
	// gateway would report every session as having nothing to integrate.
	ProjectDir string `json:"project_dir,omitempty"`
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
func (st *sessionStore) save(c *conversation) error { return st.write(c, false) }

// saveEnded is save for a run that has ENDED: the record says the session is idle even
// though the run's slot is still held for the few instructions until it is released. Holding
// the slot to the end keeps every write inside the window waitForNoRun and the delete path
// rely on; recording the truth is what keeps the next start from running the task again.
func (st *sessionStore) saveEnded(c *conversation) error { return st.write(c, true) }

func (st *sessionStore) write(c *conversation, ended bool) error {
	c.stateMu.Lock()
	rec := sessionRecord{
		ID:         c.id,
		Title:      c.title,
		ProjectID:  c.projectID,
		Created:    c.created,
		LastUsed:   c.lastUsed,
		Running:    c.running && !ended,
		LastTask:   c.lastTask,
		LastKind:   c.lastKind,
		ProjectDir: c.projectDir,
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
	if err := writeSynced(tmp, data); err != nil {
		return fmt.Errorf("could not write the session %q: %w", c.id, err)
	}
	// The version being replaced is kept as <id>.json.prev: a save that is cut half way, or a
	// bad write, costs one turn instead of the whole conversation. A missing file has nothing
	// to keep, and any other failure to keep it must not stop the save itself.
	if info, err := os.Stat(st.path(c.id)); err == nil && info.Mode().IsRegular() {
		_ = os.Rename(st.path(c.id), st.path(c.id)+prevSuffix)
	}
	if err := os.Rename(tmp, st.path(c.id)); err != nil {
		return fmt.Errorf("could not save the session %q: %w", c.id, err)
	}
	return nil
}

// prevSuffix names the copy of the previous version that save keeps beside each session.
const prevSuffix = ".prev"

// trashDir is where a deleted session's file goes. Deleting a session forgets it; it must not
// destroy the only copy of a conversation the user may want back.
const trashDir = "trash"

// writeSynced writes a file and flushes it to the disk before returning. A rename over the
// old file is atomic, but only the CONTENT of the new one is durable once it is synced: a
// power cut between the rename and the flush leaves an empty file where the session was.
func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// readRecord reads one session file.
func readRecord(path string) (sessionRecord, error) {
	var rec sessionRecord
	data, err := os.ReadFile(path)
	if err != nil {
		return rec, err
	}
	err = json.Unmarshal(data, &rec)
	return rec, err
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
		path := filepath.Join(st.dir, e.Name())
		rec, err := readRecord(path)
		if err != nil {
			// A file that cannot be read is NOT skipped and forgotten: the session would
			// vanish from the list while its file sat there, and the next save would
			// overwrite the only evidence. It is set aside, and the previous version -
			// kept by every save - takes its place when there is one.
			if _, statErr := os.Stat(path); statErr == nil {
				_ = os.Rename(path, fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix()))
			}
			if rec, err = readRecord(path + prevSuffix); err != nil {
				continue
			}
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

	dest := filepath.Join(st.dir, trashDir)
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	err := os.Rename(st.path(id), filepath.Join(dest, fmt.Sprintf("%s-%d.json", id, time.Now().Unix())))
	if os.IsNotExist(err) {
		return nil // already gone: the outcome the caller asked for
	}
	// The kept previous version goes with it: left behind, loadAll would bring the session
	// back from it.
	_ = os.Remove(st.path(id) + prevSuffix)
	return err
}

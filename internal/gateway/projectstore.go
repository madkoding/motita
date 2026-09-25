package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Project is a named group of sessions that share a workspace directory.
//
// A session that belongs to a project runs with its workspace set to the
// project's directory, so the agent works inside that folder and nowhere
// else. The directory is either a subfolder of the workspace chosen when the
// project was created, or a git repository cloned from a URL.
type Project struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Dir         string    `json:"dir"`
	GitURL      string    `json:"git_url,omitempty"`
	Created     time.Time `json:"created"`
}

// projectStore persists projects to disk as JSON files under a directory the
// caller chooses (usually ~/.motita/projects).
type projectStore struct {
	mu  sync.Mutex
	dir string
}

// newProjectStore creates a store rooted at dir.
func newProjectStore(dir string) (*projectStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("the project store needs a directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create the project directory %q: %w", dir, err)
	}
	return &projectStore{dir: dir}, nil
}

func (ps *projectStore) path(id string) string {
	return filepath.Join(ps.dir, id+".json")
}

// save writes one project to disk.
func (ps *projectStore) save(p Project) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode the project %q: %w", p.ID, err)
	}
	tmp := ps.path(p.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("could not write the project %q: %w", p.ID, err)
	}
	if err := os.Rename(tmp, ps.path(p.ID)); err != nil {
		return fmt.Errorf("could not save the project %q: %w", p.ID, err)
	}
	return nil
}

// load reads one project from disk. Returns nil, nil when the file does not exist.
func (ps *projectStore) load(id string) (*Project, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	data, err := os.ReadFile(ps.path(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the project %q: %w", id, err)
	}
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("could not parse the project %q: %w", id, err)
	}
	return &p, nil
}

// loadAll reads every project from disk, sorted by creation time.
func (ps *projectStore) loadAll() ([]Project, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	entries, err := os.ReadDir(ps.dir)
	if err != nil {
		return nil, fmt.Errorf("could not read the project directory %q: %w", ps.dir, err)
	}
	var out []Project
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ps.dir, e.Name()))
		if err != nil {
			continue
		}
		var p Project
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}

// delete removes one project's file from disk.
func (ps *projectStore) delete(id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	err := os.Remove(ps.path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// cloneGitRepo clones a git URL into the given directory and returns the
// combined output of the git command. It is called when a project is created
// with a git URL instead of a local folder.
func cloneGitRepo(gitURL, destDir string) (string, error) {
	if strings.TrimSpace(gitURL) == "" {
		return "", fmt.Errorf("the git URL is empty")
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return "", fmt.Errorf("could not create the parent directory: %w", err)
	}
	cmd := exec.Command("git", "clone", "--progress", gitURL, destDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("could not clone %q: %w\n%s", gitURL, err, string(out))
	}
	return string(out), nil
}

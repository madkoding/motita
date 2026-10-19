package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// A repository is its own kind of thing, apart from the projects that use it and the sessions that
// work in it: ONE git repository, with the settings of how its pull requests are made. A project is
// a plain folder that links to the repositories it works on.

// Repository is one git repository the gateway knows. Dir is its clone: the catalog's, a folder of
// the user's, or the root of a project that was made as a repository before this kind existed.
type Repository struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	GitURL string `json:"git_url,omitempty"`
	Dir    string `json:"dir"`
	// Local marks a repository the user already had on this machine: never fetched into, merged
	// into or deleted.
	Local bool `json:"local,omitempty"`
	// MainBranch is the branch the user chose as the main line of work of this repository.
	MainBranch string `json:"main_branch,omitempty"`
	// MergeMethod, PRMaxFixes, AutoMerge and AutoContinue are how a pull request of this
	// repository is merged and followed: see the fields of the same name in Project.
	MergeMethod  string    `json:"merge_method,omitempty"`
	PRMaxFixes   int       `json:"pr_max_fixes,omitempty"`
	AutoMerge    bool      `json:"auto_merge,omitempty"`
	AutoContinue bool      `json:"auto_continue,omitempty"`
	Created      time.Time `json:"created"`
}

// repoStoreFolder is the folder, inside the project store's, that holds the repositories. The
// project store reads only the files of its own folder, so the two never meet.
const repoStoreFolder = "repositories"

// errRepoGone is update's answer for a repository that was deleted while the caller worked.
var errRepoGone = errors.New("the repository no longer exists")

// repoStore persists repositories to disk as one JSON file each.
type repoStore struct {
	mu  sync.Mutex
	dir string
}

// newRepoStore creates a store rooted at dir.
func newRepoStore(dir string) (*repoStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create the repository directory %q: %w", dir, err)
	}
	return &repoStore{dir: dir}, nil
}

// file is the path of repository id: an id that is not a plain name is refused.
func (rs *repoStore) file(id string) (string, error) {
	if !validProjectID(id) {
		return "", fmt.Errorf("%q is not a valid repository id", id)
	}
	return filepath.Join(rs.dir, id+".json"), nil
}

// save writes one repository to disk.
func (rs *repoStore) save(r Repository) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.saveLocked(r)
}

func (rs *repoStore) saveLocked(r Repository) error {
	file, err := rs.file(r.ID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode the repository %q: %w", r.ID, err)
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("could not write the repository %q: %w", r.ID, err)
	}
	if err := os.Rename(tmp, file); err != nil {
		return fmt.Errorf("could not save the repository %q: %w", r.ID, err)
	}
	return nil
}

// load reads one repository. Returns nil, nil when it does not exist.
func (rs *repoStore) load(id string) (*Repository, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	file, err := rs.file(id)
	if err != nil {
		return nil, err
	}
	return readRepoFile(file)
}

func readRepoFile(file string) (*Repository, error) {
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the repository file %q: %w", file, err)
	}
	var r Repository
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("could not parse the repository file %q: %w", file, err)
	}
	return &r, nil
}

// update loads the repository, applies change to that fresh copy and saves it, under the lock.
func (rs *repoStore) update(id string, change func(*Repository)) (*Repository, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	file, err := rs.file(id)
	if err != nil {
		return nil, err
	}
	r, err := readRepoFile(file)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errRepoGone
	}
	change(r)
	if err := rs.saveLocked(*r); err != nil {
		return nil, err
	}
	return r, nil
}

// loadAll reads every repository, sorted by creation time.
func (rs *repoStore) loadAll() ([]Repository, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	entries, err := os.ReadDir(rs.dir)
	if err != nil {
		return nil, fmt.Errorf("could not read the repository directory %q: %w", rs.dir, err)
	}
	var out []Repository
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if r, err := readRepoFile(filepath.Join(rs.dir, e.Name())); err == nil && r != nil {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}

// delete removes one repository's file.
func (rs *repoStore) delete(id string) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	file, err := rs.file(id)
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

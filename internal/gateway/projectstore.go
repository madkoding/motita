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
// A session that belongs to a project runs in its OWN worktree of that
// directory - see sessionWorktree - so the agent works inside a copy of the
// folder and nowhere else. The directory is either a subfolder of the workspace
// chosen when the project was created, or a git repository cloned from a URL.
type Project struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Dir         string `json:"dir"`
	GitURL      string `json:"git_url,omitempty"`
	// Podman is the user's answer, given when the project was created, to running
	// it with podman. It is only ever true on a machine that had podman then.
	Podman bool `json:"podman,omitempty"`
	// Branch is the branch the project's checkout is on RIGHT NOW (read live).
	Branch string `json:"branch,omitempty"`
	// MainBranch is the branch the project always goes back to when it has no sessions, and the
	// one the user chose as its main line of work. It is saved with the project; a project from
	// before the setting existed answers with the main/master branch it has (read live).
	MainBranch string `json:"main_branch,omitempty"`
	// MergeMethod is how a pull request of this project is merged from motita: "merge", "squash"
	// or "rebase". Empty is a merge commit.
	MergeMethod string `json:"merge_method,omitempty"`
	// PRMaxFixes is how many times the agent is sent to fix a failing CI of this project's pull
	// requests before the gateway gives up; zero is the gateway's default.
	PRMaxFixes int `json:"pr_max_fixes,omitempty"`
	// AutoMerge merges a pull request of this project by itself when its CI passes and the host
	// accepts the merge. Off, merging is always the user's click.
	AutoMerge bool `json:"auto_merge,omitempty"`
	// AutoContinue opens a fresh session from the updated branch by itself when a pull request of
	// this project is merged, so the next piece of work has somewhere to start.
	AutoContinue bool `json:"auto_continue,omitempty"`
	// Changes is how many uncommitted changes are in the project's own
	// checkout. It is the project's own number, NOT the sum over its sessions:
	// a session works in its own worktree, and adding the two would report work
	// the user cannot see in this directory. Absent for a non-repository.
	Changes int `json:"changes,omitempty"`
	// Sessions counts the sessions this project has. It is what makes a
	// project header say how much is running under it without the front end
	// having to group the session list itself.
	Sessions int `json:"sessions,omitempty"`
	// Worktrees counts the session worktrees this project has on disk: one per
	// session that ran in its own checkout. It is read from git rather than
	// from the session list, so a worktree left behind by a session that is
	// gone is still counted - which is exactly the state a user wants to know
	// about.
	Worktrees int       `json:"worktrees,omitempty"`
	Created   time.Time `json:"created"`
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

// sameOrigin reports whether dir is a git checkout whose origin is gitURL, ignoring a
// trailing ".git" or slash and the case of the host.
func sameOrigin(dir, gitURL string, env []string) bool {
	cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	norm := func(u string) string {
		u = strings.TrimSpace(u)
		u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
		return strings.ToLower(u)
	}
	return norm(string(out)) == norm(gitURL)
}

// freeCloneDir returns dir when a clone of gitURL can go there (it is missing, empty, or already
// that repository), and otherwise the first "<dir>-N" that can.
func freeCloneDir(dir, gitURL string, env []string) string {
	usable := func(d string) bool {
		entries, err := os.ReadDir(d)
		return err != nil || len(entries) == 0 || sameOrigin(d, gitURL, env)
	}
	if usable(dir) {
		return dir
	}
	for i := 2; ; i++ {
		if cand := fmt.Sprintf("%s-%d", dir, i); usable(cand) {
			return cand
		}
	}
}

// cloneableURL reports whether gitURL names a repository the way a person gives one: an
// http(s) or ssh URL, the scp form user@host:path, or an absolute local path. Everything else
// is refused before git sees it - a leading "-" is a flag to git, and the "transport::address"
// form (ext::, fd::) asks git to run a command.
func cloneableURL(gitURL string) bool {
	switch {
	case strings.HasPrefix(gitURL, "-"), strings.Contains(gitURL, "::"):
		return false
	case strings.HasPrefix(gitURL, "https://"), strings.HasPrefix(gitURL, "http://"), strings.HasPrefix(gitURL, "ssh://"):
		return true
	case filepath.IsAbs(gitURL):
		return true
	}
	// scp form: a host (with an optional user) before the first ':', and no '/' in it.
	host, path, ok := strings.Cut(gitURL, ":")
	return ok && host != "" && path != "" && !strings.ContainsAny(host, "/\\ ")
}

// cloneGitRepo clones a git URL into the given directory and returns the
// combined output of the git command. It is called when a project is created
// with a git URL instead of a local folder.
func cloneGitRepo(gitURL, destDir string, env []string) (string, error) {
	if strings.TrimSpace(gitURL) == "" {
		return "", fmt.Errorf("the git URL is empty")
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return "", fmt.Errorf("could not create the parent directory: %w", err)
	}
	// A folder left by an earlier attempt (a project that was deleted, a clone that was
	// interrupted after the checkout) holding this same repository is the project's folder
	// already: git refuses to clone into it, so it is reused instead of failing.
	if entries, err := os.ReadDir(destDir); err == nil && len(entries) > 0 {
		if sameOrigin(destDir, gitURL, env) {
			return "reusing the existing clone in " + destDir, nil
		}
		return "", fmt.Errorf("the folder %q already exists and is not a clone of %q: pick another folder name or remove it", destDir, gitURL)
	}
	if !cloneableURL(gitURL) {
		return "", fmt.Errorf("%q is not a repository URL: use https://, ssh://, user@host:path or an absolute path", gitURL)
	}
	// "--" ends the options: the URL and the folder are never read as flags.
	cmd := exec.Command("git", "clone", "--progress", "--", gitURL, destDir)
	// env carries the credential helper, so a repository of a host the user connected clones
	// without a prompt nobody can answer. Nil keeps the process environment.
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("could not clone %q: %w\n%s", gitURL, err, string(out))
	}
	return string(out), nil
}

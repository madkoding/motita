package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// A project is a folder. When the folder is itself a git repository it has the git tools; when it
// is not, it is a plain folder, and the repositories INSIDE it (a checkout per subfolder, as in a
// workspace of several services) have their own. They are found by looking, never recorded: a
// repository cloned or deleted in the folder shows up or goes at the next read.

// nestedRepoDepth is how many folders down a repository is looked for: `<project>/<repo>` and
// `<project>/<group>/<repo>`. Deeper than that is a vendored tree, not a checkout of the user's.
const nestedRepoDepth = 2

// nestedSkipped are the folders never searched: what a build leaves, which can hold thousands of
// folders and a copy of other people's repositories.
var nestedSkipped = map[string]bool{"node_modules": true, "vendor": true}

// NestedRepo is a git repository found inside a project's folder.
type NestedRepo struct {
	// Path is the repository's folder relative to the project's, with forward slashes.
	Path       string `json:"path"`
	Branch     string `json:"branch,omitempty"`
	MainBranch string `json:"main_branch,omitempty"`
	Changes    int    `json:"changes,omitempty"`
}

// nestedReposOf lists the repositories inside the project's folder, with what each says right now.
// A project whose own folder is a repository has none to list: it is one repository, and what it
// contains is its own business.
func nestedReposOf(ctx context.Context, p *Project) []NestedRepo {
	if p.Dir == "" || projectIsRepo(ctx, p.Dir) {
		return nil
	}
	var found []NestedRepo
	for _, rel := range reposUnder(p.Dir, "", nestedRepoDepth) {
		dir := filepath.Join(p.Dir, filepath.FromSlash(rel))
		r := NestedRepo{Path: rel, Branch: gitx.Display(ctx, dir)}
		r.MainBranch = mainBranchOf(ctx, &Project{Dir: dir})
		r.Changes, _ = gitx.WorkingTreeChanges(ctx, dir)
		found = append(found, r)
	}
	return found
}

// reposUnder returns the folders below root/rel that hold a `.git`, as slash paths relative to
// root, looking `depth` folders down. A repository is not searched further, and a folder that
// cannot be read has none.
func reposUnder(root, rel string, depth int) []string {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || nestedSkipped[name] {
			continue
		}
		child := name
		if rel != "" {
			child = rel + "/" + name
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(child), ".git")); err == nil {
			found = append(found, child)
			continue
		}
		if depth > 1 {
			found = append(found, reposUnder(root, child, depth-1)...)
		}
	}
	return found
}

// errNoNestedRepo answers a request that names a folder the project does not hold a repository at.
const errNoNestedRepo = "the project has no repository at that path"

// nestedRepoAt is the repository of the project at rel and its folder, only when the project's
// folder really holds one there: a path in a request is never trusted to name a folder.
func nestedRepoAt(ctx context.Context, p *Project, rel string) (NestedRepo, string, bool) {
	for _, n := range nestedReposOf(ctx, p) {
		if n.Path == rel {
			return n, filepath.Join(p.Dir, filepath.FromSlash(n.Path)), true
		}
	}
	return NestedRepo{}, "", false
}

// refreshNested is the "update now" of one repository inside a project: its main branch brought up
// to date with origin, nothing forced. The path must be one the folder really holds, so a request
// cannot name a folder elsewhere. It answers the notice that remains, "" once it worked.
func (s *Server) refreshNested(w http.ResponseWriter, r *http.Request, p *Project, rel string) {
	r0, dir, ok := nestedRepoAt(r.Context(), p, rel)
	if !ok {
		writeError(w, http.StatusNotFound, errNoNestedRepo)
		return
	}
	main := r0.MainBranch
	key := p.ID + "\x00" + rel
	if !s.mainRefresh.begin(key) {
		writeError(w, http.StatusConflict, "the main branch is already being updated")
		return
	}
	s.mainRefresh.wg.Add(1)
	defer s.mainRefresh.wg.Done()
	ctx, cancel := context.WithTimeout(r.Context(), mainRefreshTimeout)
	defer cancel()
	warning := ""
	if err := gitx.RefreshBranch(ctx, dir, main); err != nil {
		warning = main + ": " + err.Error()
	}
	s.mainRefresh.finish(key, "")
	writeJSON(w, http.StatusOK, map[string]string{"main_warning": warning})
}

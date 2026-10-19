package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// Taking the main repository out of the project's folder.

// renamePath and initRepo are what moving the main repository does to the disk; variables so a
// test can make either fail.
var (
	renamePath = os.Rename
	initRepo   = gitx.InitOn
)

// mainRepoFolder is the folder name the project's own repository takes when it becomes one of the
// others: the last element of its URL, or of its folder. "" when neither makes a usable name.
func (p *Project) mainRepoFolder() string {
	if name := repoNameOf("", p.GitURL); name != "" {
		return name
	}
	return repoNameOf(filepath.Base(p.Dir), "")
}

// freeRepoFolder is name when the project has no repository or folder by it, name with "-main"
// after it when that is free, and "" when neither is (or name is empty).
func (p *Project) freeRepoFolder(name string) string {
	taken := func(n string) bool { return p.repoIndex(n) >= 0 || pathExists(filepath.Join(p.Dir, n)) }
	switch {
	case name == "":
		return ""
	case !taken(name):
		return name
	case !taken(name + "-main"):
		return name + "-main"
	default:
		return ""
	}
}

// handleDetachProjectMain takes the project's own repository out of the project's folder, into a
// folder of its own, so the project becomes a plain folder that holds its repositories, all alike.
// Nothing is deleted: the repository, its history and its uncommitted work move as they are.
//
// It is refused while a session is running, and while a session has its own copy of the main
// repository: that copy belongs to the repository that is moving, so the session has to be merged
// or deleted first.
func (s *Server) handleDetachProjectMain(w http.ResponseWriter, r *http.Request) {
	defer s.lockProjectRepos(r.PathValue("id"))()
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	ctx := r.Context()
	if !s.detachAllowed(w, ctx, p) {
		return
	}
	name := p.freeRepoFolder(p.mainRepoFolder())
	if name == "" {
		writeError(w, http.StatusConflict, "the project has no free folder name for its main repository")
		return
	}
	mainBranch := mainBranchOf(ctx, p)
	if err := moveMainRepo(p, name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mainBranch == "" {
		mainBranch = defaultMainBranch
	}
	if err := initRepo(ctx, p.Dir, mainBranch); err != nil {
		undoMoveMainRepo(p, name)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	moved := ProjectRepo{Name: name, GitURL: p.GitURL, Dir: filepath.Join(p.Dir, name), MainBranch: mainBranch}
	_ = gitx.EnsureIgnoreBase(ctx, p.Dir)
	updated, err := s.updateProject(p.ID, func(cur *Project) {
		cur.Repos = append([]ProjectRepo{moved}, cur.Repos...)
		cur.GitURL = ""
		cur.MainBranch = mainBranch
	})
	if err != nil {
		// The project does not declare the move, so the disk must not show it either.
		undoMoveMainRepo(p, name)
	}
	if errors.Is(err, errProjectGone) {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p = updated
	for _, c := range s.sessionsOfProject(p.ID) {
		applyRuntimeTo(c.svc, p, c.workspaceDir())
	}
	writeJSON(w, http.StatusOK, p)
}

// detachAllowed answers 409, and returns false, while moving the main repository would pull the
// ground from under a session.
func (s *Server) detachAllowed(w http.ResponseWriter, ctx context.Context, p *Project) bool {
	for _, c := range s.sessionsOfProject(p.ID) {
		if c.isRunning() {
			writeError(w, http.StatusConflict, "a session of the project is running: wait for it to finish")
			return false
		}
	}
	all, err := gitx.Worktrees(ctx, p.Dir)
	if err != nil {
		writeError(w, http.StatusConflict, "the project's folder is not a repository, so there is no main "+
			"repository to move: "+err.Error())
		return false
	}
	for _, wt := range all {
		if !wt.Prunable && !gitx.SamePath(wt.Path, p.Dir) {
			writeError(w, http.StatusConflict, "a session has its own copy of the main repository: "+
				"merge or delete the sessions of the project first")
			return false
		}
	}
	return true
}

// pathExists reports whether anything is at path.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// moveMainRepo moves everything in the project's folder into its subfolder name, except the other
// repositories, which stay where they are. A move that fails puts back what it had moved.
func moveMainRepo(p *Project, name string) error {
	stay := map[string]bool{name: true}
	for _, repo := range p.Repos {
		stay[strings.SplitN(filepath.ToSlash(repo.relPath(p.Dir)), "/", 2)[0]] = true
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return err
	}
	dest := filepath.Join(p.Dir, name)
	if err := os.Mkdir(dest, 0o755); err != nil {
		return err
	}
	var moved []string
	for _, e := range entries {
		if stay[e.Name()] {
			continue
		}
		if err := renamePath(filepath.Join(p.Dir, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			for _, m := range moved {
				_ = renamePath(filepath.Join(dest, m), filepath.Join(p.Dir, m))
			}
			_ = os.Remove(dest)
			return err
		}
		moved = append(moved, e.Name())
	}
	return nil
}

// undoMoveMainRepo puts back what moveMainRepo moved into the subfolder name, after a step that
// came later failed: the repository this folder was initialised as is dropped, and the original
// (which is in the subfolder) takes its place. Nothing is touched unless the original is there.
func undoMoveMainRepo(p *Project, name string) {
	dest := filepath.Join(p.Dir, name)
	if !pathExists(filepath.Join(dest, ".git")) {
		return
	}
	_ = os.RemoveAll(filepath.Join(p.Dir, ".git"))
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		_ = renamePath(filepath.Join(dest, e.Name()), filepath.Join(p.Dir, e.Name()))
	}
	_ = os.Remove(dest)
}

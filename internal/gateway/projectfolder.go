package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/madkoding/motita/internal/gitx"
)

// wipesFolder reports whether the deletion asks to remove the project's folder with its contents
// (`?folder=delete`). Without it the folder stays on disk, wherever it is: removing files is a
// choice the user makes in the dialog, never a side effect of forgetting a project.
func wipesFolder(r *http.Request) bool {
	return r.URL.Query().Get("folder") == "delete"
}

// protectedFolder says why p's folder may never be removed, empty when it may: the disk's root, the
// home folder, a system tree, the workspace (or a folder around it) and motita's own folders are
// refused whatever the client asks, and so is a folder another project also stands on.
func (s *Server) protectedFolder(p *Project) string {
	if p == nil || p.Dir == "" {
		return ""
	}
	if problem := s.projectFolderProblem(p.Dir); problem != "" {
		return problem
	}
	if systemFolder(p.Dir) {
		return "that folder is among the system's own files"
	}
	if s.projectInDir(p.Dir, p.ID) != nil {
		return "another project also stands on that folder"
	}
	return ""
}

// folderChanges are the uncommitted changes the project's own folder holds, which removing it
// throws away. A folder that is not a repository, or cannot be read, has none to list; the
// confirmation of the deletion covers it.
func (s *Server) folderChanges(ctx context.Context, p *Project) []gitx.Change {
	if p == nil || p.Dir == "" || gitx.Repo(ctx, p.Dir) != nil {
		return []gitx.Change{}
	}
	changes, _ := worktreeInspectList(ctx, p.Dir)
	if changes == nil {
		return []gitx.Change{}
	}
	return changes
}

// wipeProblem is the refusal of a deletion that removes the folder: a protected folder is always
// refused (400), and uncommitted work in it stops the deletion until the user confirmed the
// discard with `?force=1` (409). The status is 0 when the folder may go.
func (s *Server) wipeProblem(ctx context.Context, p *Project, forced bool) (int, string) {
	if problem := s.protectedFolder(p); problem != "" {
		return http.StatusBadRequest, "the project's folder cannot be deleted: " + problem
	}
	if !forced && len(s.folderChanges(ctx, p)) > 0 {
		return http.StatusConflict, "the project's folder has uncommitted changes: " +
			"commit them, or confirm the discard (" + p.Dir + ")"
	}
	return 0, ""
}

// removeProjectFolder removes the project's folder with what it holds. Best effort: the project is
// already gone, and a folder that resists (a file in use) is left for the user rather than failing a
// deletion that has otherwise happened.
func removeProjectFolder(dir string) {
	_ = os.RemoveAll(filepath.Clean(dir))
}

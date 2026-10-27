package gateway

import (
	"net/http"
	"os"
	"path/filepath"
)

// cloneIntoNewProject clones gitURL into the folder of the project just made, as one of its
// repositories. A project is a plain folder, so the clone sits inside it like any repository added
// later. When it fails nothing of the project is left: its record goes, and its folder with it if
// this request made it. It answers the request itself, and returns false, on failure.
func (s *Server) cloneIntoNewProject(
	w http.ResponseWriter, r *http.Request, p Project, gitURL, name string, madeFolder bool,
) (Project, string, bool) {
	repo, err := s.placeRepo(r.Context(), name, gitURL, filepath.Join(p.Dir, name))
	if err != nil {
		_ = s.projects.delete(p.ID)
		if madeFolder {
			_ = os.RemoveAll(p.Dir)
		}
		s.writeCloneError(w, gitURL, err)
		return p, "", false
	}
	updated, err := s.updateProject(p.ID, func(cur *Project) { cur.Repos = append(cur.Repos, repo) })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return p, "", false
	}
	return *updated, "cloned " + gitURL + " into " + name, true
}

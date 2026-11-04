package gateway

import (
	"net/http"

	"github.com/madkoding/motita/internal/gitx"
)

// handleReturnProjectMain is the project menu's "Return to main branch": the project's folder is
// checked out on its main branch now, by the user's word, and the branch brought up to date. It
// is for the folder a pull request left on another branch with uncommitted changes, which nothing
// moves by itself (see settleAfterPR): git carries the changes over when they do not conflict and
// refuses otherwise, and the refusal is the answer (409, with git's reason). 200 with the branch
// the folder is on and the notice the update left (""), 404 for an unknown project, 409 for a
// folder that is not a repository or has no main branch.
func (s *Server) handleReturnProjectMain(w http.ResponseWriter, r *http.Request) {
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	ctx := r.Context()
	main := startBranchOf(ctx, p)
	if main == "" || !projectIsRepo(ctx, p.Dir) {
		writeError(w, http.StatusConflict, "this project has no main branch to return to")
		return
	}
	if gitx.Display(ctx, p.Dir) != main {
		if err := gitx.Checkout(ctx, p.Dir, main); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	warning := ""
	if err := gitx.RefreshBranch(ctx, p.Dir, main); err != nil {
		warning = main + ": " + err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]string{"branch": main, "main_warning": warning})
}

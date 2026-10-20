package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// The git tools of one repository inside a project's folder (see projectnested.go): what it holds
// right now, and the actions a person does by hand. Committing, pushing and opening a pull request
// are not here: they need the git logins and a message worth reading, so they go to the agent.

// nestedLogLimit is how many commits the history shows.
const nestedLogLimit = 15

// nestedActionTimeout bounds one action: they all talk to a local repository, and the ones that
// reach origin (pull) are given the same time a refresh gets.
const nestedActionTimeout = mainRefreshTimeout

type nestedCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

func nestedCommits(list []gitx.Commit) []nestedCommit {
	out := make([]nestedCommit, 0, len(list))
	for _, c := range list {
		out = append(out, nestedCommit{SHA: c.SHA, Subject: c.Subject})
	}
	return out
}

// nestedStatus is what the repository's panel shows.
type nestedStatus struct {
	Path        string         `json:"path"`
	Branch      string         `json:"branch"`
	MainBranch  string         `json:"main_branch,omitempty"`
	Changes     []gitx.Change  `json:"changes"`
	Log         []nestedCommit `json:"log"`
	Ahead       int            `json:"ahead"`
	Behind      int            `json:"behind"`
	Unpublished int            `json:"unpublished"`
	Merged      []string       `json:"merged"`
}

func (s *Server) nestedRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/projects/{id}/repo", plain(s.handleNestedStatus))
	mux.Handle("POST /v1/projects/{id}/repo/{action}", plain(s.handleNestedAction))
}

// nestedTarget finds the project and the repository a request names, answering the error itself.
func (s *Server) nestedTarget(w http.ResponseWriter, r *http.Request) (NestedRepo, string, bool) {
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return NestedRepo{}, "", false
	}
	n, dir, ok := nestedRepoAt(r.Context(), p, r.URL.Query().Get("repo"))
	if !ok {
		writeError(w, http.StatusNotFound, errNoNestedRepo)
	}
	return n, dir, ok
}

func (s *Server) handleNestedStatus(w http.ResponseWriter, r *http.Request) {
	n, dir, ok := s.nestedTarget(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	st := nestedStatus{Path: n.Path, Branch: n.Branch, MainBranch: n.MainBranch}
	st.Changes, _ = gitx.WorkingTreeChangeList(ctx, dir)
	history, _ := gitx.Log(ctx, dir, nestedLogLimit)
	st.Log = nestedCommits(history)
	st.Unpublished, _ = gitx.UnpublishedCommits(ctx, dir)
	st.Merged = []string{}
	if n.MainBranch != "" {
		if n.Branch != n.MainBranch {
			ahead, behind, _ := gitx.CommitsBetween(ctx, dir, n.MainBranch, n.Branch)
			st.Ahead, st.Behind = len(ahead), len(behind)
		}
		if merged, err := gitx.MergedBranches(ctx, dir, n.MainBranch); err == nil {
			st.Merged = append(st.Merged, merged...)
		}
	}
	writeJSON(w, http.StatusOK, st)
}

// nestedActions are the things done to a repository, by name. Each answers what the web shows.
var nestedActions = map[string]func(ctx context.Context, n NestedRepo, dir string, body nestedBody) (map[string]any, int, error){
	"pull":    nestedPull,
	"main":    nestedMain,
	"branch":  nestedBranch,
	"stash":   nestedStash,
	"clean":   nestedClean,
	"discard": nestedDiscard,
}

type nestedBody struct {
	Branch  string `json:"branch"`
	Confirm bool   `json:"confirm"`
}

func (s *Server) handleNestedAction(w http.ResponseWriter, r *http.Request) {
	do, known := nestedActions[r.PathValue("action")]
	if !known {
		writeError(w, http.StatusNotFound, "there is no such action")
		return
	}
	n, dir, ok := s.nestedTarget(w, r)
	if !ok {
		return
	}
	var body nestedBody
	if r.ContentLength > 0 && !s.decodeBody(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), nestedActionTimeout)
	defer cancel()
	done, status, err := do(ctx, n, dir, body)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, done)
}

// nestedPull brings the branch the repository is on up to date with origin: a fast-forward of a
// clean checkout, never a merge.
func nestedPull(ctx context.Context, n NestedRepo, dir string, _ nestedBody) (map[string]any, int, error) {
	if err := gitx.RefreshBranch(ctx, dir, gitx.Display(ctx, dir)); err != nil {
		return nil, http.StatusConflict, err
	}
	return map[string]any{}, 0, nil
}

// nestedMain goes back to the main branch, only from a clean checkout: git would carry the changes
// along, and a person asking to "go back" does not expect them to follow.
func nestedMain(ctx context.Context, n NestedRepo, dir string, _ nestedBody) (map[string]any, int, error) {
	if n.MainBranch == "" {
		return nil, http.StatusConflict, errors.New("this repository has no main branch to go back to")
	}
	if dirty, _ := gitx.Dirty(ctx, dir); dirty {
		return nil, http.StatusConflict, gitx.ErrBranchDirty
	}
	if err := gitx.Checkout(ctx, dir, n.MainBranch); err != nil {
		return nil, http.StatusConflict, err
	}
	return map[string]any{}, 0, nil
}

// nestedBranch starts a branch at the checkout's work, carrying what is uncommitted along.
func nestedBranch(ctx context.Context, _ NestedRepo, dir string, body nestedBody) (map[string]any, int, error) {
	if !gitx.ValidBranch(ctx, body.Branch) {
		return nil, http.StatusBadRequest, errors.New("that is not a name git accepts for a branch")
	}
	if err := gitx.SwitchNew(ctx, dir, body.Branch); err != nil {
		return nil, http.StatusConflict, err
	}
	return map[string]any{}, 0, nil
}

// nestedStash puts the uncommitted work aside under a label that says when it was.
func nestedStash(ctx context.Context, n NestedRepo, dir string, _ nestedBody) (map[string]any, int, error) {
	label := "motita: " + n.Path + " " + time.Now().Format("2006-01-02 15:04")
	if err := gitx.Stash(ctx, dir, label); err != nil {
		return nil, http.StatusConflict, err
	}
	return map[string]any{}, 0, nil
}

// nestedClean deletes the branches whose work is already in the main branch. `-d` and not `-D`:
// whatever git still finds unmerged stays.
func nestedClean(ctx context.Context, n NestedRepo, dir string, _ nestedBody) (map[string]any, int, error) {
	if n.MainBranch == "" {
		return nil, http.StatusConflict, errors.New("this repository has no main branch to compare with")
	}
	// A list that cannot be read deletes nothing.
	merged, _ := gitx.MergedBranches(ctx, dir, n.MainBranch)
	deleted := []string{}
	for _, name := range merged {
		if gitx.DeleteBranch(ctx, dir, name) == nil {
			deleted = append(deleted, name)
		}
	}
	return map[string]any{"deleted": deleted}, 0, nil
}

// nestedDiscard destroys the uncommitted work, so it runs only when the request says the person
// confirmed it: the web asks first and sends the answer.
func nestedDiscard(ctx context.Context, _ NestedRepo, dir string, body nestedBody) (map[string]any, int, error) {
	if !body.Confirm {
		return nil, http.StatusBadRequest, errors.New("discarding changes needs the person's confirmation")
	}
	if err := gitx.DiscardChanges(ctx, dir); err != nil {
		return nil, http.StatusConflict, err
	}
	return map[string]any{}, 0, nil
}

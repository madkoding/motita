package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/schedule"
)

// The pull request endpoints are the session's way to propose its work and to
// follow the CI of that proposal. Opening the pull request is a run of the agent
// (it writes the semantic title and the description, as it does for a merge);
// reading the pull request and its CI is mechanical, so the web interface can ask
// for it as often as it likes without spending a turn.

// Machine-readable reasons a pull request cannot be read, so the client can tell
// "connect your account" from "this repository has nowhere to open one".
const (
	errPRNoRemote = "pr_no_remote"
	errPRNoHost   = "pr_no_host"
)

// prView is the answer of GET /v1/sessions/{id}/pr.
type prView struct {
	// State is "none" while the branch has no open pull request and "open" once it has.
	State  string                `json:"state"`
	Branch string                `json:"branch"`
	PR     *gitforge.PullRequest `json:"pr,omitempty"`
	CI     *gitforge.CIStatus    `json:"ci,omitempty"`
	// Merge says whether the host would accept a merge now; it is asked once the CI has passed.
	Merge *gitforge.MergeState `json:"merge,omitempty"`
	// Watch is what the gateway is doing about the pull request, absent when nothing.
	Watch *PRWatchView `json:"watch,omitempty"`
}

// prTarget is the repository a session's pull request lives in.
type prTarget struct {
	api    gitforge.API
	remote gitforge.Remote
	branch string
}

// prError is a refusal with the HTTP status and code the client acts on.
type prError struct {
	status  int
	code    string
	service string
	msg     string
}

func (e *prError) Error() string { return e.msg }

func (s *Server) writePRError(w http.ResponseWriter, e *prError) {
	body := map[string]string{"error": e.msg}
	if e.code != "" {
		body["code"] = e.code
	}
	if e.service != "" {
		body["service"] = e.service
	}
	writeJSON(w, e.status, body)
}

// prTargetOf resolves the host, the repository and the branch of a session.
func (s *Server) prTargetOf(ctx context.Context, c *conversation) (prTarget, *prError) {
	if c.workspace == "" || c.projectID == "" {
		return prTarget{}, &prError{status: http.StatusConflict, msg: "this session does not belong to a project, so there is no branch to propose"}
	}
	st := s.gitStore()
	if st == nil {
		return prTarget{}, &prError{status: http.StatusNotImplemented, msg: "this gateway has no directory to keep git logins in"}
	}
	cmd := exec.CommandContext(ctx, "git", "-C", c.workspace, "remote", "get-url", "origin")
	cmd.Env = s.gitCommandEnv()
	out, err := cmd.Output()
	url := strings.TrimSpace(string(out))
	if err != nil || url == "" {
		return prTarget{}, &prError{status: http.StatusConflict, code: errPRNoRemote, msg: "this repository has no remote named origin, so there is nowhere to open a pull request"}
	}
	svc, remote, err := st.ForRemote(url)
	if err != nil {
		if errors.Is(err, gitforge.ErrNoService) {
			return prTarget{}, &prError{status: http.StatusConflict, code: errPRNoHost, msg: remote.Host + " is not a git host motita can open pull requests on"}
		}
		return prTarget{}, &prError{status: http.StatusBadGateway, msg: err.Error()}
	}
	api, err := st.Open(ctx, svc)
	if gitforge.IsNotConnected(err) {
		return prTarget{}, &prError{status: http.StatusConflict, code: errGitAuthRequired, service: svc.ID, msg: svc.Name + " is not connected"}
	}
	if err != nil {
		return prTarget{}, &prError{status: http.StatusInternalServerError, msg: err.Error()}
	}
	return prTarget{api: api, remote: remote, branch: sessionBranch(c.id)}, nil
}

// prHostError maps a failed call to the host onto the answer the client expects.
func prHostError(svc gitforge.Service, err error) *prError {
	var he *gitforge.HTTPError
	if errors.As(err, &he) && he.Unauthorized() {
		return &prError{status: http.StatusConflict, code: errGitAuthRequired, service: svc.ID, msg: svc.Name + " refused the saved login: connect again"}
	}
	return &prError{status: http.StatusBadGateway, msg: err.Error()}
}

// handleGetPR answers whether the session's branch has an open pull request and,
// if so, the state of its CI.
func (s *Server) handleGetPR(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	t, perr := s.prTargetOf(r.Context(), c)
	if perr != nil {
		s.writePRError(w, perr)
		return
	}
	view := prView{State: "none", Branch: t.branch}
	pr, found, err := t.api.FindPR(r.Context(), t.remote, t.branch)
	if err != nil {
		s.writePRError(w, prHostError(t.api.Service, err))
		return
	}
	if found {
		view.State, view.PR = "open", &pr
		ci, err := t.api.CI(r.Context(), t.remote, pr.Number)
		if err != nil {
			s.writePRError(w, prHostError(t.api.Service, err))
			return
		}
		view.CI = &ci
		if ci.State == gitforge.StateSuccess {
			if m, err := t.api.MergeState(r.Context(), t.remote, pr.Number); err == nil {
				view.Merge = &m
			}
		}
		// A CI seen running is a CI to follow, whoever pushed: the loop does not wait to be asked.
		if ci.State == gitforge.StatePending {
			s.startPRWatch(c, nil)
		}
	}
	view.Watch = c.prWatchView()
	writeJSON(w, http.StatusOK, view)
}

// handleCreatePR starts an agent turn that commits the session's work, pushes
// its branch and opens the pull request, and streams it like /task.
func (s *Server) handleCreatePR(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	if c.merged {
		writeError(w, http.StatusConflict, "this session has already been integrated into the project")
		return
	}
	t, perr := s.prTargetOf(r.Context(), c)
	if perr != nil {
		s.writePRError(w, perr)
		return
	}
	// A pull request already open is the answer, not a second one.
	if pr, found, err := t.api.FindPR(r.Context(), t.remote, t.branch); err == nil && found {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "this session already has an open pull request", "code": "pr_exists", "pr": pr})
		return
	}
	if p := s.projectOf(c.projectID); p != nil {
		base := gitx.Display(r.Context(), p.Dir)
		ahead, _, _ := gitx.CommitsBetween(r.Context(), p.Dir, base, t.branch)
		changes, _ := gitx.WorkingTreeChanges(r.Context(), c.workspace)
		if len(ahead) == 0 && changes == 0 {
			writeError(w, http.StatusConflict, "this session has no work to propose: its branch has no commits the project's branch lacks and its worktree has no uncommitted changes")
			return
		}
	}
	task := fmt.Sprintf(`Open a pull request for this session's work.

Follow the pull-requests-and-ci skill. Commit any pending changes on the session branch with a semantic message, push the branch with "git push -u origin %[1]s", and open the pull request with "motita forge pr create" (a semantic title and a description with What, Why and How it was verified).

Session worktree: %[2]s
Session branch: %[1]s

Then give the user the link to the pull request. Do not wait for the CI and do not merge: the gateway follows the CI and tells the user when it ends.`, t.branch, c.workspace)
	// From here the gateway follows the pull request's CI, whether or not anyone is looking.
	s.startPRWatch(c, nil)
	s.startRunWithIntent(w, r, c, task, schedule.KindTask, "")
}

// ciFixTask is what the agent is told when the CI of the session's pull request
// fails.
func ciFixTask(branch string, pr gitforge.PullRequest) string {
	return fmt.Sprintf(`The CI of the pull request %[2]s (#%[3]d) failed. Fix it.

Follow the pull-requests-and-ci skill: run "motita forge pr checks --logs" to read the failing jobs, reproduce the failure locally with the project's own check before changing anything, fix the cause in code this pull request touches, commit with a semantic message and push to %[1]s. Never skip, disable or delete a test to get green. If the failure is not caused by this change, say so with the evidence instead of widening the pull request.

Do not merge the pull request. After pushing, stop: the gateway watches the new run of the CI and sends you back here if it fails again.`, branch, pr.URL, pr.Number)
}

// handleFixPR sends the agent to fix the CI of the pull request now and follows it again from
// the start: it is what "Try again" does after the gateway gave up.
func (s *Server) handleFixPR(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	if c.merged {
		writeError(w, http.StatusConflict, "this session has already been integrated into the project")
		return
	}
	t, perr := s.prTargetOf(r.Context(), c)
	if perr != nil {
		s.writePRError(w, perr)
		return
	}
	pr, found, err := t.api.FindPR(r.Context(), t.remote, t.branch)
	if err != nil {
		s.writePRError(w, prHostError(t.api.Service, err))
		return
	}
	if !found {
		writeError(w, http.StatusConflict, "this session has no open pull request whose CI could be fixed")
		return
	}
	c.setPRWatch(nil)
	s.startPRWatch(c, nil)
	started, pos := s.sendToAgent(c, ciFixTask(t.branch, pr))
	if started {
		writeJSON(w, http.StatusAccepted, map[string]any{"queued": false, "started": true})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "started": false, "position": pos})
}

// syncProject brings the project's own checkout up to date with what was just merged, when that
// is safe: the checkout is on the branch that was merged into and has nothing uncommitted. It is
// best effort, because the merge has happened either way; the next session pulls again.
func (s *Server) syncProject(ctx context.Context, c *conversation, base string) {
	p := s.projectOf(c.projectID)
	if p == nil {
		return
	}
	if base == "" {
		base = p.MainBranch
	}
	if base == "" || gitx.Display(ctx, p.Dir) != base {
		return
	}
	if n, err := gitx.WorkingTreeChanges(ctx, p.Dir); err != nil || n > 0 {
		return
	}
	for _, args := range [][]string{{"fetch", "origin", base}, {"merge", "--ff-only", "origin/" + base}} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", p.Dir}, args...)...)
		cmd.Env = s.gitCommandEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			if s.opts.Log != nil {
				s.opts.Log.Warn("could not bring the project up to date after a merge", "project", p.Dir, "step", args[0], "error", strings.TrimSpace(string(out)))
			}
			return
		}
	}
}

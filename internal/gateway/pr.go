package gateway

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

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
	// Repos lists the repositories with work to propose, each with its pull request, when the
	// session's folder has work in two or more; absent otherwise.
	Repos []prRepoView `json:"repos,omitempty"`
}

// prTarget is the repository a session's pull request lives in.
type prTarget struct {
	api    gitforge.API
	remote gitforge.Remote
	branch string
	// dir is the repository the pull request is about, inside the session's folder, and base the
	// branch it proposes into.
	dir  string
	base string
}

// prError is a refusal with the HTTP status and code the client acts on.
type prError struct {
	status  int
	code    string
	service string
	msg     string
}

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

// repoRemote is the repository a session's pull request goes to. A repository the project recorded
// (its clone URL) answers with that URL: the checkout's configuration is something the agent can
// rewrite (`git remote set-url`), and the pull request is opened, watched and merged with the
// person's own login, so it never lands on a repository the session pointed at. A repository the
// project knows nothing about (one cloned by hand into the folder) answers with its origin.
func (s *Server) repoRemote(ctx context.Context, c *conversation, dir string) string {
	if p := s.projectOf(c.projectID); p != nil {
		// The session's own worktree of a repository stands where the project's folder has it, inside
		// the session's folder: both are the same repository.
		if (gitx.SamePath(dir, p.Dir) || gitx.SamePath(dir, c.workspace)) && strings.TrimSpace(p.GitURL) != "" {
			return strings.TrimSpace(p.GitURL)
		}
		for _, repo := range p.Repos {
			if (gitx.SamePath(dir, p.repoPath(repo)) || gitx.SamePath(dir, sessionRepoPath(c.workspace, p, repo))) &&
				strings.TrimSpace(repo.GitURL) != "" {
				return strings.TrimSpace(repo.GitURL)
			}
		}
	}
	return s.originOf(ctx, dir)
}

// originOf is the URL of the remote named origin of the repository at dir, or "" when it has none.
func (s *Server) originOf(ctx context.Context, dir string) string {
	gitArgs := append([]string{"-C", dir}, gitx.HardeningFor(ctx, dir)...)
	cmd := exec.CommandContext(ctx, "git", append(gitArgs, "remote", "get-url", "origin")...)
	cmd.Env = s.gitCommandEnv()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// prTargetOf resolves the host, the repository and the branch of a session. The repository is one
// of those in the session's folder (see sessionRepoDir) and the branch the one it is on, so the
// pull request is about the work as it stands, wherever the agent put it.
func (s *Server) prTargetOf(ctx context.Context, c *conversation, repo string) (prTarget, *prError) {
	if c.workspace == "" || c.projectID == "" {
		return prTarget{}, &prError{status: http.StatusConflict,
			msg: "this session does not belong to a project, so there is no branch to propose"}
	}
	st := s.gitStore()
	if st == nil {
		return prTarget{}, &prError{status: http.StatusNotImplemented,
			msg: "this gateway has no directory to keep git logins in"}
	}
	dir := c.sessionRepoDir(ctx, repo)
	if dir == "" {
		return prTarget{}, &prError{status: http.StatusConflict, code: errPRNoRemote,
			msg: "there is no git repository in this project's folder, so there is nothing to propose"}
	}
	return s.prTargetAt(ctx, c, dir)
}

// prTargetAt is prTargetOf for the repository at dir.
func (s *Server) prTargetAt(ctx context.Context, c *conversation, dir string) (prTarget, *prError) {
	st := s.gitStore()
	url := s.repoRemote(ctx, c, dir)
	if url == "" {
		return prTarget{}, &prError{status: http.StatusConflict, code: errPRNoRemote,
			msg: "this repository has no remote named origin, so there is nowhere to open a pull request"}
	}
	svc, remote, err := st.ForRemote(url)
	if err != nil {
		if errors.Is(err, gitforge.ErrNoService) {
			return prTarget{}, &prError{status: http.StatusConflict, code: errPRNoHost,
				msg: remote.Host + " is not a git host motita can open pull requests on"}
		}
		return prTarget{}, &prError{status: http.StatusBadGateway, msg: err.Error()}
	}
	api, err := st.Open(ctx, svc)
	if gitforge.IsNotConnected(err) {
		return prTarget{}, &prError{status: http.StatusConflict, code: errGitAuthRequired, service: svc.ID,
			msg: svc.Name + " is not connected"}
	}
	if err != nil {
		return prTarget{}, &prError{status: http.StatusInternalServerError, msg: err.Error()}
	}
	api.Cache = &s.etags
	return prTarget{api: api, remote: remote, dir: dir, base: gitx.DefaultBranch(ctx, dir),
		branch: c.proposalBranch(ctx, dir)}, nil
}

// prHostError maps a failed call to the host onto the answer the client expects.
func prHostError(svc gitforge.Service, err error) *prError {
	var he *gitforge.HTTPError
	if errors.As(err, &he) && he.Unauthorized() {
		return &prError{status: http.StatusConflict, code: errGitAuthRequired, service: svc.ID,
			msg: svc.Name + " refused the saved login: connect again"}
	}
	return &prError{status: http.StatusBadGateway, msg: err.Error()}
}

// handleGetPR answers whether the session's branch has an open pull request and,
// if so, the state of its CI.
func (s *Server) handleGetPR(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	t, perr := s.prTargetOf(r.Context(), c, c.knownRepoName(r.Context(), r.URL.Query().Get("repo")))
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
		ci = s.awaitedCI(r.Context(), c, t, pr, ci)
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
	view.Repos = s.prRepoViews(r.Context(), c)
	writeJSON(w, http.StatusOK, view)
}

// handleCreatePR starts an agent turn that commits the session's work, pushes
// its branch and opens the pull request, and streams it like /task.
func (s *Server) handleCreatePR(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	if c.isMerged() {
		writeError(w, http.StatusConflict, "this session has already been integrated into the project")
		return
	}
	// The body is optional: it names the repository when the project folder holds several.
	var body struct {
		Repo string `json:"repo"`
	}
	if r.ContentLength > 0 && !s.decodeBody(w, r, &body) {
		return
	}
	body.Repo = c.knownRepoName(r.Context(), cmp.Or(body.Repo, r.URL.Query().Get("repo")))
	t, perr := s.prTargetOf(r.Context(), c, body.Repo)
	if perr != nil {
		s.writePRError(w, perr)
		return
	}
	// A pull request already open is the answer, not a second one. When no repository was named and
	// the project holds several, the next one with work and no pull request of its own comes first.
	if pr, found, err := t.api.FindPR(r.Context(), t.remote, t.branch); err == nil && found {
		next, ok := s.nextRepoWithoutPR(r.Context(), c, body.Repo, t.dir)
		if !ok {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "this session already has an open pull request", "code": "pr_exists", "pr": pr,
			})
			return
		}
		t = next
	}
	ahead := 0
	if t.base != "" && gitx.BranchExists(r.Context(), t.dir, t.branch) {
		commits, _, _ := gitx.CommitsBetween(r.Context(), t.dir, t.base, t.branch)
		ahead = len(commits)
	}
	changes, _ := gitx.WorkingTreeChanges(r.Context(), t.dir)
	if ahead == 0 && changes == 0 {
		writeError(w, http.StatusConflict, "this repository has no work to propose: its branch has no commits "+
			"the base branch lacks and it has no uncommitted changes")
		return
	}
	// The work goes on a branch of its own: a repository left on its default branch would push there.
	branch, err := c.ensureProposalBranch(r.Context(), t.dir)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	task := fmt.Sprintf(`Open a pull request for this session's work.

Follow the pull-requests-and-ci skill. Commit any pending changes on the current branch with a semantic message, `+
		`push the branch with "git push -u origin %[1]s", and open the pull request with "motita forge pr create" `+
		`(a semantic title and a description with What, Why and How it was verified).

Repository: %[2]s
Branch (already checked out): %[1]s
Base branch: %[3]s

Then give the user the link to the pull request. Do not wait for the CI and do not merge: `+
		`the gateway follows the CI and tells the user when it ends.`, branch, t.dir, t.base)
	// From here the gateway follows the pull request's CI, whether or not anyone is looking.
	s.startPRWatch(c, nil)
	s.startRunWithIntent(w, r, c, task, schedule.KindTask, "")
}

// nextRepoWithoutPR finds another repository of the session's folder that has work to propose and
// no open pull request, after the one at current. Only when the caller named none: a named
// repository is the answer as it is.
func (s *Server) nextRepoWithoutPR(ctx context.Context, c *conversation, named, current string) (prTarget, bool) {
	if strings.TrimSpace(named) != "" {
		return prTarget{}, false
	}
	for _, dir := range c.sessionRepoDirs(ctx, repoDirsIn(ctx, c.workspace)) {
		if dir == current || !repoHasWork(ctx, dir) {
			continue
		}
		t, perr := s.prTargetAt(ctx, c, dir)
		if perr != nil {
			continue
		}
		if _, found, err := t.api.FindPR(ctx, t.remote, t.branch); err == nil && !found {
			return t, true
		}
	}
	return prTarget{}, false
}

// prRepoView is one repository of the session's folder that has work to propose, with the state of
// its pull request.
type prRepoView struct {
	// Name is what a request names the repository by ("." for the session's folder itself).
	Name  string                `json:"name"`
	State string                `json:"state"`
	PR    *gitforge.PullRequest `json:"pr,omitempty"`
	CI    string                `json:"ci,omitempty"`
}

// prRepoViews lists the repositories of the session's folder with work to propose and where each
// one's pull request stands. nil unless there are two or more: with one the bar needs nothing but
// the view itself.
func (s *Server) prRepoViews(ctx context.Context, c *conversation) []prRepoView {
	var dirs []string
	for _, dir := range c.sessionRepoDirs(ctx, repoDirsIn(ctx, c.workspace)) {
		if repoHasWork(ctx, dir) {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) < 2 {
		return nil
	}
	var out []prRepoView
	for _, dir := range dirs {
		v := prRepoView{Name: c.repoName(dir), State: "none"}
		if t, perr := s.prTargetAt(ctx, c, dir); perr == nil {
			if pr, found, err := t.api.FindPR(ctx, t.remote, t.branch); err == nil && found {
				v.State, v.PR = "open", &pr
				ci, _ := t.api.CI(ctx, t.remote, pr.Number)
				v.CI = ci.State
			}
		}
		out = append(out, v)
	}
	return out
}

// ciFixTask is what the agent is told when the CI of the session's pull request
// fails.
func ciFixTask(branch string, pr gitforge.PullRequest) string {
	return fmt.Sprintf(`The CI of the pull request %[2]s (#%[3]d) failed. Fix it.

Follow the pull-requests-and-ci skill: run "motita forge pr checks --logs" to read the failing jobs, `+
		`reproduce the failure locally with the project's own check before changing anything, `+
		`fix the cause in code this pull request touches, commit with a semantic message and push to %[1]s. `+
		`Never skip, disable or delete a test to get green. If the failure is not caused by this change, `+
		`say so with the evidence instead of widening the pull request.

Do not merge the pull request. After pushing, stop: the gateway watches the new run of the CI `+
		`and sends you back here if it fails again.`, branch, pr.URL, pr.Number)
}

// reviewTask is what the agent is told when somebody comments on or reviews the session's pull
// request: the comments themselves, so it does not have to go and read them.
func reviewTask(branch string, pr gitforge.PullRequest, news []gitforge.Comment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "New comments or reviews arrived on the pull request %s (#%d):\n", pr.URL, pr.Number)
	for _, c := range news {
		where := c.Kind
		if c.State != "" {
			where += " " + c.State
		}
		if c.Path != "" {
			where += " on " + c.Path
		}
		if c.Author != "" {
			where += " by @" + c.Author
		}
		fmt.Fprintf(&b, "\n- [%s] %s\n", where, strings.ReplaceAll(c.Body, "\n", "\n  "))
	}
	fmt.Fprintf(&b, `
Answer them in code: make the changes they ask for that are right for this pull request, `+
		`with a test where behaviour changes, run the project's own check, commit with a semantic message `+
		`and push to %s. If a comment asks for nothing, or for something wrong, change nothing for it and `+
		`say why in your answer. Do not post comments on the pull request and do not merge it. After `+
		`pushing, stop: the gateway watches the pull request and sends you back if anything else needs you.`, branch)
	return b.String()
}

// conflictTask is what the agent is told when the session's branch stops merging cleanly into its base.
func conflictTask(branch string, pr gitforge.PullRequest) string {
	base := pr.Base
	if base == "" {
		base = "its base branch"
	}
	return fmt.Sprintf(`The pull request %[2]s (#%[3]d) conflicts with %[4]s.

Fetch origin, merge %[4]s into %[1]s and resolve the conflicts keeping the intent of both sides, `+
		`run the project's own check, commit the merge and push to %[1]s. Do not rebase or force-push, `+
		`and do not merge the pull request. After pushing, stop: the gateway watches the pull request.`,
		branch, pr.URL, pr.Number, base)
}

// handleFixPR sends the agent to fix the CI of the pull request now and follows it again from
// the start: it is what "Try again" does after the gateway gave up.
func (s *Server) handleFixPR(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	if c.isMerged() {
		writeError(w, http.StatusConflict, "this session has already been integrated into the project")
		return
	}
	t, perr := s.prTargetOf(r.Context(), c, "")
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

// syncTimeout bounds the fetch that follows a merge: it is a courtesy, and must not hold anything.
const syncTimeout = 2 * time.Minute

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
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	for _, args := range [][]string{{"fetch", "origin", base}, {"merge", "--ff-only", "origin/" + base}} {
		gitArgs := append(append([]string{"-C", p.Dir}, gitx.HardeningFor(ctx, p.Dir)...), args...)
		cmd := exec.CommandContext(ctx, "git", gitArgs...)
		cmd.Env = s.gitCommandEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			if s.opts.Log != nil {
				s.opts.Log.Warn("could not bring the project up to date after a merge",
					"project", p.Dir, "step", args[0], "error", strings.TrimSpace(string(out)))
			}
			return
		}
	}
}

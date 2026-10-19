package gitforge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// CI states. A pull request with no checks at all is "none": that is not a
// pass, and a caller waiting for CI must not wait for ever for something that
// was never going to run.
const (
	StatePending = "pending"
	StateSuccess = "success"
	StateFailure = "failure"
	StateNone    = "none"
)

// Check is one CI job as a host reports it.
type Check struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	URL    string `json:"url,omitempty"`
	Detail string `json:"detail,omitempty"`
	// jobID is the GitHub Actions job id behind a check run, which is what its log
	// is fetched by. Zero for everything else.
	jobID int64
}

// CIStatus is the CI of one pull request, as a whole and job by job.
type CIStatus struct {
	State  string  `json:"state"`
	Checks []Check `json:"checks"`
	// Rev names what the checks ran on: the commit on GitHub and Gitea, the
	// pipeline on GitLab. A caller reacting to a failure uses it to react once
	// per push and not once per poll. Empty when the host does not say.
	Rev string `json:"rev,omitempty"`
}

// summarize folds the jobs into one state: any failure fails the whole, and
// anything still running keeps it pending.
func summarize(checks []Check) CIStatus {
	st := CIStatus{State: StateNone, Checks: checks}
	if len(checks) == 0 {
		return st
	}
	st.State = StateSuccess
	for _, c := range checks {
		switch c.State {
		case StateFailure:
			st.State = StateFailure
			return st
		case StatePending:
			st.State = StatePending
		default:
			// A success leaves the summary as it is.
		}
	}
	return st
}

// Awaiting is c with every check of base that c has not started counted as running. A host lists
// a commit's jobs as they start, so for a moment after a push it reports some of them, or none, and
// a summary of those few would read as a pass before the CI has run.
func (c CIStatus) Awaiting(base CIStatus) CIStatus {
	checks := slices.Clone(c.Checks)
	for _, b := range base.Checks {
		if !slices.ContainsFunc(checks, func(h Check) bool { return h.Name == b.Name }) {
			checks = append(checks, Check{Name: b.Name, State: StatePending})
		}
	}
	st := summarize(checks)
	st.Rev = c.Rev
	return st
}

// CI reads the checks of pull request number n.
func (a API) CI(ctx context.Context, r Remote, n int) (CIStatus, error) {
	switch a.Service.Kind {
	case KindGitLab:
		return a.ciGitLab(ctx, r, n)
	case KindBitbucket:
		return a.ciBitbucket(ctx, r, n)
	default:
		return a.ciGitHubLike(ctx, r, n)
	}
}

// BranchCI reads the checks of the latest commit of a branch: what the CI says about the code a
// pull request would be merged into. Bitbucket reports checks per pull request only, and answers
// "none".
func (a API) BranchCI(ctx context.Context, r Remote, branch string) (CIStatus, error) {
	switch a.Service.Kind {
	case KindGitLab:
		return a.gitLabBranchCI(ctx, r, branch)
	case KindBitbucket:
		return summarize(nil), nil
	default:
		return a.ciGitHubLikeAt(ctx, r, url.PathEscape(branch))
	}
}

// gitLabBranchCI reads the jobs of the latest pipeline of a branch on GitLab.
func (a API) gitLabBranchCI(ctx context.Context, r Remote, branch string) (CIStatus, error) {
	var pipelines []struct {
		ID int64 `json:"id"`
	}
	q := url.Values{"ref": {branch}, "per_page": {"1"}}
	if err := a.do(ctx, http.MethodGet, a.gitLabProject(r)+"/pipelines?"+q.Encode(), nil, &pipelines); err != nil {
		return CIStatus{}, err
	}
	if len(pipelines) == 0 {
		return summarize(nil), nil
	}
	return a.gitLabJobs(ctx, url.PathEscape(r.Path), pipelines[0].ID)
}

// FailedNames are the names of the checks that failed, sorted as the host gave them.
func (c CIStatus) FailedNames() []string {
	var out []string
	for _, ch := range c.Checks {
		if ch.State == StateFailure {
			out = append(out, ch.Name)
		}
	}
	return out
}

func (a API) headSHA(ctx context.Context, r Remote, n int) (string, error) {
	var pr struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := a.do(ctx, http.MethodGet, a.pullsPath(r, n), nil, &pr); err != nil {
		return "", err
	}
	if pr.Head.SHA == "" {
		return "", errors.New("the pull request names no commit")
	}
	return pr.Head.SHA, nil
}

func (a API) ciGitHubLike(ctx context.Context, r Remote, n int) (CIStatus, error) {
	sha, err := a.headSHA(ctx, r, n)
	if err != nil {
		return CIStatus{}, err
	}
	return a.ciGitHubLikeAt(ctx, r, sha)
}

// ciGitHubLikeAt reads the checks of a commit; the hosts also accept a branch name where a commit goes.
func (a API) ciGitHubLikeAt(ctx context.Context, r Remote, sha string) (CIStatus, error) {
	var checks []Check
	if a.Service.Kind == KindGitHub {
		runs, err := a.gitHubCheckRuns(ctx, r, sha)
		if err != nil {
			return CIStatus{}, err
		}
		checks = runs
	}
	var statuses struct {
		Statuses []struct {
			Context     string `json:"context"`
			State       string `json:"state"`
			Status      string `json:"status"`
			TargetURL   string `json:"target_url"`
			Description string `json:"description"`
		} `json:"statuses"`
	}
	endpoint := fmt.Sprintf("%s/repos/%s/commits/%s/status", a.Service.APIBase, r.Path, sha)
	if err := a.do(ctx, http.MethodGet, endpoint, nil, &statuses); err != nil {
		return CIStatus{}, err
	}
	for _, s := range statuses.Statuses {
		state := s.State
		if state == "" {
			state = s.Status
		}
		checks = append(checks, Check{Name: s.Context, State: mapState(state), URL: s.TargetURL, Detail: s.Description})
	}
	st := summarize(checks)
	st.Rev = sha
	return st, nil
}

// gitHubCheckRuns reads the GitHub check runs of a commit.
func (a API) gitHubCheckRuns(ctx context.Context, r Remote, sha string) ([]Check, error) {
	var runs struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
			Output     struct {
				Title   string `json:"title"`
				Summary string `json:"summary"`
			} `json:"output"`
		} `json:"check_runs"`
	}
	endpoint := fmt.Sprintf("%s/repos/%s/commits/%s/check-runs?per_page=100", a.Service.APIBase, r.Path, sha)
	if err := a.do(ctx, http.MethodGet, endpoint, nil, &runs); err != nil {
		return nil, err
	}
	var checks []Check
	for _, c := range runs.CheckRuns {
		detail := strings.TrimSpace(c.Output.Title)
		if detail == "" {
			detail = strings.TrimSpace(c.Output.Summary)
		}
		checks = append(checks, Check{Name: c.Name, State: checkRunState(c.Status, c.Conclusion), URL: c.HTMLURL,
			Detail: firstLineOf(detail), jobID: c.ID})
	}
	return checks, nil
}

// checkRunState folds a GitHub check run's status and conclusion into one state: pending until it
// completes, then a success or a failure.
func checkRunState(status, conclusion string) string {
	if status != "completed" {
		return StatePending
	}
	switch conclusion {
	case "success", "neutral", "skipped":
		return StateSuccess
	default:
		return StateFailure
	}
}

func (a API) ciGitLab(ctx context.Context, r Remote, n int) (CIStatus, error) {
	project := url.PathEscape(r.Path)
	var pipelines []struct {
		ID int64 `json:"id"`
	}
	endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/pipelines", a.Service.APIBase, project, n)
	if err := a.do(ctx, http.MethodGet, endpoint, nil, &pipelines); err != nil {
		return CIStatus{}, err
	}
	if len(pipelines) == 0 {
		return summarize(nil), nil
	}
	return a.gitLabJobs(ctx, project, pipelines[0].ID)
}

// gitLabJobs is the CI of one pipeline, job by job.
func (a API) gitLabJobs(ctx context.Context, project string, pipeline int64) (CIStatus, error) {
	var jobs []struct {
		Name          string `json:"name"`
		Status        string `json:"status"`
		WebURL        string `json:"web_url"`
		FailureReason string `json:"failure_reason"`
		AllowFailure  bool   `json:"allow_failure"`
	}
	endpoint := fmt.Sprintf("%s/projects/%s/pipelines/%d/jobs?per_page=100", a.Service.APIBase, project, pipeline)
	if err := a.do(ctx, http.MethodGet, endpoint, nil, &jobs); err != nil {
		return CIStatus{}, err
	}
	checks := make([]Check, 0, len(jobs))
	for _, j := range jobs {
		state := mapState(j.Status)
		if state == StateFailure && j.AllowFailure {
			state = StateSuccess
		}
		checks = append(checks, Check{Name: j.Name, State: state, URL: j.WebURL, Detail: j.FailureReason})
	}
	st := summarize(checks)
	st.Rev = strconv.FormatInt(pipeline, 10)
	return st, nil
}

func (a API) ciBitbucket(ctx context.Context, r Remote, n int) (CIStatus, error) {
	var out struct {
		Values []struct {
			Name        string `json:"name"`
			State       string `json:"state"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"values"`
	}
	if err := a.do(ctx, http.MethodGet, a.bitbucketPRPath(r, n)+"/statuses", nil, &out); err != nil {
		return CIStatus{}, err
	}
	checks := make([]Check, 0, len(out.Values))
	for _, v := range out.Values {
		checks = append(checks, Check{Name: v.Name, State: mapState(v.State), URL: v.URL, Detail: v.Description})
	}
	return summarize(checks), nil
}

// mapState folds the vocabularies of the four hosts into three states.
func mapState(s string) string {
	switch strings.ToLower(s) {
	case "success", "successful", "passed", "skipped", "manual", "neutral":
		return StateSuccess
	case "failure", "failed", "error", "canceled", "cancelled", "stopped", "timed_out":
		return StateFailure
	default:
		return StatePending
	}
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// logTail is how much of a failed job's log is returned: the end, where the
// error is.
const logTail = 12 << 10

// FailureLog fetches the end of the log of a failed check. Only GitHub Actions
// serves a log through the API with the same token; for the others the check's
// URL is the way to it, and ok is false.
func (a API) FailureLog(ctx context.Context, r Remote, c Check) (log string, ok bool, err error) {
	if a.Service.Kind != KindGitHub || c.jobID == 0 {
		return "", false, nil
	}
	endpoint := a.Service.APIBase + "/repos/" + r.Path + "/actions/jobs/" + strconv.FormatInt(c.jobID, 10) + "/logs"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("User-Agent", "motita")
	hc := a.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, &HTTPError{Status: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", false, err
	}
	if len(data) > logTail {
		data = data[len(data)-logTail:]
	}
	return string(data), true, nil
}

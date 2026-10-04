package gitforge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		}
	}
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

func (a API) headSHA(ctx context.Context, r Remote, n int) (string, error) {
	var pr struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := a.do(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/pulls/%d", a.Service.APIBase, r.Path, n), nil, &pr); err != nil {
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
	var checks []Check
	if a.Service.Kind == KindGitHub {
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
		if err := a.do(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/commits/%s/check-runs?per_page=100", a.Service.APIBase, r.Path, sha), nil, &runs); err != nil {
			return CIStatus{}, err
		}
		for _, c := range runs.CheckRuns {
			state := StatePending
			if c.Status == "completed" {
				switch c.Conclusion {
				case "success", "neutral", "skipped":
					state = StateSuccess
				default:
					state = StateFailure
				}
			}
			detail := strings.TrimSpace(c.Output.Title)
			if detail == "" {
				detail = strings.TrimSpace(c.Output.Summary)
			}
			checks = append(checks, Check{Name: c.Name, State: state, URL: c.HTMLURL, Detail: firstLineOf(detail), jobID: c.ID})
		}
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
	if err := a.do(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/commits/%s/status", a.Service.APIBase, r.Path, sha), nil, &statuses); err != nil {
		return CIStatus{}, err
	}
	for _, s := range statuses.Statuses {
		state := s.State
		if state == "" {
			state = s.Status
		}
		checks = append(checks, Check{Name: s.Context, State: mapState(state), URL: s.TargetURL, Detail: s.Description})
	}
	return summarize(checks), nil
}

func (a API) ciGitLab(ctx context.Context, r Remote, n int) (CIStatus, error) {
	project := url.PathEscape(r.Path)
	var pipelines []struct {
		ID int64 `json:"id"`
	}
	if err := a.do(ctx, http.MethodGet, fmt.Sprintf("%s/projects/%s/merge_requests/%d/pipelines", a.Service.APIBase, project, n), nil, &pipelines); err != nil {
		return CIStatus{}, err
	}
	if len(pipelines) == 0 {
		return summarize(nil), nil
	}
	var jobs []struct {
		Name          string `json:"name"`
		Status        string `json:"status"`
		WebURL        string `json:"web_url"`
		FailureReason string `json:"failure_reason"`
		AllowFailure  bool   `json:"allow_failure"`
	}
	if err := a.do(ctx, http.MethodGet, fmt.Sprintf("%s/projects/%s/pipelines/%d/jobs?per_page=100", a.Service.APIBase, project, pipelines[0].ID), nil, &jobs); err != nil {
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
	return summarize(checks), nil
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
	if err := a.do(ctx, http.MethodGet, fmt.Sprintf("%s/repositories/%s/pullrequests/%d/statuses", a.Service.APIBase, r.Path, n), nil, &out); err != nil {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Service.APIBase+"/repos/"+r.Path+"/actions/jobs/"+strconv.FormatInt(c.jobID, 10)+"/logs", nil)
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

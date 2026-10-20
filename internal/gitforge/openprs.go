package gitforge

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// OpenPRsFrom lists the open pull requests whose branch starts with prefix: the ones a caller
// opened under a branch naming of its own, so it can count them. It reads one page of up to a
// hundred, which is far more than any limit a caller counts against.
func (a API) OpenPRsFrom(ctx context.Context, r Remote, prefix string) ([]PullRequest, error) {
	var all []PullRequest
	var err error
	switch a.Service.Kind {
	case KindGitLab:
		all, err = a.openGitLabMRs(ctx, r)
	case KindBitbucket:
		all, err = a.openBitbucketPRs(ctx, r, prefix)
	default:
		all, err = a.openPullsPRs(ctx, r)
	}
	if err != nil {
		return nil, err
	}
	var out []PullRequest
	for _, p := range all {
		if strings.HasPrefix(p.Head, prefix) {
			out = append(out, p)
		}
	}
	return out, nil
}

// openGitLabMRs lists the open merge requests of a GitLab project.
func (a API) openGitLabMRs(ctx context.Context, r Remote) ([]PullRequest, error) {
	var raw []struct {
		IID    int    `json:"iid"`
		WebURL string `json:"web_url"`
		Title  string `json:"title"`
		Source string `json:"source_branch"`
		Target string `json:"target_branch"`
	}
	q := url.Values{"state": {"opened"}, "per_page": {"100"}}
	if err := a.do(ctx, http.MethodGet, a.gitLabProject(r)+"/merge_requests?"+q.Encode(), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]PullRequest, 0, len(raw))
	for _, m := range raw {
		out = append(out, PullRequest{Number: m.IID, URL: m.WebURL, Title: m.Title, Head: m.Source, Base: m.Target,
			State: "open"})
	}
	return out, nil
}

// openBitbucketPRs lists the open pull requests of a Bitbucket repository whose branch contains
// prefix; the query filters on the branch name so the page is not spent on other branches.
func (a API) openBitbucketPRs(ctx context.Context, r Remote, prefix string) ([]PullRequest, error) {
	var raw struct {
		Values []struct {
			ID     int    `json:"id"`
			Title  string `json:"title"`
			Source struct {
				Branch struct {
					Name string `json:"name"`
				} `json:"branch"`
			} `json:"source"`
			Links struct {
				HTML struct {
					Href string `json:"href"`
				} `json:"html"`
			} `json:"links"`
		} `json:"values"`
	}
	q := url.Values{
		"q":       {`source.branch.name ~ "` + strings.ReplaceAll(prefix, `"`, "") + `" AND state = "OPEN"`},
		"pagelen": {"50"},
	}
	endpoint := a.Service.APIBase + "/repositories/" + r.Path + "/pullrequests?" + q.Encode()
	if err := a.do(ctx, http.MethodGet, endpoint, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]PullRequest, 0, len(raw.Values))
	for _, v := range raw.Values {
		out = append(out, PullRequest{Number: v.ID, URL: v.Links.HTML.Href, Title: v.Title, Head: v.Source.Branch.Name,
			State: "open"})
	}
	return out, nil
}

// openPullsPRs lists the open pull requests through the /pulls API GitHub and Gitea share.
func (a API) openPullsPRs(ctx context.Context, r Remote) ([]PullRequest, error) {
	var raw []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Head    struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	endpoint := a.Service.APIBase + "/repos/" + r.Path + "/pulls?state=open&per_page=100"
	if err := a.do(ctx, http.MethodGet, endpoint, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]PullRequest, 0, len(raw))
	for _, p := range raw {
		out = append(out, PullRequest{Number: p.Number, URL: p.HTMLURL, Title: p.Title, Head: p.Head.Ref,
			Base: p.Base.Ref, State: "open"})
	}
	return out, nil
}

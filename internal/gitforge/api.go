package gitforge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/oauth"
)

// API is a service plus the token to call it with.
type API struct {
	Service Service
	Cred    oauth.Credential
	// HTTP is the transport; nil means a client with a 30 second timeout.
	HTTP oauth.HTTPClient
}

// Open returns the API of a connected service, renewing its token if needed.
func (s Store) Open(ctx context.Context, svc Service) (API, error) {
	cred, err := s.Credential(ctx, svc)
	if err != nil {
		return API{}, err
	}
	return API{Service: svc, Cred: cred, HTTP: s.HTTP}, nil
}

// authHeader is how this token is presented to the host.
func (a API) authHeader() string {
	if a.Cred.Basic {
		return "Basic " + basic(a.Cred.Username, a.Cred.AccessToken)
	}
	if a.Service.Kind == KindGitea {
		return "token " + a.Cred.AccessToken
	}
	return "Bearer " + a.Cred.AccessToken
}

// GitUser is the user name git sends with the token. A password (Basic) is sent
// with the account it belongs to; an OAuth token with the name its host documents.
func GitUser(svc Service, cred oauth.Credential) string {
	switch {
	case cred.Basic && cred.Username != "":
		return cred.Username
	case svc.Kind == KindBitbucket:
		return "x-token-auth"
	case svc.Kind == KindGitHub:
		return "x-access-token"
	default:
		return "oauth2"
	}
}

// do sends one request and decodes a JSON answer into out, which every caller supplies. A non-2xx status becomes an
// error carrying the host's own message, which is what tells the user whether
// the token is wrong, expired or short of a scope.
func (a API) do(ctx context.Context, method, target string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		data, _ := json.Marshal(body) // the bodies are maps and structs of strings
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", a.authHeader())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "motita")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Message: apiMessage(data)}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("could not read the answer of %s: %w", a.Service.Name, err)
	}
	return nil
}

// HTTPError is a refusal from a host's API.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
}

// Unauthorized reports whether the host refused the token itself.
func (e *HTTPError) Unauthorized() bool { return e.Status == 401 }

// apiMessage digs the human sentence out of the error bodies of the four hosts.
func apiMessage(data []byte) string {
	var m struct {
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if json.Unmarshal(data, &m) != nil {
		return strings.TrimSpace(string(data))
	}
	if m.Message != "" {
		return m.Message
	}
	switch e := m.Error.(type) {
	case string:
		return e
	case map[string]any:
		if s, ok := e["message"].(string); ok {
			return s
		}
	}
	return strings.TrimSpace(string(data))
}

func basic(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// Whoami asks the host who the token belongs to.
func (a API) Whoami(ctx context.Context) (string, error) {
	var out struct {
		Login    string `json:"login"`
		Username string `json:"username"`
		Nickname string `json:"nickname"`
	}
	if err := a.do(ctx, http.MethodGet, a.Service.APIBase+"/user", nil, &out); err != nil {
		return "", err
	}
	for _, name := range []string{out.Login, out.Username, out.Nickname} {
		if name != "" {
			return name, nil
		}
	}
	return "", errors.New("the host did not say who this token belongs to")
}

// Repo is a repository the user can clone, as the picker shows it.
type Repo struct {
	FullName      string `json:"full_name"`
	Description   string `json:"description,omitempty"`
	CloneURL      string `json:"clone_url"`
	WebURL        string `json:"web_url,omitempty"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

// repoPageSize is how many repositories one request asks for.
const repoPageSize = 50

// maxSearchPages bounds how far a name filter reads on the hosts whose API has
// no server-side name search.
const maxSearchPages = 4

// ListRepos lists the repositories the user owns or collaborates on, most
// recently active first. query filters by name; page is 1-based. more reports
// whether a further page exists.
func (a API) ListRepos(ctx context.Context, query string, page int) (repos []Repo, more bool, err error) {
	if page < 1 {
		page = 1
	}
	query = strings.TrimSpace(query)
	switch a.Service.Kind {
	case KindGitLab:
		return a.listGitLab(ctx, query, page)
	case KindBitbucket:
		return a.listBitbucket(ctx, query, page)
	case KindGitea:
		return a.listGitea(ctx, query, page)
	default:
		return a.listGitHub(ctx, query, page)
	}
}

func matches(query string, repo Repo) bool {
	q := strings.ToLower(query)
	return q == "" || strings.Contains(strings.ToLower(repo.FullName), q) || strings.Contains(strings.ToLower(repo.Description), q)
}

func (a API) listGitHub(ctx context.Context, query string, page int) ([]Repo, bool, error) {
	pages := 1
	if query != "" {
		pages = maxSearchPages
	}
	var out []Repo
	more := false
	for i := 0; i < pages; i++ {
		var raw []struct {
			FullName      string `json:"full_name"`
			Description   string `json:"description"`
			CloneURL      string `json:"clone_url"`
			HTMLURL       string `json:"html_url"`
			Private       bool   `json:"private"`
			DefaultBranch string `json:"default_branch"`
			PushedAt      string `json:"pushed_at"`
		}
		q := url.Values{"per_page": {strconv.Itoa(repoPageSize)}, "page": {strconv.Itoa(page + i)}, "sort": {"pushed"}}
		if err := a.do(ctx, http.MethodGet, a.Service.APIBase+"/user/repos?"+q.Encode(), nil, &raw); err != nil {
			return nil, false, err
		}
		for _, r := range raw {
			repo := Repo{FullName: r.FullName, Description: r.Description, CloneURL: r.CloneURL, WebURL: r.HTMLURL,
				Private: r.Private, DefaultBranch: r.DefaultBranch, UpdatedAt: r.PushedAt}
			if matches(query, repo) {
				out = append(out, repo)
			}
		}
		more = len(raw) == repoPageSize
		if !more {
			break
		}
	}
	return out, more, nil
}

func (a API) listGitLab(ctx context.Context, query string, page int) ([]Repo, bool, error) {
	var raw []struct {
		Path          string `json:"path_with_namespace"`
		Description   string `json:"description"`
		HTTPURL       string `json:"http_url_to_repo"`
		WebURL        string `json:"web_url"`
		Visibility    string `json:"visibility"`
		DefaultBranch string `json:"default_branch"`
		LastActivity  string `json:"last_activity_at"`
	}
	q := url.Values{"membership": {"true"}, "order_by": {"last_activity_at"}, "per_page": {strconv.Itoa(repoPageSize)}, "page": {strconv.Itoa(page)}}
	if query != "" {
		q.Set("search", query)
	}
	if err := a.do(ctx, http.MethodGet, a.Service.APIBase+"/projects?"+q.Encode(), nil, &raw); err != nil {
		return nil, false, err
	}
	out := make([]Repo, 0, len(raw))
	for _, r := range raw {
		out = append(out, Repo{FullName: r.Path, Description: r.Description, CloneURL: r.HTTPURL, WebURL: r.WebURL,
			Private: r.Visibility != "public", DefaultBranch: r.DefaultBranch, UpdatedAt: r.LastActivity})
	}
	return out, len(raw) == repoPageSize, nil
}

func (a API) listBitbucket(ctx context.Context, query string, page int) ([]Repo, bool, error) {
	var raw struct {
		Next   string `json:"next"`
		Values []struct {
			FullName    string `json:"full_name"`
			Description string `json:"description"`
			IsPrivate   bool   `json:"is_private"`
			UpdatedOn   string `json:"updated_on"`
			MainBranch  struct {
				Name string `json:"name"`
			} `json:"mainbranch"`
			Links struct {
				HTML struct {
					Href string `json:"href"`
				} `json:"html"`
			} `json:"links"`
		} `json:"values"`
	}
	q := url.Values{"role": {"member"}, "sort": {"-updated_on"}, "pagelen": {strconv.Itoa(repoPageSize)}, "page": {strconv.Itoa(page)}}
	if query != "" {
		q.Set("q", `name ~ "`+strings.NewReplacer(`"`, ``, `\`, ``).Replace(query)+`"`)
	}
	if err := a.do(ctx, http.MethodGet, a.Service.APIBase+"/repositories?"+q.Encode(), nil, &raw); err != nil {
		return nil, false, err
	}
	out := make([]Repo, 0, len(raw.Values))
	for _, r := range raw.Values {
		out = append(out, Repo{FullName: r.FullName, Description: r.Description, WebURL: r.Links.HTML.Href,
			CloneURL: r.Links.HTML.Href + ".git", Private: r.IsPrivate, DefaultBranch: r.MainBranch.Name, UpdatedAt: r.UpdatedOn})
	}
	return out, raw.Next != "", nil
}

func (a API) listGitea(ctx context.Context, query string, page int) ([]Repo, bool, error) {
	var raw []struct {
		FullName      string `json:"full_name"`
		Description   string `json:"description"`
		CloneURL      string `json:"clone_url"`
		HTMLURL       string `json:"html_url"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
		UpdatedAt     string `json:"updated_at"`
	}
	q := url.Values{"limit": {strconv.Itoa(repoPageSize)}, "page": {strconv.Itoa(page)}}
	if query != "" {
		q.Set("q", query)
	}
	if err := a.do(ctx, http.MethodGet, a.Service.APIBase+"/user/repos?"+q.Encode(), nil, &raw); err != nil {
		return nil, false, err
	}
	out := make([]Repo, 0, len(raw))
	for _, r := range raw {
		out = append(out, Repo{FullName: r.FullName, Description: r.Description, CloneURL: r.CloneURL, WebURL: r.HTMLURL,
			Private: r.Private, DefaultBranch: r.DefaultBranch, UpdatedAt: r.UpdatedAt})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, len(raw) == repoPageSize, nil
}

// PullRequest is what opening one leaves behind: the number and the link the
// user follows to it.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	State  string `json:"state,omitempty"`
	Head   string `json:"head,omitempty"`
	Base   string `json:"base,omitempty"`
}

// PROptions is what opening a pull request needs.
type PROptions struct {
	Title string
	Body  string
	// Head is the branch with the work; Base is the branch it is proposed into.
	Head, Base string
	Draft      bool
}

// CreatePR opens a pull request (a merge request on GitLab).
func (a API) CreatePR(ctx context.Context, r Remote, o PROptions) (PullRequest, error) {
	pr := PullRequest{Title: o.Title, Head: o.Head, Base: o.Base, State: "open"}
	switch a.Service.Kind {
	case KindGitLab:
		var out struct {
			IID    int    `json:"iid"`
			WebURL string `json:"web_url"`
		}
		title := o.Title
		if o.Draft && !strings.HasPrefix(strings.ToLower(title), "draft:") {
			title = "Draft: " + title
		}
		body := map[string]any{"source_branch": o.Head, "target_branch": o.Base, "title": title, "description": o.Body}
		if err := a.do(ctx, http.MethodPost, a.Service.APIBase+"/projects/"+url.PathEscape(r.Path)+"/merge_requests", body, &out); err != nil {
			return PullRequest{}, err
		}
		pr.Number, pr.URL = out.IID, out.WebURL
	case KindBitbucket:
		var out struct {
			ID    int `json:"id"`
			Links struct {
				HTML struct {
					Href string `json:"href"`
				} `json:"html"`
			} `json:"links"`
		}
		body := map[string]any{
			"title": o.Title, "description": o.Body,
			"source":      map[string]any{"branch": map[string]string{"name": o.Head}},
			"destination": map[string]any{"branch": map[string]string{"name": o.Base}},
		}
		if err := a.do(ctx, http.MethodPost, a.Service.APIBase+"/repositories/"+r.Path+"/pullrequests", body, &out); err != nil {
			return PullRequest{}, err
		}
		pr.Number, pr.URL = out.ID, out.Links.HTML.Href
	default:
		var out struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
		}
		body := map[string]any{"title": o.Title, "body": o.Body, "head": o.Head, "base": o.Base}
		endpoint := a.Service.APIBase + "/repos/" + r.Path + "/pulls"
		if a.Service.Kind == KindGitHub {
			body["draft"] = o.Draft
		}
		if err := a.do(ctx, http.MethodPost, endpoint, body, &out); err != nil {
			return PullRequest{}, err
		}
		pr.Number, pr.URL = out.Number, out.HTMLURL
	}
	if pr.Number == 0 || pr.URL == "" {
		return PullRequest{}, errors.New("the host created the pull request but did not return its number and link")
	}
	return pr, nil
}

// FindPR returns the open pull request from head, or false when there is none.
func (a API) FindPR(ctx context.Context, r Remote, head string) (PullRequest, bool, error) {
	switch a.Service.Kind {
	case KindGitLab:
		var out []struct {
			IID    int    `json:"iid"`
			WebURL string `json:"web_url"`
			Title  string `json:"title"`
			Target string `json:"target_branch"`
		}
		q := url.Values{"source_branch": {head}, "state": {"opened"}}
		if err := a.do(ctx, http.MethodGet, a.Service.APIBase+"/projects/"+url.PathEscape(r.Path)+"/merge_requests?"+q.Encode(), nil, &out); err != nil || len(out) == 0 {
			return PullRequest{}, false, err
		}
		return PullRequest{Number: out[0].IID, URL: out[0].WebURL, Title: out[0].Title, Head: head, Base: out[0].Target, State: "open"}, true, nil
	case KindBitbucket:
		var out struct {
			Values []struct {
				ID    int    `json:"id"`
				Title string `json:"title"`
				Links struct {
					HTML struct {
						Href string `json:"href"`
					} `json:"html"`
				} `json:"links"`
			} `json:"values"`
		}
		q := url.Values{"q": {`source.branch.name = "` + strings.ReplaceAll(head, `"`, "") + `" AND state = "OPEN"`}}
		if err := a.do(ctx, http.MethodGet, a.Service.APIBase+"/repositories/"+r.Path+"/pullrequests?"+q.Encode(), nil, &out); err != nil || len(out.Values) == 0 {
			return PullRequest{}, false, err
		}
		v := out.Values[0]
		return PullRequest{Number: v.ID, URL: v.Links.HTML.Href, Title: v.Title, Head: head, State: "open"}, true, nil
	default:
		var out []struct {
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
		endpoint := a.Service.APIBase + "/repos/" + r.Path + "/pulls?state=open"
		if a.Service.Kind == KindGitHub {
			endpoint += "&head=" + url.QueryEscape(r.Owner()+":"+head)
		}
		if err := a.do(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
			return PullRequest{}, false, err
		}
		// Gitea has no head filter, so every host's list is matched by branch.
		for _, p := range out {
			if p.Head.Ref == head {
				return PullRequest{Number: p.Number, URL: p.HTMLURL, Title: p.Title, Head: head, Base: p.Base.Ref, State: "open"}, true, nil
			}
		}
		return PullRequest{}, false, nil
	}
}

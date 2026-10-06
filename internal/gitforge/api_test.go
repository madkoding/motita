package gitforge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/oauth"
)

// host is a fake git host: a route table keyed "METHOD /path?query" prefixes.
type host struct {
	t      *testing.T
	routes map[string]string // "GET /user" -> JSON body, or "STATUS 404 {...}"
	seen   []string
	auth   string
	body   string
}

func (h *host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.auth = r.Header.Get("Authorization")
	b := make([]byte, 4096)
	n, _ := r.Body.Read(b)
	h.body = string(b[:n])
	key := r.Method + " " + r.URL.RequestURI()
	h.seen = append(h.seen, key)
	for pattern, body := range h.routes {
		if strings.HasPrefix(key, pattern) {
			status := 200
			if rest, ok := strings.CutPrefix(body, "STATUS "); ok {
				code, payload, _ := strings.Cut(rest, " ")
				status = atoi(code)
				body = payload
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
	}
	http.Error(w, `{"message":"no route `+key+`"}`, 404)
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func newAPI(t *testing.T, kind Kind, routes map[string]string) (API, *host) {
	t.Helper()
	h := &host{t: t, routes: routes}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	svc := Service{ID: string(kind), Name: string(kind), Kind: kind, Host: "h.test", APIBase: srv.URL}
	return API{Service: svc, Cred: oauth.Credential{AccessToken: "tok", Username: "me"}}, h
}

func TestAuthHeadersAndGitUser(t *testing.T) {
	a := API{Service: Service{Kind: KindGitHub}, Cred: oauth.Credential{AccessToken: "t"}}
	if a.authHeader() != "Bearer t" {
		t.Error(a.authHeader())
	}
	a.Service.Kind = KindGitea
	if a.authHeader() != "token t" {
		t.Error(a.authHeader())
	}
	a.Cred = oauth.Credential{AccessToken: "pw", Username: "me", Basic: true}
	if a.authHeader() != "Basic bWU6cHc=" {
		t.Error(a.authHeader())
	}
	cases := []struct {
		kind Kind
		cred oauth.Credential
		want string
	}{
		{KindBitbucket, oauth.Credential{Basic: true, Username: "me"}, "me"},
		{KindBitbucket, oauth.Credential{}, "x-token-auth"},
		{KindGitHub, oauth.Credential{}, "x-access-token"},
		{KindGitLab, oauth.Credential{}, "oauth2"},
		{KindGitea, oauth.Credential{Basic: true}, "oauth2"},
	}
	for _, c := range cases {
		if got := GitUser(Service{Kind: c.kind}, c.cred); got != c.want {
			t.Errorf("%s %+v: %q, want %q", c.kind, c.cred, got, c.want)
		}
	}
}

func TestWhoamiAndErrors(t *testing.T) {
	for name, body := range map[string]string{"login": `{"login":"octo"}`, "username": `{"username":"octo"}`, "nickname": `{"nickname":"octo"}`} {
		a, h := newAPI(t, KindGitHub, map[string]string{"GET /user": body})
		got, err := a.Whoami(context.Background())
		if err != nil || got != "octo" || h.auth != "Bearer tok" {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	a, _ := newAPI(t, KindGitHub, map[string]string{"GET /user": `{}`})
	if _, err := a.Whoami(context.Background()); err == nil {
		t.Error("an answer with no name must be an error")
	}
	a, _ = newAPI(t, KindGitHub, map[string]string{"GET /user": `STATUS 401 {"message":"Bad credentials"}`})
	_, err := a.Whoami(context.Background())
	var he *HTTPError
	if !errors.As(err, &he) || !he.Unauthorized() || he.Message != "Bad credentials" || !strings.Contains(he.Error(), "401") {
		t.Errorf("err = %v", err)
	}
	a, _ = newAPI(t, KindGitHub, map[string]string{"GET /user": `not json`})
	if _, err := a.Whoami(context.Background()); err == nil || !strings.Contains(err.Error(), "could not read") {
		t.Errorf("err = %v", err)
	}
	// The host's error shapes: {"error":"x"}, {"error":{"message":"x"}}, plain text, and nothing.
	for body, want := range map[string]string{
		`STATUS 403 {"error":"denied"}`:             "denied",
		`STATUS 403 {"error":{"message":"nested"}}`: "nested",
		`STATUS 500 plain words`:                    "plain words",
		`STATUS 500 {"error":42}`:                   `{"error":42}`,
		`STATUS 500 {}`:                             "{}",
	} {
		a, _ = newAPI(t, KindGitHub, map[string]string{"GET /user": body})
		_, err := a.Whoami(context.Background())
		if !errors.As(err, &he) || he.Message != want {
			t.Errorf("%s: %v, want message %q", body, err, want)
		}
	}
	if (&HTTPError{Status: 502}).Error() != "HTTP 502" || (&HTTPError{Status: 502}).Unauthorized() {
		t.Error("an error with no message reads as its status")
	}
	// Transport failures and bad requests surface.
	dead := API{Service: Service{APIBase: "http://127.0.0.1:1"}}
	if _, err := dead.Whoami(context.Background()); err == nil {
		t.Error("an unreachable host must be an error")
	}
	bad := API{Service: Service{APIBase: "http://bad host/%"}}
	if _, err := bad.Whoami(context.Background()); err == nil {
		t.Error("a malformed URL must be an error")
	}
}

func TestOpen(t *testing.T) {
	s := Store{Dir: t.TempDir(), Env: noEnv}
	if _, err := s.Open(context.Background(), ghService()); !IsNotConnected(err) {
		t.Errorf("err = %v", err)
	}
	_ = s.Save(ghService(), oauth.Token{AccessToken: "t"}, "octo", false)
	a, err := s.Open(context.Background(), ghService())
	if err != nil || a.Cred.AccessToken != "t" {
		t.Errorf("%+v %v", a, err)
	}
}

func TestListGitHub(t *testing.T) {
	page := `[{"full_name":"me/alpha","description":"first","clone_url":"https://github.com/me/alpha.git","html_url":"https://github.com/me/alpha","private":true,"default_branch":"main","pushed_at":"2026-01-02"},
	{"full_name":"me/beta","description":"second","clone_url":"https://github.com/me/beta.git"}]`
	a, h := newAPI(t, KindGitHub, map[string]string{"GET /user/repos": page})
	repos, more, err := a.ListRepos(context.Background(), "", 0)
	if err != nil || more || len(repos) != 2 || repos[0].FullName != "me/alpha" || !repos[0].Private || repos[0].DefaultBranch != "main" {
		t.Fatalf("%+v %v %v", repos, more, err)
	}
	if !strings.Contains(h.seen[0], "page=1") {
		t.Errorf("page 0 must ask for page 1: %v", h.seen)
	}
	// A name filter matches the name or the description, case-insensitively.
	repos, _, _ = a.ListRepos(context.Background(), " SECOND ", 1)
	if len(repos) != 1 || repos[0].FullName != "me/beta" {
		t.Errorf("filtered = %+v", repos)
	}
	// A full page means there is more, and a filter reads on until a short page.
	full := "[" + strings.Repeat(`{"full_name":"me/x","clone_url":"u"},`, repoPageSize-1) + `{"full_name":"me/y","clone_url":"u"}]`
	a, h = newAPI(t, KindGitHub, map[string]string{"GET /user/repos": full})
	_, more, _ = a.ListRepos(context.Background(), "", 1)
	if !more || len(h.seen) != 1 {
		t.Errorf("more=%v seen=%d", more, len(h.seen))
	}
	_, more, _ = a.ListRepos(context.Background(), "me", 1)
	if !more || len(h.seen) != 1+maxSearchPages {
		t.Errorf("a filter must read %d pages, read %d", maxSearchPages, len(h.seen)-1)
	}
	a, _ = newAPI(t, KindGitHub, nil)
	if _, _, err := a.ListRepos(context.Background(), "", 1); err == nil {
		t.Error("a host error must surface")
	}
}

func TestListGitLab(t *testing.T) {
	a, h := newAPI(t, KindGitLab, map[string]string{"GET /projects": `[{"path_with_namespace":"grp/app","description":"d","http_url_to_repo":"https://gitlab.com/grp/app.git","web_url":"https://gitlab.com/grp/app","visibility":"private","default_branch":"main","last_activity_at":"2026-02-01"},{"path_with_namespace":"grp/pub","visibility":"public"}]`})
	repos, more, err := a.ListRepos(context.Background(), "app", 2)
	if err != nil || more || len(repos) != 2 || !repos[0].Private || repos[1].Private || repos[0].CloneURL != "https://gitlab.com/grp/app.git" {
		t.Fatalf("%+v %v %v", repos, more, err)
	}
	if !strings.Contains(h.seen[0], "search=app") || !strings.Contains(h.seen[0], "page=2") {
		t.Errorf("request = %v", h.seen)
	}
	a, _ = newAPI(t, KindGitLab, nil)
	if _, _, err := a.ListRepos(context.Background(), "", 1); err == nil {
		t.Error("a host error must surface")
	}
}

func TestListBitbucket(t *testing.T) {
	a, h := newAPI(t, KindBitbucket, map[string]string{"GET /repositories": `{"next":"x","values":[{"full_name":"ws/repo","description":"d","is_private":true,"updated_on":"2026","mainbranch":{"name":"main"},"links":{"html":{"href":"https://bitbucket.org/ws/repo"}}}]}`})
	repos, more, err := a.ListRepos(context.Background(), `re"po\`, 1)
	if err != nil || !more || len(repos) != 1 || repos[0].CloneURL != "https://bitbucket.org/ws/repo.git" || repos[0].DefaultBranch != "main" {
		t.Fatalf("%+v %v %v", repos, more, err)
	}
	// Quotes and backslashes cannot break out of the query expression.
	if strings.Contains(h.seen[0], "%5C") || strings.Contains(h.seen[0], "%22re%22po") {
		t.Errorf("unsanitised query: %s", h.seen[0])
	}
	a, _ = newAPI(t, KindBitbucket, nil)
	if _, _, err := a.ListRepos(context.Background(), "", 1); err == nil {
		t.Error("a host error must surface")
	}
}

func TestListGitea(t *testing.T) {
	a, h := newAPI(t, KindGitea, map[string]string{"GET /user/repos": `[{"full_name":"o/old","clone_url":"u1","updated_at":"2025"},{"full_name":"o/new","clone_url":"u2","html_url":"w","private":true,"default_branch":"main","updated_at":"2026"}]`})
	repos, more, err := a.ListRepos(context.Background(), "o", 1)
	if err != nil || more || len(repos) != 2 || repos[0].FullName != "o/new" {
		t.Fatalf("%+v %v %v", repos, more, err)
	}
	if !strings.Contains(h.seen[0], "q=o") {
		t.Errorf("request = %v", h.seen)
	}
	a, _ = newAPI(t, KindGitea, nil)
	if _, _, err := a.ListRepos(context.Background(), "", 1); err == nil {
		t.Error("a host error must surface")
	}
}

func TestCreatePR(t *testing.T) {
	opt := PROptions{Title: "feat: x", Body: "why", Head: "feature", Base: "main", Draft: true}
	a, h := newAPI(t, KindGitHub, map[string]string{"POST /repos/o/r/pulls": `{"number":7,"html_url":"https://github.com/o/r/pull/7"}`})
	pr, err := a.CreatePR(context.Background(), Remote{Path: "o/r"}, opt)
	if err != nil || pr.Number != 7 || pr.URL != "https://github.com/o/r/pull/7" || pr.Head != "feature" || !strings.Contains(h.body, `"draft":true`) {
		t.Fatalf("%+v %v body=%s", pr, err, h.body)
	}
	a, h = newAPI(t, KindGitea, map[string]string{"POST /repos/o/r/pulls": `{"number":3,"html_url":"https://gitea.example.com/o/r/pulls/3"}`})
	if pr, err = a.CreatePR(context.Background(), Remote{Path: "o/r"}, opt); err != nil || pr.Number != 3 || strings.Contains(h.body, "draft") {
		t.Fatalf("%+v %v body=%s", pr, err, h.body)
	}
	a, h = newAPI(t, KindGitLab, map[string]string{"POST /projects/grp%2Fsub%2Fr/merge_requests": `{"iid":9,"web_url":"https://gitlab.com/grp/sub/r/-/merge_requests/9"}`})
	if pr, err = a.CreatePR(context.Background(), Remote{Path: "grp/sub/r"}, opt); err != nil || pr.Number != 9 || !strings.Contains(h.body, "Draft: feat: x") {
		t.Fatalf("%+v %v body=%s", pr, err, h.body)
	}
	opt.Title = "Draft: feat: x"
	if _, err = a.CreatePR(context.Background(), Remote{Path: "grp/sub/r"}, opt); err != nil || strings.Contains(h.body, "Draft: Draft") {
		t.Fatalf("an already-draft title must not be prefixed twice: %v %s", err, h.body)
	}
	a, h = newAPI(t, KindBitbucket, map[string]string{"POST /repositories/ws/r/pullrequests": `{"id":4,"links":{"html":{"href":"https://bitbucket.org/ws/r/pull-requests/4"}}}`})
	if pr, err = a.CreatePR(context.Background(), Remote{Path: "ws/r"}, opt); err != nil || pr.Number != 4 || !strings.Contains(h.body, `"destination"`) {
		t.Fatalf("%+v %v body=%s", pr, err, h.body)
	}
	for _, kind := range []Kind{KindGitHub, KindGitLab, KindBitbucket} {
		a, _ = newAPI(t, kind, nil)
		if _, err := a.CreatePR(context.Background(), Remote{Path: "o/r"}, opt); err == nil {
			t.Errorf("%s: a refusal must surface", kind)
		}
	}
	// A host that answers 200 with no number and link is not a success.
	a, _ = newAPI(t, KindGitHub, map[string]string{"POST /repos/o/r/pulls": `{}`})
	if _, err := a.CreatePR(context.Background(), Remote{Path: "o/r"}, opt); err == nil {
		t.Error("an answer with no pull request must be an error")
	}
}

func TestFindPR(t *testing.T) {
	r := Remote{Path: "o/r"}
	a, h := newAPI(t, KindGitHub, map[string]string{"GET /repos/o/r/pulls": `[{"number":5,"html_url":"u5","title":"t","head":{"ref":"feature"},"base":{"ref":"main"}}]`})
	pr, ok, err := a.FindPR(context.Background(), r, "feature")
	if err != nil || !ok || pr.Number != 5 || pr.Base != "main" || !strings.Contains(h.seen[0], "head=o%3Afeature") {
		t.Fatalf("%+v %v %v %v", pr, ok, err, h.seen)
	}
	if _, ok, _ = a.FindPR(context.Background(), r, "other"); ok {
		t.Error("a pull request from another branch is not this one")
	}
	a, _ = newAPI(t, KindGitea, map[string]string{"GET /repos/o/r/pulls": `[{"number":2,"html_url":"u2","head":{"ref":"feature"},"base":{"ref":"dev"}}]`})
	if pr, ok, err = a.FindPR(context.Background(), r, "feature"); err != nil || !ok || pr.Number != 2 {
		t.Fatalf("%+v %v %v", pr, ok, err)
	}
	a, _ = newAPI(t, KindGitHub, nil)
	if _, _, err = a.FindPR(context.Background(), r, "feature"); err == nil {
		t.Error("a host error must surface")
	}

	a, _ = newAPI(t, KindGitLab, map[string]string{"GET /projects/o%2Fr/merge_requests": `[{"iid":6,"web_url":"u6","title":"t","target_branch":"main"}]`})
	if pr, ok, err = a.FindPR(context.Background(), r, "feature"); err != nil || !ok || pr.Number != 6 || pr.Base != "main" {
		t.Fatalf("%+v %v %v", pr, ok, err)
	}
	a, _ = newAPI(t, KindGitLab, map[string]string{"GET /projects/o%2Fr/merge_requests": `[]`})
	if _, ok, err = a.FindPR(context.Background(), r, "feature"); err != nil || ok {
		t.Errorf("%v %v", ok, err)
	}
	a, _ = newAPI(t, KindGitLab, nil)
	if _, _, err = a.FindPR(context.Background(), r, "feature"); err == nil {
		t.Error("a host error must surface")
	}

	a, _ = newAPI(t, KindBitbucket, map[string]string{"GET /repositories/o/r/pullrequests": `{"values":[{"id":8,"title":"t","links":{"html":{"href":"u8"}}}]}`})
	if pr, ok, err = a.FindPR(context.Background(), r, `fea"ture`); err != nil || !ok || pr.Number != 8 {
		t.Fatalf("%+v %v %v", pr, ok, err)
	}
	a, _ = newAPI(t, KindBitbucket, map[string]string{"GET /repositories/o/r/pullrequests": `{"values":[]}`})
	if _, ok, err = a.FindPR(context.Background(), r, "x"); err != nil || ok {
		t.Errorf("%v %v", ok, err)
	}
	a, _ = newAPI(t, KindBitbucket, nil)
	if _, _, err = a.FindPR(context.Background(), r, "x"); err == nil {
		t.Error("a host error must surface")
	}
}

// brokenBody is an answer that dies halfway: the connection drops while the
// host is still writing.
type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (brokenBody) Close() error             { return nil }

type brokenHTTP struct{ status int }

func (b brokenHTTP) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: b.status, Body: brokenBody{}, Header: http.Header{}}, nil
}

func TestAnAnswerCutShortIsAnError(t *testing.T) {
	a := API{Service: Service{Kind: KindGitHub, APIBase: "http://x"}, HTTP: brokenHTTP{200}}
	if _, err := a.Whoami(context.Background()); err == nil || !strings.Contains(err.Error(), "reset") {
		t.Errorf("err = %v", err)
	}
	if _, _, err := a.FailureLog(context.Background(), Remote{Path: "o/r"}, Check{jobID: 1}); err == nil {
		t.Error("a log cut short must be an error")
	}
	// A non-2xx answer whose body cannot be read is still reported, by status.
	a.HTTP = brokenHTTP{500}
	if _, err := a.Whoami(context.Background()); err == nil {
		t.Error("err = nil")
	}
}

func TestMergePR(t *testing.T) {
	cases := []struct {
		kind  Kind
		path  string
		route string
		body  string
	}{
		{KindGitHub, "o/r", "PUT /repos/o/r/pulls/7/merge", `{"merged":true}`},
		{KindGitea, "o/r", "POST /repos/o/r/pulls/7/merge", ``}, // Gitea answers with no body
		{KindGitLab, "grp/r", "PUT /projects/grp%2Fr/merge_requests/7/merge", `{"state":"merged"}`},
		{KindBitbucket, "ws/r", "POST /repositories/ws/r/pullrequests/7/merge", `{"state":"MERGED"}`},
	}
	for _, c := range cases {
		a, h := newAPI(t, c.kind, map[string]string{c.route: c.body})
		if _, err := a.MergePR(context.Background(), Remote{Path: c.path}, 7, MergeOptions{}); err != nil {
			t.Errorf("%s: %v", c.kind, err)
		}
		if len(h.seen) != 1 || !strings.HasPrefix(h.seen[0], c.route) {
			t.Errorf("%s: asked %v, want %s", c.kind, h.seen, c.route)
		}
	}
	a, _ := newAPI(t, KindGitHub, map[string]string{"PUT /repos/o/r/pulls/7/merge": `STATUS 405 {"message":"Pull Request is not mergeable"}`})
	if _, err := a.MergePR(context.Background(), Remote{Path: "o/r"}, 7, MergeOptions{}); err == nil || !strings.Contains(err.Error(), "not mergeable") {
		t.Errorf("the host's refusal must surface: %v", err)
	}
}

func TestMergePRMethodAndBranchDeletion(t *testing.T) {
	opt := MergeOptions{Method: "squash", DeleteBranch: true, Branch: "motita/s1"}
	a, h := newAPI(t, KindGitHub, map[string]string{"PUT /repos/o/r/pulls/7/merge": `{"merged":true}`, "DELETE /repos/o/r/git/refs/heads/motita/s1": ``})
	if _, err := a.MergePR(context.Background(), Remote{Path: "o/r"}, 7, opt); err != nil {
		t.Fatal(err)
	}
	if len(h.seen) != 2 || !strings.HasPrefix(h.seen[1], "DELETE /repos/o/r/git/refs/heads/motita/s1") {
		t.Errorf("the branch is deleted after the merge: %v", h.seen)
	}
	// A branch that cannot be deleted is not a failed merge.
	a, _ = newAPI(t, KindGitHub, map[string]string{"PUT /repos/o/r/pulls/7/merge": `{"merged":true}`})
	if _, err := a.MergePR(context.Background(), Remote{Path: "o/r"}, 7, opt); err != nil {
		t.Errorf("a refused deletion must not fail the merge: %v", err)
	}
	a, h = newAPI(t, KindGitea, map[string]string{"POST /repos/o/r/pulls/7/merge": ``})
	_, _ = a.MergePR(context.Background(), Remote{Path: "o/r"}, 7, opt)
	if !strings.Contains(h.body, `"Do":"squash"`) || !strings.Contains(h.body, `"delete_branch_after_merge":true`) {
		t.Errorf("gitea body: %s", h.body)
	}
	a, h = newAPI(t, KindGitLab, map[string]string{"PUT /projects/o%2Fr/merge_requests/7/merge": `{}`})
	_, _ = a.MergePR(context.Background(), Remote{Path: "o/r"}, 7, opt)
	if !strings.Contains(h.body, `"squash":true`) || !strings.Contains(h.body, `"should_remove_source_branch":true`) {
		t.Errorf("gitlab body: %s", h.body)
	}
	a, h = newAPI(t, KindBitbucket, map[string]string{"POST /repositories/ws/r/pullrequests/7/merge": `{}`})
	_, _ = a.MergePR(context.Background(), Remote{Path: "ws/r"}, 7, MergeOptions{Method: "rebase", DeleteBranch: true})
	if !strings.Contains(h.body, `"fast_forward"`) || !strings.Contains(h.body, `"close_source_branch":true`) {
		t.Errorf("bitbucket body: %s", h.body)
	}
}

func TestBranchCI(t *testing.T) {
	a, _ := newAPI(t, KindGitHub, map[string]string{
		"GET /repos/o/r/commits/main/check-runs": `{"check_runs":[{"id":1,"name":"test","status":"completed","conclusion":"failure"}]}`,
		"GET /repos/o/r/commits/main/status":     `{"statuses":[]}`,
	})
	st, err := a.BranchCI(context.Background(), Remote{Path: "o/r"}, "main")
	if err != nil || st.State != StateFailure || len(st.FailedNames()) != 1 || st.FailedNames()[0] != "test" {
		t.Fatalf("%+v %v", st, err)
	}
	a, _ = newAPI(t, KindGitLab, map[string]string{
		"GET /projects/o%2Fr/pipelines?":       `[{"id":5}]`,
		"GET /projects/o%2Fr/pipelines/5/jobs": `[{"name":"lint","status":"failed"}]`,
	})
	if st, err = a.BranchCI(context.Background(), Remote{Path: "o/r"}, "main"); err != nil || st.State != StateFailure || st.Rev != "5" {
		t.Fatalf("%+v %v", st, err)
	}
	a, _ = newAPI(t, KindBitbucket, nil)
	if st, _ = a.BranchCI(context.Background(), Remote{Path: "o/r"}, "main"); st.State != StateNone {
		t.Errorf("bitbucket has no branch checks: %+v", st)
	}
}

func TestMergePRReturnsTheCommit(t *testing.T) {
	a, _ := newAPI(t, KindGitHub, map[string]string{"PUT /repos/o/r/pulls/7/merge": `{"merged":true,"sha":"deadbeef"}`})
	if sha, err := a.MergePR(context.Background(), Remote{Path: "o/r"}, 7, MergeOptions{}); err != nil || sha != "deadbeef" {
		t.Errorf("%q %v", sha, err)
	}
	a, _ = newAPI(t, KindGitLab, map[string]string{"PUT /projects/o%2Fr/merge_requests/7/merge": `{"merge_commit_sha":"c0ffee"}`})
	if sha, _ := a.MergePR(context.Background(), Remote{Path: "o/r"}, 7, MergeOptions{}); sha != "c0ffee" {
		t.Errorf("gitlab: %q", sha)
	}
}

func TestMergeState(t *testing.T) {
	gh := func(body string) MergeState {
		a, _ := newAPI(t, KindGitHub, map[string]string{"GET /repos/o/r/pulls/7": body})
		m, err := a.MergeState(context.Background(), Remote{Path: "o/r"}, 7)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	for body, want := range map[string]string{
		`{"mergeable_state":"clean"}`:    MergeOK,
		`{"mergeable_state":"unstable"}`: MergeOK,
		`{"mergeable_state":"blocked"}`:  MergeBlocked,
		`{"mergeable_state":"dirty"}`:    MergeConflict,
		`{"mergeable_state":"behind"}`:   MergeBehind,
		`{"draft":true}`:                 MergeDraft,
		`{"mergeable_state":"unknown"}`:  MergeUnknown,
	} {
		if got := gh(body); got.Code != want {
			t.Errorf("github %s: %s, want %s", body, got.Code, want)
		}
	}
	if !(MergeState{MergeBlocked}).Refuses() || (MergeState{MergeBehind}).Refuses() || (MergeState{MergeUnknown}).Refuses() {
		t.Error("only a blocked, conflicting or draft pull request refuses a merge")
	}
	a, _ := newAPI(t, KindGitLab, map[string]string{"GET /projects/o%2Fr/merge_requests/7": `{"detailed_merge_status":"not_approved"}`})
	if m, _ := a.MergeState(context.Background(), Remote{Path: "o/r"}, 7); m.Code != MergeBlocked {
		t.Errorf("gitlab: %v", m)
	}
	a, _ = newAPI(t, KindGitea, map[string]string{"GET /repos/o/r/pulls/7": `{"mergeable":false}`})
	if m, _ := a.MergeState(context.Background(), Remote{Path: "o/r"}, 7); m.Code != MergeConflict {
		t.Errorf("gitea: %v", m)
	}
	a, _ = newAPI(t, KindBitbucket, nil)
	if m, _ := a.MergeState(context.Background(), Remote{Path: "ws/r"}, 7); m.Code != MergeUnknown {
		t.Errorf("bitbucket: %v", m)
	}
}

func TestPRStateTellsMergedFromClosed(t *testing.T) {
	ctx := context.Background()
	check := func(kind Kind, route, body, want, sha string) {
		t.Helper()
		a, _ := newAPI(t, kind, map[string]string{route: body})
		path := "o/r"
		st, err := a.PRState(ctx, Remote{Path: path}, 7)
		if err != nil || st.State != want || st.SHA != sha {
			t.Errorf("%s %s: %+v %v, want %s %s", kind, body, st, err, want, sha)
		}
	}
	check(KindGitHub, "GET /repos/o/r/pulls/7", `{"state":"closed","merged":true,"merge_commit_sha":"abc"}`, PRMerged, "abc")
	check(KindGitHub, "GET /repos/o/r/pulls/7", `{"state":"closed","merged":false}`, PRClosed, "")
	check(KindGitHub, "GET /repos/o/r/pulls/7", `{"state":"open"}`, PROpen, "")
	check(KindGitea, "GET /repos/o/r/pulls/7", `{"state":"closed","merged":true}`, PRMerged, "")
	check(KindGitLab, "GET /projects/o%2Fr/merge_requests/7", `{"state":"merged","merge_commit_sha":"c0"}`, PRMerged, "c0")
	check(KindGitLab, "GET /projects/o%2Fr/merge_requests/7", `{"state":"closed"}`, PRClosed, "")
	check(KindBitbucket, "GET /repositories/o/r/pullrequests/7", `{"state":"MERGED"}`, PRMerged, "")
	check(KindBitbucket, "GET /repositories/o/r/pullrequests/7", `{"state":"DECLINED"}`, PRClosed, "")
}

// etagHost answers like GitHub: a read carries an ETag, and a read that presents it gets 304.
type etagHost struct {
	full, notModified int
	body              string
}

func (h *etagHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("If-None-Match") == `"v1"` {
		h.notModified++
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.full++
	w.Header().Set("ETag", `"v1"`)
	_, _ = w.Write([]byte(h.body))
}

func TestReadsAreConditional(t *testing.T) {
	h := &etagHost{body: `[{"number":7,"html_url":"https://x/7","title":"t","head":{"ref":"b"},"base":{"ref":"main"}}]`}
	srv := httptest.NewServer(h)
	defer srv.Close()
	svc := Service{ID: "github", Kind: KindGitHub, APIBase: srv.URL}
	a := API{Service: svc, Cred: oauth.Credential{AccessToken: "tok"}, Cache: &ETagCache{}}
	for i := 0; i < 3; i++ {
		pr, found, err := a.FindPR(context.Background(), Remote{Path: "o/r"}, "b")
		if err != nil || !found || pr.Number != 7 {
			t.Fatalf("read %d: %+v %v %v", i, pr, found, err)
		}
	}
	if h.full != 1 || h.notModified != 2 {
		t.Errorf("one full answer and two 'not modified': %d %d", h.full, h.notModified)
	}

	// Another account does not get the first one's answers.
	b := API{Service: svc, Cred: oauth.Credential{AccessToken: "other"}, Cache: a.Cache}
	if _, _, err := b.FindPR(context.Background(), Remote{Path: "o/r"}, "b"); err != nil || h.full != 2 {
		t.Errorf("a different token is a different reader: %v full=%d", err, h.full)
	}

	// Without a cache nothing is conditional.
	c := API{Service: svc, Cred: oauth.Credential{AccessToken: "tok"}}
	before := h.notModified
	_, _, _ = c.FindPR(context.Background(), Remote{Path: "o/r"}, "b")
	if h.notModified != before {
		t.Error("no cache, no conditional request")
	}
}

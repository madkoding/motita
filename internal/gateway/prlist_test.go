package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPRListShowsEveryOpenPullRequestWithItsCI(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	prHost(host, "feature/other", "failure", "behind")
	c := srv.sessions[ss.ID]
	c.setPRWatch(&PRWatchView{Status: prFollowing, Number: 7})

	w := get(t, srv, "/v1/pull-requests", testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var l PRList
	decodeJSON(t, w, &l)
	if len(l.PullRequests) != 1 || len(l.Problems) != 0 {
		t.Fatalf("list = %+v", l)
	}
	it := l.PullRequests[0]
	if it.PR.Number != 7 || it.Repo != "o/r" || it.ProjectID != ss.ProjectID || it.PR.Head != "feature/other" {
		t.Errorf("item = %+v", it)
	}
	if it.CI == nil || it.CI.State != "failure" || it.Merge == nil || it.Merge.Code != "behind" {
		t.Errorf("ci %+v merge %+v", it.CI, it.Merge)
	}
	if it.Session != ss.ID || it.Watch == nil || it.Watch.Status != prFollowing {
		t.Errorf("the session that follows it: %q %+v", it.Session, it.Watch)
	}
}

func TestPRListIsRememberedBrieflyAndRefreshedOnDemand(t *testing.T) {
	srv, host, _ := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	prHost(host, "feature/a", "success", "clean")
	count := func() int {
		var l PRList
		decodeJSON(t, get(t, srv, "/v1/pull-requests", testToken), &l)
		return len(l.PullRequests)
	}
	if count() != 1 {
		t.Fatal("first read")
	}
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	if count() != 1 {
		t.Error("a second read inside the memory does not ask the host again")
	}
	var l PRList
	decodeJSON(t, get(t, srv, "/v1/pull-requests?refresh=1", testToken), &l)
	if len(l.PullRequests) != 0 {
		t.Error("refresh asks the host again")
	}
	srv.prLists.mu.Lock()
	srv.prLists.at = time.Now().Add(-2 * prListTTL)
	srv.prLists.mu.Unlock()
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open",
		`[{"number":9,"html_url":"u","title":"t","head":{"ref":"b"},"base":{"ref":"main"}}]`)
	if count() != 1 {
		t.Error("an old memory is read again")
	}
}

func TestPRListSaysWhyAProjectCannotBeRead(t *testing.T) {
	srv, host, _ := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `STATUS 500 {"message":"boom"}`)
	var l PRList
	decodeJSON(t, get(t, srv, "/v1/pull-requests", testToken), &l)
	if len(l.PullRequests) != 0 || len(l.Problems) != 1 {
		t.Fatalf("the host failed: %+v", l)
	}
	// A project with no remote is a problem to show, not a reason to hide the rest.
	srv2, _ := gitServer(t)
	withProjectsAndFake(t, srv2, t.TempDir())
	makeProject(t, srv2, "bare")
	var l2 PRList
	decodeJSON(t, get(t, srv2, "/v1/pull-requests", testToken), &l2)
	if len(l2.Problems) != 1 || l2.PullRequests == nil {
		t.Errorf("no remote: %+v", l2)
	}
}

func TestPRListReadsASharedRepositoryOnce(t *testing.T) {
	srv, host, _ := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	prHost(host, "feature/a", "success", "clean")
	twin := makeProject(t, srv, "twin")
	mustRun(t, "git", "-C", srv.projectOf(twin).Dir, "remote", "add", "origin", "https://github.com/o/r.git")
	var l PRList
	decodeJSON(t, get(t, srv, "/v1/pull-requests", testToken), &l)
	if len(l.PullRequests) != 1 {
		t.Errorf("two projects of one repository list its pull requests once: %+v", l.PullRequests)
	}
}

func TestPRListIncludesTheOtherRepositoriesOfAProject(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	prHost(host, "feature/a", "failure", "clean")
	api := filepath.Join(t.TempDir(), "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, api)
	p := *srv.projectOf(ss.ProjectID)
	p.Repos = []ProjectRepo{
		{Name: "api", Dir: api, GitURL: "https://github.com/o/api.git"},
		{Name: "gone", Dir: filepath.Join(api, "missing")},
	}
	if err := srv.projects.save(p); err != nil {
		t.Fatal(err)
	}
	host.set("GET https://api.github.com/repos/o/api/pulls?state=open",
		`[{"number":3,"html_url":"u","title":"nested","head":{"ref":"n"},"base":{"ref":"main"}}]`)
	host.set("GET https://api.github.com/repos/o/api/pulls/3", `{"head":{"sha":"def"},"mergeable_state":"clean"}`)
	host.set("GET https://api.github.com/repos/o/api/commits/def/status", `{"statuses":[]}`)
	host.set("GET https://api.github.com/repos/o/api/commits/def/check-runs",
		`{"check_runs":[{"id":2,"name":"t","status":"completed","conclusion":"failure"}]}`)

	var l PRList
	decodeJSON(t, get(t, srv, "/v1/pull-requests", testToken), &l)
	if len(l.PullRequests) != 2 || l.Failing != 2 {
		t.Fatalf("both repositories, both failing: %+v", l)
	}
	if len(l.Problems) != 1 || !strings.HasSuffix(l.Problems[0].Project, " / gone") {
		t.Errorf("a repository that is gone is a problem named after it: %+v", l.Problems)
	}
}

func TestPRBranchCanBeUpdatedFromTheOverview(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	body := func(pid, repo string, n int) string {
		return fmt.Sprintf(`{"project_id":%q,"repo":%q,"number":%d}`, pid, repo, n)
	}
	update := func(pid, repo string, n int) int {
		return postJSON(t, srv, "/v1/pull-requests/update", testToken, body(pid, repo, n)).Code
	}
	host.set(updateBranchRoute, `{"message":"Updating pull request branch."}`)
	if c := update(ss.ProjectID, "o/r", 7); c != http.StatusAccepted || updates(host) != 1 {
		t.Fatalf("update: %d, asked %d times", c, updates(host))
	}
	if _, fresh := srv.prLists.fresh(); fresh {
		t.Error("the remembered overview is forgotten, so the next read shows the new state")
	}
	if c := update("nope", "o/r", 7); c != http.StatusNotFound {
		t.Errorf("unknown project: %d", c)
	}
	if c := update(ss.ProjectID, "o/r", 0); c != http.StatusNotFound {
		t.Errorf("no number: %d", c)
	}
	if c := update(ss.ProjectID, "o/other", 7); c != http.StatusNotFound {
		t.Errorf("a repository the project does not have: %d", c)
	}
	host.set(updateBranchRoute, `STATUS 500 {"message":"boom"}`)
	if c := update(ss.ProjectID, "o/r", 7); c != http.StatusBadGateway {
		t.Errorf("a host that fails: %d", c)
	}
	if w := postJSON(t, srv, "/v1/pull-requests/update", testToken, `{`); w.Code != http.StatusBadRequest {
		t.Errorf("bad body: %d", w.Code)
	}
}

func TestPRHostsThatOnlyRebaseAreNotAskedToUpdate(t *testing.T) {
	srv, host, ss := prSession(t, "https://gitlab.com/o/r.git")
	host.set("GET https://gitlab.com/api/v4/user", `{"username":"octo"}`)
	if w := postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"gitlab","token":"glpat_x"}`); w.Code != 200 {
		t.Fatalf("connect gitlab: %d %s", w.Code, w.Body.String())
	}
	w := postJSON(t, srv, "/v1/pull-requests/update", testToken,
		fmt.Sprintf(`{"project_id":%q,"repo":"o/r","number":7}`, ss.ProjectID))
	if w.Code != http.StatusConflict {
		t.Errorf("gitlab only rebases: %d %s", w.Code, w.Body.String())
	}
}

func TestPRTurningRedIsToldOnce(t *testing.T) {
	srv, host, _ := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	prHost(host, "feature/a", "success", "clean")
	ctx := context.Background()
	red := func() int {
		return srv.announceRedPRs(ctx)
	}
	if red() != 0 {
		t.Fatal("the first look is the baseline")
	}
	prHost(host, "feature/a", "failure", "clean")
	if red() != 1 {
		t.Fatal("it turned red: news")
	}
	if red() != 0 {
		t.Fatal("still red: not news again")
	}
	prHost(host, "feature/a", "success", "clean")
	red()
	prHost(host, "feature/a", "failure", "clean")
	if red() != 1 {
		t.Fatal("red again after a pass: news again")
	}
}

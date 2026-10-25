package gateway

import (
	"net/http"
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

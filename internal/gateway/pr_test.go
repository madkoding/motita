package gateway

import (
	"context"
	"encoding/json"
	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/gitx"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// prSession is a gateway with a git host, a project, and a session in its own
// worktree. origin, when not empty, is added to the worktree's repository.
func prSession(t *testing.T, origin string) (*Server, *gitHost, SessionStatus) {
	t.Helper()
	srv, host := gitServer(t)
	withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var ss SessionStatus
	if err := json.Unmarshal(w.Body.Bytes(), &ss); err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		mustRun(t, "git", "-C", ss.Workspace, "remote", "add", "origin", origin)
	}
	return srv, host, ss
}

func connectGitHub(t *testing.T, srv *Server) {
	t.Helper()
	if w := postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github","token":"gho_x"}`); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
}

func TestPRNeedsAProjectSession(t *testing.T) {
	srv, _ := gitServer(t)
	if w := get(t, srv, sessionPath(srv, DefaultSession, "/pr"), testToken); w.Code != http.StatusConflict {
		t.Fatalf("a session with no project has no pull request: %d %s", w.Code, w.Body.String())
	}
}

func TestPRReasonsItCannotBeRead(t *testing.T) {
	srv, _, ss := prSession(t, "")
	if w := get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken); w.Code != http.StatusConflict || decode(t, w)["code"] != errPRNoRemote {
		t.Errorf("no origin: %d %s", w.Code, w.Body.String())
	}

	srv, _, ss = prSession(t, "https://nowhere.example/o/r.git")
	if w := get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken); w.Code != http.StatusConflict || decode(t, w)["code"] != errPRNoHost {
		t.Errorf("unknown host: %d %s", w.Code, w.Body.String())
	}

	srv, _, ss = prSession(t, "https://github.com/o/r.git")
	w := get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken)
	if m := decode(t, w); w.Code != http.StatusConflict || m["code"] != errGitAuthRequired || m["service"] != "github" {
		t.Errorf("not connected: %d %s", w.Code, w.Body.String())
	}
}

func TestPRReportsTheCIOfTheBranch(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	path := sessionPath(srv, ss.ID, "/pr")

	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	if m := decode(t, get(t, srv, path, testToken)); m["state"] != "none" || m["pr"] != nil {
		t.Fatalf("a branch with no pull request: %v", m)
	}

	host.set("GET https://api.github.com/repos/o/r/pulls?state=open",
		`[{"number":7,"html_url":"https://github.com/o/r/pull/7","title":"feat: x","head":{"ref":"`+sessionBranch(ss.ID)+`"},"base":{"ref":"main"}}]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7", `{"head":{"sha":"abc123"}}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc123/check-runs", `{"check_runs":[{"id":1,"name":"test","status":"completed","conclusion":"failure"}]}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc123/status", `{"statuses":[]}`)
	m := decode(t, get(t, srv, path, testToken))
	ci, _ := m["ci"].(map[string]any)
	if m["state"] != "open" || ci["state"] != "failure" || ci["rev"] != "abc123" || m["pr"].(map[string]any)["number"] != float64(7) {
		t.Fatalf("an open pull request with a failing CI: %v", m)
	}

	// The refusal of a pull request already open is the same answer, whoever asks.
	w := postJSON(t, srv, path, testToken, `{}`)
	if m := decode(t, w); w.Code != http.StatusConflict || m["code"] != "pr_exists" {
		t.Errorf("opening a second pull request: %d %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, srv, path+"/fix", testToken, `{}`); w.Code != http.StatusAccepted {
		t.Errorf("fixing the CI: %d %s", w.Code, w.Body.String())
	}
}

func TestPRFixNeedsAPullRequest(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	if w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr/fix"), testToken, `{}`); w.Code != http.StatusConflict {
		t.Fatalf("nothing to fix: %d %s", w.Code, w.Body.String())
	}
}

func TestPRRefusesASessionWithNoWork(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	if w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken, `{}`); w.Code != http.StatusConflict {
		t.Fatalf("nothing to propose: %d %s", w.Code, w.Body.String())
	}
}

func ci(state, rev string, failed ...string) *gitforge.CIStatus {
	st := &gitforge.CIStatus{State: state, Rev: rev}
	for _, n := range failed {
		st.Checks = append(st.Checks, gitforge.Check{Name: n, State: gitforge.StateFailure})
	}
	return st
}

func TestPRWatchStepsThroughTheCI(t *testing.T) {
	w := &prWatchState{status: prFollowing}
	if got := w.step(ci(gitforge.StatePending, "a"), 2); got != "" || w.status != prFollowing {
		t.Fatalf("pending: %q %s", got, w.status)
	}
	if got := w.step(ci(gitforge.StateFailure, "a"), 2); got != "fix" || w.attempts != 1 || w.status != prFixing {
		t.Fatalf("first failure: %q %d %s", got, w.attempts, w.status)
	}
	if got := w.step(ci(gitforge.StateFailure, "a"), 2); got != "" {
		t.Fatalf("the same push is answered once: %q", got)
	}
	w.step(ci(gitforge.StatePending, "b"), 2)
	if got := w.step(ci(gitforge.StateFailure, "b"), 2); got != "fix" || w.attempts != 2 {
		t.Fatalf("a new push that fails goes round again: %q %d", got, w.attempts)
	}
	if got := w.step(ci(gitforge.StateFailure, "c"), 2); got != prGaveUp || w.status != prGaveUp {
		t.Fatalf("past the allowed attempts it gives up: %q %s", got, w.status)
	}

	w = &prWatchState{status: prFollowing}
	if got := w.step(ci(gitforge.StateSuccess, "d"), 2); got != prPassed {
		t.Fatalf("success: %q", got)
	}

	w = &prWatchState{status: prFollowing}
	got := ""
	for i := 0; i < prNoneLimit; i++ {
		got = w.step(ci(gitforge.StateNone, ""), 2)
	}
	if got != prNoCI {
		t.Fatalf("no CI at all gives up after a few polls: %q", got)
	}
	if k1, k2 := ciKey(ci("failure", "", "test")), ciKey(ci("failure", "", "lint")); k1 == k2 {
		t.Error("without a revision a failure is told apart by its jobs")
	}
}

func TestPRWatchFollowsAFailureToAPass(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open",
		`[{"number":7,"html_url":"https://github.com/o/r/pull/7","title":"feat: x","head":{"ref":"`+sessionBranch(ss.ID)+`"},"base":{"ref":"main"}}]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7", `{"head":{"sha":"abc"}}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc/status", `{"statuses":[]}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs", `{"check_runs":[{"id":1,"name":"test","status":"completed","conclusion":"failure"}]}`)

	w := &prWatchState{status: prFollowing}
	if srv.pollPR(c, w) {
		t.Fatal("a failure is not the end of the watch")
	}
	if v := c.prWatchView(); v == nil || v.Status != prFixing || v.Attempts != 1 || v.Number != 7 {
		t.Fatalf("the failure is reported: %+v", v)
	}
	// The session list carries it, so a tab that is looking at another session can say so.
	if st := c.status(); st.PRWatch == nil || st.PRWatch.Status != prFixing {
		t.Errorf("the list must say what the watch is doing: %+v", st.PRWatch)
	}

	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs", `{"check_runs":[{"id":1,"name":"test","status":"completed","conclusion":"success"}]}`)
	if srv.pollPR(c, w) {
		t.Fatal("a pass is not the end: the pull request is watched until it is merged or closed")
	}
	if v := c.prWatchView(); v.Status != prPassed {
		t.Fatalf("passed: %+v", v)
	}

	// Merging is refused until the CI has passed, and goes through once it has.
	path := sessionPath(srv, ss.ID, "/pr/merge")
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs", `{"check_runs":[{"id":1,"name":"test","status":"in_progress"}]}`)
	if w := postJSON(t, srv, path, testToken, `{}`); w.Code != http.StatusConflict {
		t.Fatalf("merging a running CI: %d %s", w.Code, w.Body.String())
	}
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs", `{"check_runs":[{"id":1,"name":"test","status":"completed","conclusion":"success"}]}`)
	host.set("PUT https://api.github.com/repos/o/r/pulls/7/merge", `{"merged":true}`)
	if w := postJSON(t, srv, path, testToken, `{}`); w.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", w.Code, w.Body.String())
	}
	if v := c.prWatchView(); v.Status != prMerged {
		t.Errorf("merged: %+v", v)
	}
}

func TestPRWatchEndsWhenThereIsNoPullRequestAndNoRun(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	if !srv.pollPR(c, &prWatchState{status: prFollowing}) {
		t.Fatal("with nothing to follow and nobody opening one, the watch is over")
	}
	if c.prWatchView() != nil {
		t.Error("nothing is left to report")
	}
}

func TestPRWatchSurvivesARestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id":        "s-pr",
		"title":     "a session with a pull request",
		"created":   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{},
		"pr_watch":  map[string]any{"status": "gave_up", "attempts": 5, "number": 7, "fixed_key": "abc"},
	})
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})
	c, ok := srv.lookup("s-pr")
	if !ok {
		t.Fatal("the session must be back")
	}
	if v := c.status().PRWatch; v == nil || v.Status != prGaveUp || v.Attempts != 5 || v.Number != 7 {
		t.Fatalf("the watch is restored as it was: %+v", v)
	}
	// And it is saved again, so the next restart finds it too.
	if err := srv.store.save(c); err != nil {
		t.Fatal(err)
	}
	recs, _ := srv.store.loadAll()
	for _, r := range recs {
		if r.ID == "s-pr" && (r.PRWatch == nil || r.PRWatch.FixedKey != "abc") {
			t.Errorf("saved record: %+v", r.PRWatch)
		}
	}
}

func TestPRWatchStopsWhenTheBaseBranchIsRedToo(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open",
		`[{"number":7,"html_url":"https://github.com/o/r/pull/7","title":"feat: x","head":{"ref":"`+sessionBranch(ss.ID)+`"},"base":{"ref":"main"}}]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7", `{"head":{"sha":"abc"}}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc/status", `{"statuses":[]}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs", `{"check_runs":[{"id":1,"name":"test","status":"completed","conclusion":"failure"}]}`)
	host.set("GET https://api.github.com/repos/o/r/commits/main/status", `{"statuses":[]}`)

	// Red on main for another job: this change's failure is its own.
	host.set("GET https://api.github.com/repos/o/r/commits/main/check-runs", `{"check_runs":[{"id":2,"name":"lint","status":"completed","conclusion":"failure"}]}`)
	w := &prWatchState{status: prFollowing}
	if srv.pollPR(c, w) || w.status != prFixing || w.attempts != 1 {
		t.Fatalf("a failure of its own is fixed: done=%v %s %d", false, w.status, w.attempts)
	}

	// Red on main for the SAME job: not fixable from here, and the attempt is not spent.
	w = &prWatchState{status: prFollowing}
	host.set("GET https://api.github.com/repos/o/r/commits/main/check-runs", `{"check_runs":[{"id":2,"name":"test","status":"completed","conclusion":"failure"}]}`)
	if !srv.pollPR(c, w) || w.status != prBaseRed || w.attempts != 0 {
		t.Fatalf("a failure the base has too ends the watch: %s %d", w.status, w.attempts)
	}
}

func TestProjectMergeMethodIsValidated(t *testing.T) {
	srv, _ := gitServer(t)
	withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	patch := func(body string) int {
		req, _ := http.NewRequest(http.MethodPatch, srv.BaseURL()+"/v1/projects/"+pid, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w.Code
	}
	if code := patch(`{"merge_method":"squash"}`); code != http.StatusOK {
		t.Errorf("squash: %d", code)
	}
	if p := srv.projectOf(pid); p.MergeMethod != "squash" {
		t.Errorf("saved: %q", p.MergeMethod)
	}
	if code := patch(`{"merge_method":"fast-forward-ish"}`); code != http.StatusBadRequest {
		t.Errorf("an unknown method: %d", code)
	}
}

// prHost arms the fake host with an open pull request #7 whose CI is `conclusion`.
func prHost(host *gitHost, branch, conclusion, mergeState string) {
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open",
		`[{"number":7,"html_url":"https://github.com/o/r/pull/7","title":"feat: x","head":{"ref":"`+branch+`"},"base":{"ref":"main"}}]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7", `{"head":{"sha":"abc"},"mergeable_state":"`+mergeState+`"}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc/status", `{"statuses":[]}`)
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs", `{"check_runs":[{"id":1,"name":"test","status":"completed","conclusion":"`+conclusion+`"}]}`)
	host.set("PUT https://api.github.com/repos/o/r/pulls/7/merge", `{"merged":true,"sha":"cafe01"}`)
}

func patchProjectCode(t *testing.T, srv *Server, pid, body string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, srv.BaseURL()+"/v1/projects/"+pid, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w.Code
}

func TestPRMergeIsRefusedWhenTheHostWouldRefuseIt(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	prHost(host, sessionBranch(ss.ID), "success", "blocked")
	if m := decode(t, get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken)); m["merge"].(map[string]any)["code"] != "blocked" {
		t.Fatalf("the view says why it cannot be merged: %v", m)
	}
	w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr/merge"), testToken, `{}`)
	if m := decode(t, w); w.Code != http.StatusConflict || m["code"] != "pr_blocked" {
		t.Fatalf("a blocked merge: %d %s", w.Code, w.Body.String())
	}
	if c := srv.sessions[ss.ID]; c.merged {
		t.Error("nothing was merged")
	}
}

func TestPRMergeMakesTheSessionIntegrated(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	if w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr/merge"), testToken, `{}`); w.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", w.Code, w.Body.String())
	}
	st := srv.sessions[ss.ID].status()
	if !st.Merged || st.MergedSHA != "cafe01" {
		t.Errorf("the session is the integrated one, with the host's commit: %+v", st)
	}
}

func TestPRAutoMergeIsAProjectChoice(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	prHost(host, sessionBranch(ss.ID), "success", "clean")

	if srv.pollPR(c, &prWatchState{status: prFollowing}) || c.merged {
		t.Fatal("without the setting a pass merges nothing and the watch goes on")
	}
	if patchProjectCode(t, srv, ss.ProjectID, `{"auto_merge":true}`) != http.StatusOK {
		t.Fatal("setting auto_merge")
	}
	prHost(host, sessionBranch(ss.ID), "success", "blocked")
	if srv.pollPR(c, &prWatchState{status: prFollowing}) || c.merged {
		t.Fatal("a host that would refuse is left to the user")
	}
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	if !srv.pollPR(c, &prWatchState{status: prFollowing}) || !c.merged {
		t.Fatal("with the setting and a mergeable pull request, it is merged")
	}
	if v := c.prWatchView(); v.Status != prMerged {
		t.Errorf("merged is what the watch ends on: %+v", v)
	}
}

func TestPRAttemptsAreAProjectChoice(t *testing.T) {
	srv, _, ss := prSession(t, "https://github.com/o/r.git")
	c := srv.sessions[ss.ID]
	if got := srv.prMaxFixes(c); got != defaultPRMaxFixes {
		t.Fatalf("default: %d", got)
	}
	if patchProjectCode(t, srv, ss.ProjectID, `{"pr_max_fixes":2}`) != http.StatusOK || srv.prMaxFixes(c) != 2 {
		t.Fatalf("the project's own limit: %d", srv.prMaxFixes(c))
	}
	for _, bad := range []string{`{"pr_max_fixes":-1}`, `{"pr_max_fixes":21}`} {
		if code := patchProjectCode(t, srv, ss.ProjectID, bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, code)
		}
	}
	if patchProjectCode(t, srv, ss.ProjectID, `{"pr_max_fixes":0}`) != http.StatusOK || srv.prMaxFixes(c) != defaultPRMaxFixes {
		t.Error("zero goes back to the default")
	}
}

func TestPRMergedSomewhereElseIsNoticed(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	w := &prWatchState{status: prFollowing}
	if srv.pollPR(c, w) || w.status != prPassed {
		t.Fatalf("passed, and still watched: %s", w.status)
	}

	// Still open: nothing to say.
	if srv.pollPR(c, w) {
		t.Fatal("an open pull request is watched")
	}

	// Gone from the open ones, and the host says it was merged.
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7", `{"state":"closed","merged":true,"merge_commit_sha":"feed42"}`)
	if !srv.pollPR(c, w) {
		t.Fatal("a merged pull request ends the watch")
	}
	if st := c.status(); !st.Merged || st.MergedSHA != "feed42" || st.PRWatch.Status != prMerged {
		t.Fatalf("merged elsewhere is merged here: %+v", st)
	}
}

func TestPRClosedWithoutMergeIsNoticed(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	w := &prWatchState{status: prFollowing}
	srv.pollPR(c, w)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7", `{"state":"closed","merged":false}`)
	if !srv.pollPR(c, w) || c.merged || c.prWatchView().Status != prClosed {
		t.Fatalf("closed: merged=%v %+v", c.merged, c.prWatchView())
	}
}

func TestMergeBringsTheProjectUpToDate(t *testing.T) {
	srv, _, ss := prSession(t, "")
	c := srv.sessions[ss.ID]
	p := srv.projectOf(ss.ProjectID)
	base := gitx.Display(context.Background(), p.Dir)

	bare := t.TempDir()
	mustRun(t, "git", "init", "-q", "--bare", bare)
	mustRun(t, "git", "-C", p.Dir, "remote", "add", "origin", bare)
	mustRun(t, "git", "-C", p.Dir, "push", "-q", "origin", base)

	// Somebody merges something on the host.
	other := t.TempDir()
	mustRun(t, "git", "clone", "-q", "-b", base, bare, other)
	if err := os.WriteFile(filepath.Join(other, "merged.txt"), []byte("from the host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", other, "add", ".")
	mustRun(t, "git", "-C", other, "-c", "user.email=a@b.c", "-c", "user.name=a", "commit", "-qm", "merged on the host")
	mustRun(t, "git", "-C", other, "push", "-q", "origin", "HEAD:"+base)

	// Uncommitted work in the project is never touched.
	if err := os.WriteFile(filepath.Join(p.Dir, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.finishMerge(context.Background(), c, 7, "x", base)
	if _, err := os.Stat(filepath.Join(p.Dir, "merged.txt")); err == nil {
		t.Fatal("a checkout with uncommitted changes is left alone")
	}

	if err := os.Remove(filepath.Join(p.Dir, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	srv.syncProject(context.Background(), c, base)
	if _, err := os.Stat(filepath.Join(p.Dir, "merged.txt")); err != nil {
		t.Errorf("a clean checkout on the merged branch is brought up to date: %v", err)
	}
}

// The whole loop through the real goroutine: the CI fails, the agent is sent, the CI passes, and
// somebody merges the pull request elsewhere.
func TestPRWatchRunsByItself(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	srv.opts.PRPollEvery = 10 * time.Millisecond
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	prHost(host, sessionBranch(ss.ID), "failure", "clean")

	if w := postJSON(t, srv, sessionPath(srv, ss.ID, "/pr/fix"), testToken, `{}`); w.Code != http.StatusAccepted {
		t.Fatalf("starting: %d %s", w.Code, w.Body.String())
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for i := 0; i < 400; i++ {
			if ok() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s: %+v", what, c.prWatchView())
	}
	waitFor("the failure to be answered", func() bool { v := c.prWatchView(); return v != nil && v.Status == prFixing && v.Attempts == 1 })

	prHost(host, sessionBranch(ss.ID), "success", "clean")
	waitFor("the pass", func() bool { v := c.prWatchView(); return v != nil && v.Status == prPassed })

	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7", `{"state":"closed","merged":true,"merge_commit_sha":"beef"}`)
	waitFor("the merge to be noticed", func() bool { return c.status().Merged })
	if v := c.prWatchView(); v.Status != prMerged {
		t.Errorf("%+v", v)
	}
}

func TestPRWatchPaceFollowsTheHost(t *testing.T) {
	every := 20 * time.Second
	w := &prWatchState{status: prFollowing}
	if got := w.wait(every); got != every {
		t.Fatalf("a CI that is moving is asked about at the base pace: %v", got)
	}
	run := ci(gitforge.StatePending, "a")
	for i := 0; i < 4; i++ {
		w.see(run)
	}
	if got := w.wait(every); got <= every || got > every*prMaxQuiet {
		t.Fatalf("a CI that has not moved is asked about less often, within bounds: %v", got)
	}
	for i := 0; i < 100; i++ {
		w.see(run)
	}
	if got := w.wait(every); got != every*prMaxQuiet {
		t.Fatalf("the slowdown is capped: %v", got)
	}
	w.see(ci(gitforge.StateFailure, "b"))
	if got := w.wait(every); got != every {
		t.Fatalf("a reading that changed brings the base pace back: %v", got)
	}
	w.errors = 1
	first := w.wait(every)
	w.errors = 3
	third := w.wait(every)
	w.errors = 50
	capped := w.wait(every)
	if !(first > every && third > first && capped == every*prMaxBackoff) {
		t.Fatalf("a failing host is backed off from, up to a cap: %v %v %v", first, third, capped)
	}
	w = &prWatchState{status: prPassed}
	if got := w.wait(every); got != every*3 {
		t.Fatalf("a pull request waiting to be merged: %v", got)
	}
}

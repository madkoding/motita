package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/logx"
)

// The failures of the pull request endpoints and of the watch: a host that errors, a login that is
// refused, a session that has nothing to propose. Each is a different thing for the user to be told.

const (
	ghPulls = "GET https://api.github.com/repos/o/r/pulls?state=open"
	ghPull7 = "GET https://api.github.com/repos/o/r/pulls/7"
	ghMerge = "PUT https://api.github.com/repos/o/r/pulls/7/merge"
	boom    = `STATUS 500 {"message":"boom"}`
)

func connected(t *testing.T, opts ...func(*Options)) (*Server, *gitHost, SessionStatus, *conversation) {
	t.Helper()
	srv, host, ss := prSession(t, "https://github.com/o/r.git", opts...)
	connectGitHub(t, srv)
	return srv, host, ss, srv.sessions[ss.ID]
}

func logTo(t *testing.T) *logx.Logger {
	t.Helper()
	log, err := logx.New(logx.Options{Path: filepath.Join(t.TempDir(), "gateway.log"), Level: logx.Info})
	if err != nil {
		t.Fatal(err)
	}
	return log
}

func TestReadingThePRTellsHostFailuresApart(t *testing.T) {
	srv, host, ss, c := connected(t)
	path := sessionPath(srv, ss.ID, "/pr")

	host.set(ghPulls, boom)
	if w := get(t, srv, path, testToken); w.Code != http.StatusBadGateway {
		t.Errorf("a host that errors: %d %s", w.Code, w.Body.String())
	}
	host.set(ghPulls, `STATUS 401 {"message":"Bad credentials"}`)
	if w := get(t, srv, path, testToken); w.Code != http.StatusConflict || decode(t, w)["code"] != errGitAuthRequired || decode(t, w)["service"] != "github" {
		t.Errorf("a login the host refuses is a login to make again: %d %s", w.Code, w.Body.String())
	}

	prHost(host, sessionBranch(ss.ID), "success", "clean")
	host.set(ghPull7, boom)
	if w := get(t, srv, path, testToken); w.Code != http.StatusBadGateway {
		t.Errorf("a CI that cannot be read: %d %s", w.Code, w.Body.String())
	}

	// A CI seen running is a CI to follow, whoever asked.
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs", `{"check_runs":[{"id":1,"name":"t","status":"in_progress"}]}`)
	if w := get(t, srv, path, testToken); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if v := c.prWatchView(); v == nil || v.Status != prFollowing {
		t.Errorf("a running CI is followed: %+v", v)
	}
	srv.startPRWatch(c, nil) // already followed: nothing new starts
}

func TestThePRNeedsAWayToTalkToTheHost(t *testing.T) {
	// No directory for logins at all.
	srv := newTestServer(t, &fakeService{})
	withProjectsAndFake(t, srv, t.TempDir())
	pid := makeProject(t, srv, "repo")
	w := post(t, srv, "/v1/sessions", `{"project_id":"`+pid+`"}`, testToken)
	var ss SessionStatus
	_ = json_(w.Body.Bytes(), &ss)
	if w := get(t, srv, sessionPath(srv, ss.ID, "/pr"), testToken); w.Code != http.StatusNotImplemented {
		t.Errorf("no store: %d %s", w.Code, w.Body.String())
	}

	// An origin that is not an address.
	srv2, _, ss2 := prSession(t, "nonsense")
	if w := get(t, srv2, sessionPath(srv2, ss2.ID, "/pr"), testToken); w.Code != http.StatusBadGateway {
		t.Errorf("an origin nobody can read: %d %s", w.Code, w.Body.String())
	}

	// A saved login that cannot be read.
	srv3, _, ss3 := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv3)
	corrupt(t, srv3.opts.GitAuthDir, "git-github.json")
	if w := get(t, srv3, sessionPath(srv3, ss3.ID, "/pr"), testToken); w.Code != http.StatusInternalServerError {
		t.Errorf("a login that cannot be read: %d %s", w.Code, w.Body.String())
	}
}

func TestOpeningAndFixingAPRRefusals(t *testing.T) {
	srv, host, ss, c := connected(t)
	host.set(ghPulls, `[]`)
	create, fix := sessionPath(srv, ss.ID, "/pr"), sessionPath(srv, ss.ID, "/pr/fix")

	// The agent is asked, and the gateway starts following.
	if err := putWork(ss.Workspace, "work.txt"); err != nil {
		t.Fatal(err)
	}
	if w := postJSON(t, srv, create, testToken, `{}`); w.Code != http.StatusOK {
		t.Fatalf("opening: %d %s", w.Code, w.Body.String())
	}
	if c.prWatchView() == nil {
		t.Error("from here the CI is followed")
	}

	host.set(ghPulls, boom)
	if w := postJSON(t, srv, fix, testToken, `{}`); w.Code != http.StatusBadGateway {
		t.Errorf("fixing, host down: %d %s", w.Code, w.Body.String())
	}

	// An integrated session is read-only.
	c.setMerged("abc")
	for _, p := range []string{create, fix} {
		if w := postJSON(t, srv, p, testToken, `{}`); w.Code != http.StatusConflict {
			t.Errorf("%s on an integrated session: %d", p, w.Code)
		}
	}
}

func TestOpeningAndFixingWithNowhereToOpenIt(t *testing.T) {
	srv, _, ss := prSession(t, "")
	for _, p := range []string{"/pr", "/pr/fix", "/pr/merge"} {
		w := postJSON(t, srv, sessionPath(srv, ss.ID, p), testToken, `{}`)
		if m := decode(t, w); w.Code != http.StatusConflict || m["code"] != errPRNoRemote {
			t.Errorf("%s: %d %s", p, w.Code, w.Body.String())
		}
	}
}

func TestAFixAskedWhileTheAgentIsBusyWaitsItsTurn(t *testing.T) {
	srv, host, ss, c := connected(t)
	prHost(host, sessionBranch(ss.ID), "failure", "clean")
	release := make(chan struct{})
	c.svc.(*fakeService).task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "done", nil
	}
	t.Cleanup(func() { close(release) })
	fix := sessionPath(srv, ss.ID, "/pr/fix")
	if m := decode(t, postJSON(t, srv, fix, testToken, `{}`)); m["started"] != true {
		t.Fatalf("the first one starts the agent: %v", m)
	}
	if m := decode(t, postJSON(t, srv, fix, testToken, `{}`)); m["queued"] != true || m["started"] != false {
		t.Fatalf("the second one waits behind it: %v", m)
	}
}

func TestThePollOfAWatchSurvivesAndGivesUpOnAHost(t *testing.T) {
	srv, host, ss, c := connected(t)
	w := &prWatchState{status: prFollowing}

	// An integrated session has nothing left to follow, but a merge that was just made is kept.
	c.setMerged("abc")
	c.setPRWatch(&PRWatchView{Status: prMerged, Number: 7})
	if !srv.pollPR(c, w) || c.prWatchView() == nil {
		t.Fatal("a merged watch is kept")
	}
	c.setPRWatch(&PRWatchView{Status: prFollowing})
	if !srv.pollPR(c, w) || c.prWatchView() != nil {
		t.Fatal("any other watch of an integrated session is dropped")
	}
	c.stateMu.Lock()
	c.merged = false
	c.stateMu.Unlock()

	// Hosts that fail are waited out, ten times, and then given up on.
	host.set(ghPulls, boom)
	for i := 1; i < prErrorLimit; i++ {
		if srv.pollPR(c, w) || w.errors != i {
			t.Fatalf("poll %d: the host's failure is counted, and not the end: %d", i, w.errors)
		}
	}
	if !srv.pollPR(c, w) || c.prWatchView() != nil {
		t.Fatal("a host that keeps failing ends the watch")
	}

	// A CI that cannot be read is the same.
	w = &prWatchState{status: prFollowing}
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	host.set(ghPull7, boom)
	if srv.pollPR(c, w) || w.errors != 1 {
		t.Fatalf("a CI that cannot be read: %d", w.errors)
	}
}

func TestThePollWaitsForAnAgentThatIsOpeningThePR(t *testing.T) {
	srv, host, _, c := connected(t)
	host.set(ghPulls, `[]`)
	c.stateMu.Lock()
	c.running = true
	c.stateMu.Unlock()
	if srv.pollPR(c, &prWatchState{status: prFollowing}) {
		t.Error("while the agent is working there may still be a pull request to come")
	}
}

func TestThePollWithNoOriginCountsAsAFailure(t *testing.T) {
	srv, _, ss := prSession(t, "")
	c := srv.sessions[ss.ID]
	w := &prWatchState{status: prFollowing}
	if srv.pollPR(c, w) || w.errors != 1 {
		t.Errorf("%d", w.errors)
	}
}

func TestThePollEndsWhenThereIsNothingMoreToWaitFor(t *testing.T) {
	srv, host, ss, c := connected(t, func(o *Options) { o.PRMaxFixes = 1 })
	if srv.prMaxFixes(c) != 1 {
		t.Fatal("the gateway's own limit")
	}
	prHost(host, sessionBranch(ss.ID), "failure", "clean")
	w := &prWatchState{status: prFixing, attempts: 1}
	if !srv.pollPR(c, w) || w.status != prGaveUp {
		t.Errorf("out of attempts: %s", w.status)
	}

	prHost(host, sessionBranch(ss.ID), "success", "clean")
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs", `{"check_runs":[]}`)
	w = &prWatchState{status: prFollowing, noneCount: prNoneLimit - 1}
	if !srv.pollPR(c, w) || w.status != prNoCI {
		t.Errorf("no CI at all: %s", w.status)
	}
}

func TestAPassedPRIsWatchedUntilItsFateIsKnown(t *testing.T) {
	srv, host, _, c := connected(t)
	w := &prWatchState{status: prPassed}

	host.set(ghPulls, boom)
	if srv.pollPR(c, w) || w.errors != 1 {
		t.Fatal("a host that fails")
	}
	host.set(ghPulls, `[]`)
	if !srv.pollPR(c, &prWatchState{status: prPassed}) {
		t.Fatal("with no number to ask about there is nothing to watch")
	}
	c.setPRWatch(&PRWatchView{Status: prPassed, Number: 7})
	host.set(ghPull7, boom)
	if srv.pollPR(c, &prWatchState{status: prPassed}) {
		t.Fatal("a fate that cannot be read yet is asked about again")
	}
	host.set(ghPull7, `{"state":"open"}`)
	if srv.pollPR(c, &prWatchState{status: prPassed}) {
		t.Fatal("still open, still watched")
	}
}

func TestMergingTellsWhyItCannot(t *testing.T) {
	srv, host, ss, _ := connected(t)
	merge := sessionPath(srv, ss.ID, "/pr/merge")
	status := func() (int, map[string]any) {
		w := postJSON(t, srv, merge, testToken, `{}`)
		return w.Code, decode(t, w)
	}

	host.set(ghPulls, boom)
	if code, _ := status(); code != http.StatusBadGateway {
		t.Errorf("host down: %d", code)
	}
	host.set(ghPulls, `[]`)
	if code, _ := status(); code != http.StatusConflict {
		t.Errorf("no pull request: %d", code)
	}
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	host.set(ghPull7, boom)
	if code, _ := status(); code != http.StatusBadGateway {
		t.Errorf("a CI that cannot be read: %d", code)
	}
	for state, want := range map[string]string{"dirty": "pr_conflict", "draft": "pr_draft", "blocked": "pr_blocked"} {
		prHost(host, sessionBranch(ss.ID), "success", state)
		if code, m := status(); code != http.StatusConflict || m["code"] != want || !strings.Contains(m["error"].(string), "host would not merge") {
			t.Errorf("%s: %d %v", state, code, m)
		}
	}
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	host.set(ghMerge, boom)
	if code, _ := status(); code != http.StatusBadGateway {
		t.Errorf("the host refuses the merge: %d", code)
	}
}

func TestAutomaticMergeAndContinuationSayWhenTheyFail(t *testing.T) {
	for _, withLog := range []bool{false, true} {
		var opts []func(*Options)
		if withLog {
			log := logTo(t)
			opts = append(opts, func(o *Options) { o.Log = log })
		}
		srv, host, ss, c := connected(t, opts...)
		if patchProjectCode(t, srv, ss.ProjectID, `{"auto_merge":true,"auto_continue":true}`) != http.StatusOK {
			t.Fatal("settings")
		}
		prHost(host, sessionBranch(ss.ID), "success", "clean")
		host.set(ghMerge, boom)
		if srv.pollPR(c, &prWatchState{status: prFollowing}) || c.merged {
			t.Fatalf("log=%v: a merge the host refuses leaves the pull request to the user", withLog)
		}

		// Merged, but with nowhere to start the next session from: said, not hidden.
		host.set(ghMerge, `{"merged":true,"sha":"cafe"}`)
		srv.pollPR(c, &prWatchState{status: prFollowing})
		if !c.merged || c.prWatchView().Next != "" {
			t.Fatalf("log=%v: merged, and no next session: %+v", withLog, c.prWatchView())
		}
	}
}

func TestSyncingTheProjectIsBestEffort(t *testing.T) {
	for _, withLog := range []bool{false, true} {
		var opts []func(*Options)
		if withLog {
			log := logTo(t)
			opts = append(opts, func(o *Options) { o.Log = log })
		}
		srv, _, ss, c := connected(t, opts...)
		srv.syncProject(context.Background(), newConversation("nobody", &fakeService{}), "main")

		// A checkout whose origin cannot be reached: the merge stands, and the failure is logged.
		p := srv.projectOf(ss.ProjectID)
		mustRun(t, "git", "-C", p.Dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))
		srv.syncProject(context.Background(), c, gitxDisplay(p.Dir))
	}
}

func TestAWatchFollowingWhenTheGatewayStoppedIsFollowedAgain(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessionFile(t, dir, map[string]any{
		"id":        "s-follow",
		"title":     "following a CI",
		"created":   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"last_used": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"turns":     []map[string]string{},
		"pr_watch":  map[string]any{"status": "fixing", "attempts": 2, "number": 7, "fixed_key": "abc"},
	})
	srv, _ := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   dir,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
		NewService:   func() (Service, error) { return &fakeService{}, nil },
	})
	srv.prMu.Lock()
	following := srv.prWatching["s-follow"]
	srv.prMu.Unlock()
	if !following {
		t.Error("a CI being fixed when the gateway stopped is followed again")
	}
}

func json_(data []byte, v any) error { return json.Unmarshal(data, v) }

func putWork(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte("work\n"), 0o644)
}

func gitxDisplay(dir string) string { return gitx.Display(context.Background(), dir) }

// Bringing the project up to date after a merge runs git as the user, outside any sandbox, in a
// checkout an agent worked in: a hook or an fsmonitor it planted in .git must not run.
func TestSyncingTheProjectRunsNoRepositoryCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the planted commands are shell scripts")
	}
	srv, _, ss, c := connected(t)
	p := srv.projectOf(ss.ProjectID)
	base := gitxDisplay(p.Dir)

	// An origin one commit ahead of the checkout, so the fast-forward has something to do.
	origin := filepath.Join(t.TempDir(), "origin.git")
	mustRun(t, "git", "clone", "-q", "--bare", p.Dir, origin)
	ahead := filepath.Join(t.TempDir(), "ahead")
	mustRun(t, "git", "clone", "-q", origin, ahead)
	if err := putWork(ahead, "ahead.txt"); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", ahead, "add", "-A")
	mustRun(t, "git", "-C", ahead, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "ahead")
	mustRun(t, "git", "-C", ahead, "push", "-q", "origin", "HEAD:"+base)
	mustRun(t, "git", "-C", p.Dir, "remote", "set-url", "origin", origin)

	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\necho \"$0\" >> " + marker + "\n"
	for _, hook := range []string{"post-merge", "post-checkout", "reference-transaction"} {
		path := filepath.Join(p.Dir, ".git", "hooks", hook)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	srv.syncProject(context.Background(), c, base)
	if _, err := os.Stat(filepath.Join(p.Dir, "ahead.txt")); err != nil {
		t.Fatalf("the project was not brought up to date: %v", err)
	}
	if data, err := os.ReadFile(marker); err == nil {
		t.Errorf("a planted hook ran: %s", data)
	}
}

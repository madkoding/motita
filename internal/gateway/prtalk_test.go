package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
)

// sentTasks records what the agent of a session is sent, and lets a test wait for it.
type sentTasks struct {
	mu    sync.Mutex
	tasks []string
}

func recordTasks(t *testing.T, c *conversation) *sentTasks {
	t.Helper()
	fs, ok := c.svc.(*fakeService)
	if !ok {
		t.Fatalf("the session's service is %T", c.svc)
	}
	st := &sentTasks{}
	fs.task = func(_ context.Context, task string, _ func(string, ...any)) (string, error) {
		st.mu.Lock()
		st.tasks = append(st.tasks, task)
		st.mu.Unlock()
		return "done", nil
	}
	return st
}

func (st *sentTasks) wait(t *testing.T, c *conversation, n int) []string {
	t.Helper()
	for i := 0; i < 400; i++ {
		st.mu.Lock()
		got := append([]string(nil), st.tasks...)
		st.mu.Unlock()
		if len(got) >= n && !c.isRunning() {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the agent was not sent %d tasks", n)
	return nil
}

const comments = "GET https://api.github.com/repos/o/r/issues/7/comments"

func talkHost(host *gitHost, branch, conclusion, mergeState string) {
	prHost(host, branch, conclusion, mergeState)
	host.set(comments, `[{"id":1,"body":"from before","user":{"login":"a"}}]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7/reviews", `[]`)
	host.set("GET https://api.github.com/repos/o/r/pulls/7/comments", `[]`)
}

func TestPRWatchSendsTheAgentToANewReview(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	sent := recordTasks(t, c)
	talkHost(host, sessionBranch(ss.ID), "success", "clean")

	w := &prWatchState{status: prFollowing}
	if srv.pollPR(c, w) || w.status != prPassed {
		t.Fatalf("passed: %s", w.status)
	}
	// The comment from before the watch is the baseline, not news.
	if srv.pollPR(c, w) || w.status != prPassed || w.attempts != 0 {
		t.Fatalf("baseline: %s %d", w.status, w.attempts)
	}
	host.set(comments, `[{"id":1,"body":"from before"},{"id":2,"body":"please rename x","user":{"login":"bo"}}]`)
	if srv.pollPR(c, w) {
		t.Fatal("a review is not the end of the watch")
	}
	if v := c.prWatchView(); v.Status != prReviewing || v.Attempts != 1 {
		t.Fatalf("reviewing: %+v", v)
	}
	tasks := sent.wait(t, c, 1)
	if !strings.Contains(tasks[0], "please rename x") || !strings.Contains(tasks[0], "@bo") ||
		strings.Contains(tasks[0], "from before") {
		t.Errorf("task = %s", tasks[0])
	}
	// Once the agent is done the CI of its push is read again; nothing new is said twice.
	if srv.pollPR(c, w) || w.status != prPassed {
		t.Fatalf("after the answer: %s", w.status)
	}
}

func TestPRWatchSendsTheAgentToAConflict(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	sent := recordTasks(t, c)
	talkHost(host, sessionBranch(ss.ID), "pending", "dirty")
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs",
		`{"check_runs":[{"id":1,"name":"test","status":"in_progress"}]}`)

	w := &prWatchState{status: prFollowing}
	if srv.pollPR(c, w) || w.status != prResolving || w.attempts != 1 {
		t.Fatalf("resolving: %s %d", w.status, w.attempts)
	}
	tasks := sent.wait(t, c, 1)
	if !strings.Contains(tasks[0], "conflicts with main") || !strings.Contains(tasks[0], "Do not rebase") {
		t.Errorf("task = %s", tasks[0])
	}
	// While the agent is at it, the watch waits for it.
	c.takeRunSlot()
	if srv.pollPR(c, w) || w.status != prResolving {
		t.Fatalf("waiting: %s", w.status)
	}
	c.releaseRunSlot()
	// The same conflict is not handed over twice.
	if srv.pollPR(c, w) || w.attempts != 1 || w.status != prFollowing {
		t.Fatalf("again: %s %d", w.status, w.attempts)
	}
}

func TestPRWatchTalkSharesTheAttempts(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	talkHost(host, sessionBranch(ss.ID), "success", "dirty")
	max := srv.prMaxFixes(c)
	w := &prWatchState{status: prFollowing, attempts: max}
	// A session whose agent is busy is not read for talk at all.
	tg, perr := srv.prTargetOf(context.Background(), c, "")
	if perr != nil {
		t.Fatal(perr.msg)
	}
	c.takeRunSlot()
	if srv.pollTalk(context.Background(), c, w, tg, gitforge.PullRequest{Number: 7}) || w.status != prFollowing {
		t.Fatalf("busy: %s", w.status)
	}
	c.releaseRunSlot()
	// The pass comes first; the conflict is read while it waits to be merged.
	if srv.pollPR(c, w) || w.status != prPassed {
		t.Fatalf("passed: %s", w.status)
	}
	if !srv.pollPR(c, w) || c.prWatchView().Status != prGaveUp {
		t.Fatalf("out of attempts: %+v", c.prWatchView())
	}
}

func TestConflictTaskWithoutABase(t *testing.T) {
	task := conflictTask("b", gitforge.PullRequest{Number: 3, URL: "u"})
	if !strings.Contains(task, "conflicts with its base branch") {
		t.Errorf("task = %s", task)
	}
	task = reviewTask("b", gitforge.PullRequest{Number: 3, URL: "u"},
		[]gitforge.Comment{{Kind: gitforge.CommentLine, Path: "a.go", Body: "x\ny"},
			{Kind: gitforge.CommentReview, State: "changes_requested", Body: "z"}})
	if !strings.Contains(task, "[line on a.go] x\n  y") || !strings.Contains(task, "[review changes_requested] z") {
		t.Errorf("task = %s", task)
	}
}

func TestARunThatOpenedAPRStartsTheWatch(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	srv.opts.PRPollEvery = time.Hour
	c := srv.sessions[ss.ID]

	// Not connected: nothing is followed.
	srv.followOpenedPR(c)
	if c.prWatchView() != nil {
		t.Fatal("no host, no watch")
	}
	connectGitHub(t, srv)
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", `[]`)
	srv.followOpenedPR(c)
	if c.prWatchView() != nil {
		t.Fatal("no pull request, no watch")
	}
	host.set("GET https://api.github.com/repos/o/r/pulls?state=open", "STATUS 500 {}")
	srv.followOpenedPR(c)
	if c.prWatchView() != nil {
		t.Fatal("an unreadable host, no watch")
	}
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	srv.followOpenedPR(c)
	if v := c.prWatchView(); v == nil || v.Status != prFollowing || v.Number != 7 {
		t.Fatalf("followed: %+v", v)
	}
	// A watch already there is left as it is.
	c.setPRWatch(&PRWatchView{Status: prPassed, Number: 7})
	srv.followOpenedPR(c)
	if c.prWatchView().Status != prPassed {
		t.Error("an existing watch is kept")
	}
}

func TestARunThatEndsLooksForAPR(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git")
	srv.opts.PRPollEvery = time.Hour
	connectGitHub(t, srv)
	prHost(host, sessionBranch(ss.ID), "success", "clean")
	c := srv.sessions[ss.ID]
	recordTasks(t, c)
	if _, ok := srv.startDetachedRun(c, "open a pull request", "task", "", srv.approverFactory(c)); !ok {
		t.Fatal("the run did not start")
	}
	for i := 0; i < 400 && c.prWatchView() == nil; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if v := c.prWatchView(); v == nil || v.Number != 7 {
		t.Fatalf("the pull request the run left is followed: %+v", v)
	}
}

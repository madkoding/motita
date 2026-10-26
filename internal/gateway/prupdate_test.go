package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
)

const updateBranchRoute = "PUT https://api.github.com/repos/o/r/pulls/7/update-branch"

func updates(host *gitHost) int {
	host.mu.Lock()
	defer host.mu.Unlock()
	n := 0
	for _, k := range host.seen {
		if strings.HasPrefix(k, updateBranchRoute) {
			n++
		}
	}
	return n
}

func TestPRBehindBaseIsUpdatedWhenTheProjectAsks(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git", func(o *Options) { o.Log = logTo(t) })
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	talkHost(host, sessionBranch(ss.ID), "success", "behind")
	host.set(updateBranchRoute, `{"message":"Updating pull request branch."}`)

	w := &prWatchState{status: prFollowing}
	if srv.pollPR(c, w) || w.status != prPassed {
		t.Fatalf("passed: %s", w.status)
	}
	if srv.pollPR(c, w) || updates(host) != 0 {
		t.Fatalf("without the setting a branch that is behind is left alone: %d", updates(host))
	}

	if patchProjectCode(t, srv, ss.ProjectID, `{"auto_update_pr":true}`) != http.StatusOK {
		t.Fatal("setting auto_update_pr")
	}
	if srv.pollPR(c, w) || updates(host) != 1 {
		t.Fatalf("with the setting the host is asked to update the branch: %d", updates(host))
	}
	if w.status != prFollowing || c.prWatchView().Status != prFollowing {
		t.Errorf("the CI of the updated branch is followed again: %s", w.status)
	}
	// Still behind a moment later: not asked again at every poll.
	w.status = prPassed
	if srv.pollPR(c, w) || updates(host) != 1 {
		t.Fatalf("asked again too soon: %d", updates(host))
	}
	w.updatedAt = time.Now().Add(-2 * prUpdateEvery)
	if srv.pollPR(c, w) || updates(host) != 2 {
		t.Fatalf("asked again after a while: %d", updates(host))
	}
}

func TestPRUpdateRefusedByTheHostChangesNothing(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git", func(o *Options) { o.Log = logTo(t) })
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	if patchProjectCode(t, srv, ss.ProjectID, `{"auto_update_pr":true}`) != http.StatusOK {
		t.Fatal("setting auto_update_pr")
	}
	talkHost(host, sessionBranch(ss.ID), "success", "behind")
	// No route: the host answers 404, as it does for a branch it cannot update.
	w := &prWatchState{status: prPassed}
	if srv.pollPR(c, w) || w.status != prPassed || updates(host) != 1 {
		t.Fatalf("a refusal leaves the watch as it was: %s %d", w.status, updates(host))
	}
}

func TestPRUpdateNeedsAHostThatCanMergeTheBaseIn(t *testing.T) {
	srv, _, ss := prSession(t, "https://github.com/o/r.git", func(o *Options) { o.Log = logTo(t) })
	c := srv.sessions[ss.ID]
	if patchProjectCode(t, srv, ss.ProjectID, `{"auto_update_pr":true}`) != http.StatusOK {
		t.Fatal("setting auto_update_pr")
	}
	// GitLab only rebases, which force-pushes: the branch is left alone, and quietly.
	tg := prTarget{api: gitforge.API{Service: gitforge.Service{Kind: gitforge.KindGitLab}}}
	w := &prWatchState{status: prPassed}
	behind := gitforge.MergeState{Code: gitforge.MergeBehind}
	srv.updateBase(context.Background(), c, w, tg, gitforge.PullRequest{Number: 7}, behind)
	if w.status != prPassed {
		t.Errorf("nothing was updated: %s", w.status)
	}
}

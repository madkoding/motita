package gateway

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
)

// Every state in which the gateway is still acting on a pull request is resumed after a restart,
// including the two in which it sent the agent to answer a review or resolve a conflict.
func TestIsWatchedPRStatus(t *testing.T) {
	watched := []string{prFollowing, prFixing, prPassed, prReviewing, prResolving}
	ended := []string{prGaveUp, prNoCI, prMerged, prBaseRed, prClosed, ""}
	for _, s := range watched {
		if !isWatchedPRStatus(s) {
			t.Errorf("%q must be resumed after a restart", s)
		}
	}
	for _, s := range ended {
		if isWatchedPRStatus(s) {
			t.Errorf("%q is over and must not be resumed", s)
		}
	}
}

// The comments the watch has taken in are saved with the session, and a watch resumed in
// "reviewing" after a restart reports the comment made while the gateway was down instead of
// swallowing it in a new baseline.
func TestAResumedReviewingWatchAnswersTheCommentsOfTheDowntime(t *testing.T) {
	srv, host, ss := prSession(t, "https://github.com/o/r.git", func(o *Options) {
		o.PRPollEvery = time.Hour
		o.SessionDir = t.TempDir()
	})
	connectGitHub(t, srv)
	c := srv.sessions[ss.ID]
	sent := recordTasks(t, c)
	talkHost(host, sessionBranch(ss.ID), "pending", "clean")
	host.set("GET https://api.github.com/repos/o/r/commits/abc/check-runs",
		`{"check_runs":[{"id":1,"name":"test","status":"in_progress"}]}`)

	// The first reading is the baseline, and it is saved.
	if srv.pollPR(c, &prWatchState{status: prFollowing}) {
		t.Fatal("a pending CI is not the end of the watch")
	}
	recs, err := srv.store.loadAll()
	if err != nil {
		t.Fatal(err)
	}
	var saved *prWatchRecord
	for _, r := range recs {
		if r.ID == ss.ID {
			saved = r.PRWatch
		}
	}
	if saved == nil || len(saved.Seen) != 1 {
		t.Fatalf("the baseline was not saved with the watch: %+v", saved)
	}

	// The gateway restarts with the agent answering a review, and a comment arrives meanwhile.
	saved.Status = prReviewing
	srv.resumePRWatch(sessionRecord{ID: ss.ID, PRWatch: saved})
	srv.prMu.Lock()
	watching := srv.watchingAny(ss.ID)
	srv.prMu.Unlock()
	if !watching {
		t.Fatal("a watch in reviewing was not resumed")
	}
	c.stateMu.Lock()
	seen := slices.Clone(c.prSeen)
	c.stateMu.Unlock()
	if !slices.Equal(seen, saved.Seen) {
		t.Fatalf("the resumed watch did not start from the saved comments: %v, want %v", seen, saved.Seen)
	}

	host.set(comments, `[{"id":1,"body":"from before"},{"id":2,"body":"rename y while you are at it",`+
		`"user":{"login":"bo"}}]`)
	w := &prWatchState{status: prReviewing}
	w.talk.Resume(saved.Seen)
	if srv.pollPR(c, w) || w.status != prReviewing || w.attempts != 1 {
		t.Fatalf("the comment of the downtime was not answered: %s %d", w.status, w.attempts)
	}
	if tasks := sent.wait(t, c, 1); !strings.Contains(tasks[0], "rename y") {
		t.Errorf("task = %s", tasks[0])
	}
}

// A long discussion is not carried in the record: past prSeenLimit the restart takes a new
// baseline, as it did before the IDs were saved.
func TestALongDiscussionIsNotSaved(t *testing.T) {
	var w prWatchState
	var act gitforge.Activity
	for i := 0; i <= prSeenLimit; i++ {
		act.Comments = append(act.Comments, gitforge.Comment{ID: fmt.Sprintf("c:%d", i)})
	}
	w.talk.NewComments(act)
	if got := w.seenRecord(); got != nil {
		t.Fatalf("%d IDs were kept, want none past the limit", len(got))
	}
}

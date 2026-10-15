package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/schedule"
)

// stillNot fails the test if cond turns true within a short window: what it checks would happen
// right after the run goroutine's last steps, so a window this long is where it would show.
func stillNot(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if cond() {
			t.Fatal(what)
		}
		time.Sleep(time.Millisecond)
	}
}

// A message queued in a session that is then deleted never runs. The deletion waited for the run's
// done channel and nothing more, and the run goroutine went on to drain the queue after done: the
// next message started in a conversation that had just been forgotten.
func TestDeletingASessionDropsItsQueueAndStartsNothing(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	abandon := startInBackground(t, srv, a.ID, "/task", `{"task":"block-1"}`)
	defer abandon()
	waitBlocked(t, rec)
	c := srv.conversationOf(a.ID)
	if started, _ := srv.startOrQueue(c, "after-delete"); started {
		t.Fatal("the message must be queued behind the run")
	}

	if w := send(t, srv, http.MethodDelete, "/v1/sessions/"+a.ID, testToken, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if got := queueOf(c); len(got) != 0 {
		t.Fatalf("the deleted session kept its queue: %q", got)
	}
	stillNot(t, "a message queued in a deleted session ran", func() bool { return rec.ran("after-delete") })
	if c.takeRunSlot() {
		t.Fatal("a deleted session gave its run slot out again")
	}
	close(rec.release)
}

// A title asked for before the default session was reset is not written after it, even when the
// conversation that follows starts with the same words: the reset is a new conversation.
func TestAResetDefaultSessionIsNotNamedByTheTitleOfTheOldOne(t *testing.T) {
	asked := make(chan struct{})
	release := make(chan struct{})
	srv, c, svc := newTitleServer(t, func(context.Context, string) string {
		close(asked)
		<-release
		return "Old conversation"
	})
	rn, ok := srv.startDetachedRun(c, "first", schedule.KindTask, "", srv.unattendedApprover("t"))
	if !ok {
		t.Fatal("the run did not start")
	}
	<-rn.done
	<-asked

	if w := send(t, srv, http.MethodDelete, "/v1/sessions/"+DefaultSession, testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	if c.isClosing() {
		t.Fatal("the default session must be usable again after its reset")
	}
	svc.RestoreTranscript([]agent.DialogueTurn{{User: "fix the build"}})
	close(release)
	waitUntil(t, "the title request to end", func() bool {
		c.stateMu.Lock()
		defer c.stateMu.Unlock()
		return !c.titling
	})
	if got := c.status().Title; got != placeholderTitle {
		t.Fatalf("Title = %q: the old conversation's title landed on the reset one", got)
	}
}

// No title is asked for a conversation that is being deleted.
func TestNoTitleIsAskedForAClosingConversation(t *testing.T) {
	srv, c, _ := newTitleServer(t, func(context.Context, string) string {
		t.Error("a title was asked for a conversation being deleted")
		return "Named"
	})
	c.beginClosing()
	srv.autoTitle(c)
	c.stateMu.Lock()
	titling := c.titling
	c.stateMu.Unlock()
	if titling {
		t.Fatal("a title request started for a conversation being deleted")
	}
	if c.setTitleIfPlaceholder("Named", c.epoch) {
		t.Fatal("a title was written to a conversation being deleted")
	}
}

// A deletion that is refused gives the conversation back as it was: its queue is kept and drained,
// and runs start in it again.
func TestARefusedDeletionReopensAndDrains(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	c := srv.conversationOf(a.ID)

	c.beginClosing()
	if started, pos := srv.startOrQueue(c, "kept"); started || pos != 1 {
		t.Fatalf("a closing conversation started a message: %v %d", started, pos)
	}
	srv.drainQueue(c)
	if rec.ran("kept") {
		t.Fatal("a closing conversation drained its queue")
	}
	srv.reopenAndDrain(c, false)
	waitUntil(t, "the kept message to run", func() bool { return rec.ran("kept") })
	waitIdle(t, c)

	c.beginClosing()
	c.pushQueue("dropped")
	srv.reopenAndDrain(c, true)
	if got := queueOf(c); len(got) != 0 || c.isClosing() {
		t.Fatalf("a reset must drop the queue and reopen: %q %v", got, c.isClosing())
	}
}

// A run that opened a pull request is not followed once its session is being deleted.
func TestAClosingConversationFollowsNoPullRequest(t *testing.T) {
	srv, c, _ := newTitleServer(t, func(context.Context, string) string { return "" })
	c.projectID = "p"
	c.beginClosing()
	srv.followOpenedPR(c)
	if c.prWatchView() != nil {
		t.Fatal("a pull request was followed for a session being deleted")
	}
}

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/schedule"
)

// titleService is the fake service with a title the test controls: generate stands in for the
// model call, so a test can make it slow or make the conversation change while it is asked.
type titleService struct {
	*fakeService
	generate func(ctx context.Context, first string) string
}

func (t *titleService) GenerateTitle(ctx context.Context, first string) string {
	return t.generate(ctx, first)
}

// newTitleServer is a server whose default conversation is untitled and has a first message, which
// is exactly the state the first run of every new session ends in.
func newTitleServer(t *testing.T, generate func(context.Context, string) string) (*Server, *conversation, *titleService) {
	t.Helper()
	svc := &titleService{
		fakeService: &fakeService{
			transcript: []agent.DialogueTurn{{User: "fix the build"}, {Agent: "done"}},
			task: func(context.Context, string, func(string, ...any)) (string, error) {
				return "the answer", nil
			},
		},
		generate: generate,
	}
	srv := newTestServer(t, svc)
	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("the default conversation must exist")
	}
	c.svc = svc
	c.setTitle(placeholderTitle)
	return srv, c, svc
}

// The next message after `done` is accepted while the title is still being generated. The slot used
// to be held through the model call that names the session, so the first message a person sent after
// the first answer of every new session was refused with 409 "a run is already in progress".
func TestTheSlotIsFreeWhenARunReportsDoneEvenWhileItsTitleIsGenerated(t *testing.T) {
	asked := make(chan struct{})
	release := make(chan struct{})
	srv, c, _ := newTitleServer(t, func(_ context.Context, first string) string {
		close(asked)
		<-release
		return "Fixing the build"
	})
	defer waitForNoRun(t, srv)
	defer close(release)

	rn, ok := srv.startDetachedRun(c, "first", schedule.KindTask, "", srv.unattendedApprover("t"))
	if !ok {
		t.Fatal("the first run did not start")
	}
	<-rn.done
	// No sleep, no poll: a client that has read `done` may send at once, and must be served.
	next, ok := srv.startDetachedRun(c, "second", schedule.KindTask, "", srv.unattendedApprover("t"))
	if !ok {
		t.Fatal("a run started right after the previous one reported done was refused: the slot was still held")
	}
	<-next.done

	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the title was never asked for")
	}
}

// The title is still set, after the run, from the first thing the person said.
func TestATitleIsGeneratedAfterTheRun(t *testing.T) {
	srv, c, _ := newTitleServer(t, func(_ context.Context, first string) string { return "About: " + first })

	rn, ok := srv.startDetachedRun(c, "first", schedule.KindTask, "", srv.unattendedApprover("t"))
	if !ok {
		t.Fatal("the run did not start")
	}
	<-rn.done
	waitForNoRun(t, srv)
	if got := c.status().Title; got != "About: fix the build" {
		t.Errorf("Title = %q, want the generated one", got)
	}
}

// The model is slow and the conversation does not wait for it, so what it answers is set only if it
// still applies: a person's rename wins, a conversation emptied in the meantime is not named after
// what it no longer holds, a gateway that is closing writes nothing, and an empty answer is no title.
func TestAGeneratedTitleIsDroppedWhenTheConversationMovedOn(t *testing.T) {
	cases := map[string]struct {
		during func(srv *Server, c *conversation, svc *titleService)
		answer string
		want   string
	}{
		"renamed while it was asked": {
			during: func(_ *Server, c *conversation, _ *titleService) { c.setTitle("my own name") },
			answer: "Generated", want: "my own name",
		},
		"emptied while it was asked": {
			during: func(_ *Server, _ *conversation, svc *titleService) { svc.ResetConversation() },
			answer: "Generated", want: placeholderTitle,
		},
		"the gateway closed while it was asked": {
			during: func(srv *Server, _ *conversation, _ *titleService) { _ = srv.Close(context.Background()) },
			answer: "Generated", want: placeholderTitle,
		},
		"the model answered nothing": {
			during: func(*Server, *conversation, *titleService) {},
			answer: "", want: placeholderTitle,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var srv *Server
			var c *conversation
			var svc *titleService
			srv, c, svc = newTitleServer(t, func(context.Context, string) string {
				tc.during(srv, c, svc)
				return tc.answer
			})

			srv.maybeAutoTitle(context.Background(), c)

			if got := c.status().Title; got != tc.want {
				t.Errorf("Title = %q, want %q", got, tc.want)
			}
		})
	}
}

// One title request at a time per conversation: a turn that ends while the previous title is still
// being asked for does not ask again.
func TestOnlyOneTitleIsAskedForAtATime(t *testing.T) {
	asked := 0
	srv, c, _ := newTitleServer(t, func(context.Context, string) string {
		asked++
		return "Generated"
	})
	c.stateMu.Lock()
	c.titling = true
	c.stateMu.Unlock()

	srv.autoTitle(c)

	c.stateMu.Lock()
	c.titling = false
	c.stateMu.Unlock()
	if asked != 0 {
		t.Errorf("a title was asked for %d times while another request was in flight, want none", asked)
	}
}

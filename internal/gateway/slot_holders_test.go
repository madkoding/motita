package gateway

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/schedule"
)

// gatedService blocks inside a shell command, or inside a checkpoint restore once armed, until the
// test lets it go, and records the tasks it runs.
type gatedService struct {
	fakeService
	rec     *queueRec
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func newGatedService() *gatedService {
	g := &gatedService{
		rec:     &queueRec{},
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	g.task = func(_ context.Context, task string, _ func(string, ...any)) (string, error) {
		g.rec.add(task)
		return "done " + task, nil
	}
	return g
}

func (g *gatedService) hold() {
	g.entered <- struct{}{}
	<-g.release
}

func (g *gatedService) RunShell(context.Context, string, agent.Approver) (string, int, error) {
	g.hold()
	return "ok\n", 0, nil
}

func (g *gatedService) RestoreTranscript(turns []agent.DialogueTurn) {
	if g.armed.Load() {
		g.hold()
	}
	g.fakeService.RestoreTranscript(turns)
}

// waitEntered waits until the service is blocked inside the operation under test.
func waitEntered(t *testing.T, g *gatedService) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the operation never reached the service")
	}
}

// expectSlotHeld checks that, while the operation runs, no run can start in its workspace and a
// message offered meanwhile is queued, then lets the operation finish and waits for that message to
// run: the operation drains what was queued while it held the slot.
func expectSlotHeld(t *testing.T, srv *Server, c *conversation, g *gatedService, answered <-chan int) {
	t.Helper()
	waitEntered(t, g)
	if _, ok := srv.startDetachedRun(c, "scheduled", schedule.KindTask, "", srv.unattendedApprover("t")); ok {
		t.Fatal("a run started in the workspace while the operation was still using it")
	}
	if started, _ := srv.startOrQueue(c, "queued"); started {
		t.Fatal("a queued message started while the operation was still using the workspace")
	}
	close(g.release)
	if code := <-answered; code != http.StatusOK {
		t.Fatalf("the operation answered %d", code)
	}
	waitUntil(t, "the queued message to run after the operation", func() bool { return g.rec.ran("queued") })
	waitUntil(t, "the session to go idle", func() bool { return !c.isRunning() })
}

// A shell command holds the run slot while it runs. It used to check the slot was free and go on
// without taking it, so a scheduled run or a pull request fix could start in the same workspace.
func TestAShellCommandHoldsTheRunSlot(t *testing.T) {
	g := newGatedService()
	srv := newTestServer(t, g)
	c := defaultConv(t, srv)

	answered := make(chan int, 1)
	go func() {
		answered <- post(t, srv, sessionPath(srv, DefaultSession, "/shell"), `{"command":"ls"}`, testToken).Code
	}()
	expectSlotHeld(t, srv, c, g, answered)
}

// Going back to a checkpoint holds the run slot for the whole restore, for the same reason.
func TestARestoreHoldsTheRunSlot(t *testing.T) {
	g := newGatedService()
	srv := newTestServer(t, g)
	c := defaultConv(t, srv)
	c.openCheckpoint(0, "first", schedule.KindTask, false)
	g.fakeService.transcript = []agent.DialogueTurn{{User: "first", Agent: "done"}}
	g.armed.Store(true)

	answered := make(chan int, 1)
	go func() { answered <- restoreReq(t, srv, "0", `{}`).Code }()
	expectSlotHeld(t, srv, c, g, answered)
}

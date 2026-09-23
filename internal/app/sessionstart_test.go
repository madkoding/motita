package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/tui"
)

// sessionStartRunner is a tui.Runner that offers conversations AND control of a run, which is what
// entering a session at startup reaches for. It doubles the REMOTE client, so the test can observe
// what the startup path asked of it - which is the whole question: does -session enter the
// conversation, or does it only pick which blank screen you look at?
type sessionStartRunner struct {
	noopRunner
	sessions   []tui.SessionInfo
	transcript []tui.Turn

	// live is the run in flight in the gateway, and nil when the conversation is idle.
	live []string
	// followed is closed when the startup path asked to follow the run, which is how a test knows
	// it did without polling the interface.
	followed chan struct{}
	// release lets the test end the run: one that ended instantly could not show that the interface
	// was following it at all.
	release chan struct{}
	// switched records the session the interface entered, and switching a second time records the
	// move: it is how the tests tell "entered sabc" from "entered sabc and then something else".
	switches []string

	mu sync.Mutex
}

func (r *sessionStartRunner) ListSessions(context.Context) ([]tui.SessionInfo, error) {
	return r.sessions, nil
}

func (r *sessionStartRunner) SwitchSession(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.switches = append(r.switches, id)
	return nil
}

func (r *sessionStartRunner) CurrentSession() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.switches) == 0 {
		return ""
	}
	return r.switches[len(r.switches)-1]
}

func (r *sessionStartRunner) Conversation(context.Context) ([]tui.Turn, error) {
	return r.transcript, nil
}

func (r *sessionStartRunner) LiveRun(context.Context) (tui.LiveRun, bool, error) {
	if r.live == nil {
		return tui.LiveRun{}, false, nil
	}
	return tui.LiveRun{RunID: "run-1", LastSeq: 7}, true, nil
}

func (r *sessionStartRunner) FollowRun(ctx context.Context, progress func(string, ...any)) (string, error) {
	if r.followed != nil {
		close(r.followed)
	}
	for _, line := range r.live {
		progress("%s", line)
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "the run finished", nil
}

func (r *sessionStartRunner) CancelRun(context.Context) (bool, error) { return true, nil }

// outBox is an io.Writer the test can read while the interface is still writing to it.
//
// It is needed because the startup RETURNS BEFORE THE RUN ENDS - that is the property under test -
// so a test that read an unguarded buffer while the followed run reported would be a data race, and
// -race says so.
type outBox struct {
	mu   sync.Mutex
	text string
}

func (b *outBox) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text += string(p)
	return len(p), nil
}

func (b *outBox) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text
}

// waitForText waits until the interface has drawn s, which is how a test observes a turn that is
// still reporting after the startup returned.
func waitForText(t *testing.T, out *outBox, s string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), s) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%q was never drawn:\n%s", s, out.String())
}

// startClient runs the real client path with the given runner behind the seam, and returns what the
// interface drew.
//
// The stdin is EMPTY, so the interface returns as soon as it has read its first line: what is being
// measured is the STARTUP, and a test that kept the interface open would have to close it by
// guessing when it had finished starting.
func startClient(t *testing.T, runner tui.Runner, session string) (*outBox, int) {
	t.Helper()
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()
	cfgPath := planConfig(t, srv)
	writeTokenFile(t, cfgPath)

	out := &outBox{}
	op, _ := connectOptions(t, out, []string{
		"-config", cfgPath, "-connect", srv.URL, "-tui", "-session", session,
	})
	op.Out = out
	op.Stdin = strings.NewReader("")
	op.NewClient = func(baseURL, tok, session string) tui.Runner { return runner }
	code := Run(op)
	return out, code
}

// TestStartingAsAClientOnASessionShowsItsConversation: -session is how a user says "take me back to
// that conversation", and starting on a blank screen contradicts it. The attach path is what makes
// the flag mean anything - otherwise it only picks which blank screen you look at.
func TestStartingAsAClientOnASessionShowsItsConversation(t *testing.T) {
	r := &sessionStartRunner{
		sessions:   []tui.SessionInfo{{ID: "default"}, {ID: "sabc"}},
		transcript: []tui.Turn{{User: "count the files", Agent: "there are twelve"}},
	}

	out, code := startClient(t, r, "sabc")
	if code != Success {
		t.Fatalf("the client exited %d; output:\n%s", code, out)
	}
	if got := r.CurrentSession(); got != "sabc" {
		t.Errorf("the client speaks for %q, -session must enter the conversation", got)
	}
	for _, want := range []string{"count the files", "there are twelve"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the conversation was not drawn on startup: %q is missing from:\n%s", want, out)
		}
	}
}

// TestStartingAsAClientOnASessionWithALiveRunFollowsIt: the same case as /attach, at startup. A
// client that connects while a turn is in flight must show it, or the user's first act is to wonder
// whether anything is happening.
func TestStartingAsAClientOnASessionWithALiveRunFollowsIt(t *testing.T) {
	r := &sessionStartRunner{
		sessions:   []tui.SessionInfo{{ID: "default"}, {ID: "sabc"}},
		transcript: []tui.Turn{{Agent: "an earlier answer"}},
		live:       []string{"reading the tree"},
		followed:   make(chan struct{}),
		release:    make(chan struct{}),
	}

	// The startup must NOT wait for the turn. This is the same risk as /attach, at the other call
	// site: a Run that blocked here would leave the interface undrawn until the run ended, so the
	// user would see nothing at all.
	done := make(chan int, 1)
	var out *outBox
	go func() {
		box, code := startClient(t, r, "sabc")
		out = box
		done <- code
	}()
	select {
	case code := <-done:
		if code != Success {
			t.Fatalf("the client exited %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("starting as a client blocked on the run in flight: the interface would never be drawn")
	}

	select {
	case <-r.followed:
	case <-time.After(5 * time.Second):
		t.Fatal("the client did not follow the run already in flight in the session it was told to enter")
	}
	close(r.release)
	waitForText(t, out, "reading the tree")
}

// TestStartingAsAClientKeepsTheActionableSessionError: entering the session replaced the old
// existence check, and the check's real value was the MESSAGE: a wrong id must name itself and list
// the ids that exist, or the user has no next step. Losing that in the refactor would be a
// regression nobody notices until they mistype a session.
func TestStartingAsAClientKeepsTheActionableSessionError(t *testing.T) {
	r := &sessionStartRunner{
		sessions: []tui.SessionInfo{{ID: "default"}, {ID: "sother"}},
	}

	out, code := startClient(t, r, "smissing")
	if code == Success {
		t.Fatalf("a session the gateway does not hold must fail, not open an interface:\n%s", out)
	}
	if !strings.Contains(out.String(), "smissing") {
		t.Errorf("the failure must name the session that was asked for:\n%s", out)
	}
	if !strings.Contains(out.String(), "sother") {
		t.Errorf("the failure must list the sessions that do exist:\n%s", out)
	}
}

// TestStartingAsAClientWithoutTheSessionCapabilityStillRuns: the capability is OPTIONAL, so a runner
// that cannot offer conversations must still start an interface rather than be refused. The
// embedded runner is exactly this shape.
func TestStartingAsAClientWithoutTheSessionCapabilityStillRuns(t *testing.T) {
	out, code := startClient(t, noopRunner{}, "anything")
	if code != Success {
		t.Fatalf("a runner with no session capability must not be refused; exit %d:\n%s", code, out)
	}
}

// TestStartingAsAClientEntersTheSessionONCE: the interface must not be moved twice - once by a
// check and once by the entry - because the second move is the one that could land somewhere nobody
// asked for.
func TestStartingAsAClientEntersTheSessionONCE(t *testing.T) {
	r := &sessionStartRunner{
		sessions:   []tui.SessionInfo{{ID: "default"}, {ID: "sabc"}},
		transcript: []tui.Turn{{Agent: "hello"}},
	}

	if _, code := startClient(t, r, "sabc"); code != Success {
		t.Fatalf("the client exited %d", code)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.switches) != 1 || r.switches[0] != "sabc" {
		t.Errorf("the interface entered %v, it must enter the session exactly once", r.switches)
	}
}

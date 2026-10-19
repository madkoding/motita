package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// routeService is a service that can be given the thread action, and runs tasks that wait until the
// test lets them go.
type routeService struct {
	fakeService
	route func(string) string
}

func (r *routeService) SetThreads(route func(string) string) { r.route = route }

// routeRig is a gateway with one project whose main session was built with the thread action.
type routeRig struct {
	srv        *Server
	projectDir string
	main       SessionStatus
	mu         sync.Mutex
	svcs       []*routeService
}

// last is the service built most recently: the main session's, then each thread's.
func (g *routeRig) last() *routeService {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.svcs[len(g.svcs)-1]
}

func (g *routeRig) newService(release chan struct{}) (Service, error) {
	svc := &routeService{}
	svc.task = func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "", nil
	}
	g.mu.Lock()
	g.svcs = append(g.svcs, svc)
	g.mu.Unlock()
	return svc, nil
}

// waitIdle waits for every run to end, so nothing writes into the test's folders while they go.
func waitNoRuns(srv *Server) {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		busy := false
		for _, c := range srv.snapshot() {
			busy = busy || c.isRunning()
		}
		if !busy {
			return
		}
	}
}

func newRouteRig(t *testing.T) *routeRig {
	t.Helper()
	g := &routeRig{}
	workspace := t.TempDir()
	g.projectDir = filepath.Join(workspace, "proj")
	if err := os.MkdirAll(g.projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, g.projectDir)
	release := make(chan struct{})
	g.srv, _ = startServer(t, Options{
		Token: testToken, WorkspaceDir: workspace,
		ProjectDir: filepath.Join(workspace, "projects"), SessionDir: filepath.Join(workspace, "sessions"),
		NewService: func() (Service, error) { return g.newService(release) },
	})
	t.Cleanup(func() {
		close(release)
		waitNoRuns(g.srv)
		_ = g.srv.Close(context.Background())
	})
	rec := post(t, g.srv, "/v1/projects", `{"title":"proj","dir":"proj"}`, testToken)
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &proj); err != nil {
		t.Fatal(err)
	}
	g.main = newThreadSession(t, g.srv, proj.ID, "")
	return g
}

func TestOnlyAMainSessionIsGivenTheThreadAction(t *testing.T) {
	g := newRouteRig(t)
	route := g.last().route
	if route == nil {
		t.Fatal("the main session of a project must be given the thread action")
	}
	for _, bad := range []string{"", "red boxes", "red boxes ::", ":: do it"} {
		if got := route(bad); !strings.HasPrefix(got, "Error:") {
			t.Errorf("%q must be refused, got %q", bad, got)
		}
	}
	if got := route("list"); !strings.Contains(got, "no threads") {
		t.Errorf("list of a project without threads: %q", got)
	}
	route("red boxes :: make a red box")
	if g.last().route != nil {
		t.Error("a thread must not be given the thread action")
	}
	thread := g.srv.threadsOf(convNamed(t, g.srv, g.main.ID))[0]
	if got := g.srv.routeThread(thread, "list"); !strings.HasPrefix(got, "Error:") {
		t.Errorf("a thread must be refused: %q", got)
	}
}

// The same kind of work goes to the thread that already does it, and different work opens another
// one, in a worktree of its own.
func TestTheMainSessionOpensAndReusesThreadsByTopic(t *testing.T) {
	g := newRouteRig(t)
	route := g.last().route
	if got := route("red boxes :: make a red box"); !strings.Contains(got, "opened") || !strings.Contains(got, "started") {
		t.Errorf("first request: %q", got)
	}
	got := route("Red   BOXES :: make another red box")
	if !strings.Contains(got, "reused") || !strings.Contains(got, "queue") {
		t.Errorf("same kind of work must reuse the thread and wait its turn: %q", got)
	}
	route("white boxes :: make a white box")
	got = route("list")
	if !strings.Contains(got, "- red boxes (working)") || !strings.Contains(got, "- white boxes (working)") {
		t.Errorf("exactly the two threads are expected: %q", got)
	}
	threads := g.srv.threadsOf(convNamed(t, g.srv, g.main.ID))
	if len(threads) != 2 {
		t.Fatalf("threads = %d, want 2", len(threads))
	}
	if ws := threads[0].workspaceDir(); filepath.Dir(ws) == g.projectDir || ws == g.projectDir {
		t.Errorf("a thread works in a worktree of its own, got %q", ws)
	}
}

// A thread that cannot be opened says why instead of losing the brief.
func TestAThreadThatCannotBeOpenedIsReported(t *testing.T) {
	srv, _, mainID := newSessionInProject(t)
	main := convNamed(t, srv, mainID)
	build := srv.opts.NewService
	srv.opts.NewService = nil
	if got := srv.routeThread(main, "red boxes :: make one"); !strings.HasPrefix(got, "Error:") {
		t.Errorf("a gateway that cannot build a session: %q", got)
	}
	srv.opts.NewService = build
	main.setProjectID("gone", "", "")
	if got := srv.routeThread(main, "red boxes :: make one"); !strings.Contains(got, "Error:") {
		t.Errorf("a project that is gone: %q", got)
	}
}

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

// A main session hands work to threads by topic: the same kind of work goes to the thread that
// already does it, and different work opens another one, in a worktree of its own.
func TestTheMainSessionOpensAndReusesThreadsByTopic(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, projectDir)
	release := make(chan struct{})
	var mu sync.Mutex
	var briefs []string
	var svcs []*routeService
	srv, _ := startServer(t, Options{
		Token: testToken, WorkspaceDir: workspace,
		ProjectDir: filepath.Join(workspace, "projects"), SessionDir: filepath.Join(workspace, "sessions"),
		NewService: func() (Service, error) {
			svc := &routeService{}
			svc.task = func(ctx context.Context, task string, _ func(string, ...any)) (string, error) {
				mu.Lock()
				briefs = append(briefs, task)
				mu.Unlock()
				select {
				case <-release:
				case <-ctx.Done():
				}
				return "", nil
			}
			mu.Lock()
			svcs = append(svcs, svc)
			mu.Unlock()
			return svc, nil
		},
	})
	t.Cleanup(func() {
		close(release)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			busy := false
			for _, c := range srv.snapshot() {
				busy = busy || c.isRunning()
			}
			if !busy {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = srv.Close(context.Background())
	})
	rec := post(t, srv, "/v1/projects", `{"title":"proj","dir":"proj"}`, testToken)
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &proj); err != nil {
		t.Fatal(err)
	}
	main := newThreadSession(t, srv, proj.ID, "")
	route := svcs[len(svcs)-1].route
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

	if got := route("red boxes :: make a red box"); !strings.Contains(got, "opened") || !strings.Contains(got, "started") {
		t.Errorf("first request: %q", got)
	}
	if got := route("Red   BOXES :: make another red box"); !strings.Contains(got, "reused") || !strings.Contains(got, "queue") {
		t.Errorf("same kind of work must reuse the thread and wait its turn: %q", got)
	}
	route("white boxes :: make a white box")
	if got := route("list"); !strings.Contains(got, "- red boxes (working)") || !strings.Contains(got, "- white boxes (working)") {
		t.Errorf("exactly the two threads are expected: %q", got)
	}
	threads := srv.threadsOf(convNamed(t, srv, main.ID))
	if len(threads) != 2 {
		t.Fatalf("threads = %d, want 2", len(threads))
	}
	if ws := threads[0].workspaceDir(); filepath.Dir(ws) == projectDir || ws == projectDir {
		t.Errorf("a thread works in a worktree of its own, got %q", ws)
	}
	// A thread cannot open threads, and is not given the action.
	if got := srv.routeThread(threads[0], "list"); !strings.HasPrefix(got, "Error:") {
		t.Errorf("a thread must be refused: %q", got)
	}
	if svcs[len(svcs)-1].route != nil {
		t.Error("the last service built was a thread's, which must not be given the thread action")
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

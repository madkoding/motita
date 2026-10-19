package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// mainRefreshTimeout bounds one background update: a fetch that hangs (a dead network) must not
// keep a goroutine and a lock on the project forever.
const mainRefreshTimeout = 2 * time.Minute

// mainRefreshState is what the gateway knows about the background updates of main branches.
type mainRefreshState struct {
	mu      sync.Mutex
	running map[string]bool
	warned  map[string]string
	wg      sync.WaitGroup
}

// begin marks the project as being updated; false means an update is already running.
func (m *mainRefreshState) begin(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[id] {
		return false
	}
	if m.running == nil {
		m.running = map[string]bool{}
	}
	m.running[id] = true
	return true
}

// finish ends an update and records why it failed, or forgets an earlier failure.
func (m *mainRefreshState) finish(id, warning string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.running, id)
	if warning == "" {
		delete(m.warned, id)
		return
	}
	if m.warned == nil {
		m.warned = map[string]string{}
	}
	m.warned[id] = warning
}

// warning is why the last update of the project failed, "" when it worked or never ran.
func (m *mainRefreshState) warning(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.warned[id]
}

// wait blocks until every running update has ended.
func (m *mainRefreshState) wait() { m.wg.Wait() }

// refreshMain brings the project's assigned main branch up to date with origin, and the main
// branch of the shared repositories it uses. It never forces anything: the project's checkout
// moves forward only, and only when it is clean; anything else it returns as the reason, which is
// a notice for the person and never an error for the session. A project with no main branch or no
// origin has nothing to bring in.
func (s *Server) refreshMain(ctx context.Context, p *Project) string {
	warning := ""
	if main := mainBranchOf(ctx, p); main != "" && gitx.Repo(ctx, p.Dir) == nil {
		if err := gitx.RefreshBranch(ctx, p.Dir, main); err != nil {
			warning = main + ": " + err.Error()
		}
	}
	s.syncSharedRepos(ctx, p)
	return warning
}

// refreshMainInBackground updates the project's main branch without making the caller wait: it is
// what opening and closing a session do, and a slow or unreachable origin must delay neither. At
// most one update per project runs at a time; a failure is logged and kept for the project's
// listing (main_warning).
func (s *Server) refreshMainInBackground(p *Project) {
	if p == nil || !s.mainRefresh.begin(p.ID) {
		return
	}
	proj := *p
	s.mainRefresh.wg.Add(1)
	go func() {
		defer s.mainRefresh.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), mainRefreshTimeout)
		defer cancel()
		warning := s.refreshMain(ctx, &proj)
		s.mainRefresh.finish(proj.ID, warning)
		if warning != "" {
			s.warn("the project's main branch could not be updated", "project", proj.Dir, "reason", warning)
		}
	}()
}

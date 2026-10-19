package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// mainRefreshTimeout bounds one background update: a fetch that hangs (a dead network) must not
// keep a goroutine and a lock on the project forever.
const mainRefreshTimeout = 2 * time.Minute

// newSessionRefreshWait is how long a new session waits for the update of its project's main branch
// before branching from the checkout as it is.
var newSessionRefreshWait = 5 * time.Second

// mainRefreshRetryAfter is the pause before the one retry of a failed update. A variable so the
// tests of a failing origin do not wait for it.
var mainRefreshRetryAfter = 300 * time.Millisecond

// mainRefreshState is what the gateway knows about the background updates of main branches.
type mainRefreshState struct {
	mu      sync.Mutex
	running map[string]chan struct{}
	warned  map[string]string
	wg      sync.WaitGroup
}

// begin marks the project as being updated; false means an update is already running.
func (m *mainRefreshState) begin(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[id] != nil {
		return false
	}
	if m.running == nil {
		m.running = map[string]chan struct{}{}
	}
	m.running[id] = make(chan struct{})
	return true
}

// await waits for the update running for the project, at most d: a session that opens branches
// from the checkout, so it gives a fetch a moment to bring in what origin has, and goes on from the
// checkout as it is when origin is slow. It returns at once when no update runs.
func (m *mainRefreshState) await(id string, d time.Duration) {
	m.mu.Lock()
	done := m.running[id]
	m.mu.Unlock()
	if done == nil {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// finish ends an update and records why it failed, or forgets an earlier failure.
func (m *mainRefreshState) finish(id, warning string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if done := m.running[id]; done != nil {
		close(done)
	}
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
func (s *Server) refreshMain(ctx context.Context, p *Project, explicit bool) string {
	warning := ""
	// A folder of the user's own, outside the workspace, is theirs to update: only an explicit "update
	// now" moves its branch.
	if main := mainBranchOf(ctx, p); main != "" && gitx.Repo(ctx, p.Dir) == nil && (explicit || !s.usersFolder(p.Dir)) {
		before := gitx.BranchCommit(ctx, p.Dir, main)
		err := gitx.RefreshBranch(ctx, p.Dir, main)
		if err != nil && !errors.Is(err, gitx.ErrBranchDirty) {
			// A session opening creates its worktree at the same moment, and a fetch that reads
			// the refs halfway through it fails on a half-written one: a second try, a moment
			// later, sees it whole. A real failure fails the same way twice.
			time.Sleep(mainRefreshRetryAfter)
			err = gitx.RefreshBranch(ctx, p.Dir, main)
		}
		if err != nil {
			warning = main + ": " + err.Error()
		}
		// New code on the main branch: the manuals in the notes catch up with it by themselves.
		if err == nil && gitx.BranchCommit(ctx, p.Dir, main) != before {
			s.scanAfterMainMoved()
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
		warning := s.refreshMain(ctx, &proj, false)
		s.mainRefresh.finish(proj.ID, warning)
		if warning != "" {
			s.warn("the project's main branch could not be updated", "project", proj.Dir, "reason", warning)
		}
	}()
}

// awaitMainRefresh gives the update of the project's main branch a moment to end, so the worktree a
// session is about to get branches from current code and not from the checkout as it was.
func (s *Server) awaitMainRefresh(p *Project) {
	if p != nil {
		s.mainRefresh.await(p.ID, newSessionRefreshWait)
	}
}

// mainRefreshInterval is how often a running gateway brings every project's main branch up to
// date by itself, so a project nobody opens a session in does not drift. A variable so a test
// can reach the tick.
var mainRefreshInterval = 15 * time.Minute

// refreshAllMainsEvery updates every project's main branch each interval until ctx ends.
func (s *Server) refreshAllMainsEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, p := range s.allProjects() {
				s.refreshMainInBackground(&p)
			}
		}
	}
}

// handleRefreshProjectMain is the "update now" of a project whose main branch could not be brought
// up to date: it runs the update and answers with the notice that remains ("" once it worked).
// 409 while another update of the project is still running.
func (s *Server) handleRefreshProjectMain(w http.ResponseWriter, r *http.Request) {
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	if !s.mainRefresh.begin(p.ID) {
		writeError(w, http.StatusConflict, "the main branch is already being updated")
		return
	}
	s.mainRefresh.wg.Add(1)
	defer s.mainRefresh.wg.Done()
	ctx, cancel := context.WithTimeout(r.Context(), mainRefreshTimeout)
	defer cancel()
	warning := s.refreshMain(ctx, p, true)
	s.mainRefresh.finish(p.ID, warning)
	writeJSON(w, http.StatusOK, map[string]string{"main_warning": warning})
}

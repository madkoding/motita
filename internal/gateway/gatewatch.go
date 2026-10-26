package gateway

import (
	"errors"
	"net/http"
	"sync"

	"github.com/madkoding/motita/internal/anchor"
)

// TESTS LOST. A gate that runs `go test ./...` over a tree with no tests exits 0, so deleting the
// tests is the cheapest way to a green run. The anchor refuses that inside one task (guardGate);
// this look catches it between tasks, from any cause. On the same 15-minute look as origin, the
// number of tests each project's folder declares is compared with the most it has had (its floor):
// a drop is shown on the project and told to the phones once, until the person accepts it.

// TestsLost is how far a project's tests fell below its floor.
type TestsLost struct {
	Was int `json:"was"`
	Now int `json:"now"`
}

// gateWatchState is the drops the last look found.
type gateWatchState struct {
	mu   sync.Mutex
	lost map[string]TestsLost
	// alerted is the count each drop was last announced at: the same state is told once.
	alerted map[string]int
}

// of is the drop found for a project, or nil.
func (g *gateWatchState) of(id string) *TestsLost {
	g.mu.Lock()
	defer g.mu.Unlock()
	if l, ok := g.lost[id]; ok {
		return &l
	}
	return nil
}

// record keeps the outcome of a look and says whether the drop is news.
func (g *gateWatchState) record(id string, lost *TestsLost) (fresh bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lost == nil {
		g.lost, g.alerted = map[string]TestsLost{}, map[string]int{}
	}
	if lost == nil {
		delete(g.lost, id)
		delete(g.alerted, id)
		return false
	}
	g.lost[id] = *lost
	if prev, told := g.alerted[id]; told && prev == lost.Now {
		return false
	}
	g.alerted[id] = lost.Now
	return true
}

// watchTests compares the project's tests with its floor: more raises the floor, fewer is a drop.
func (s *Server) watchTests(p *Project) {
	now := anchor.TestsIn(p.Dir)
	var lost *TestsLost
	switch {
	case now > p.TestsFloor:
		_, _ = s.projects.update(p.ID, func(q *Project) { q.TestsFloor = now })
	case now < p.TestsFloor:
		lost = &TestsLost{Was: p.TestsFloor, Now: now}
	}
	fresh := s.gateWatch.record(p.ID, lost)
	s.remoteWatch.feed.kick()
	if fresh {
		s.notifyAutopilot("motita-tests-"+p.ID, "Tests lost",
			"%s had %d tests and now has %d. Look at what was deleted, or accept the lower count in the project's menu.",
			p.Title, lost.Was, lost.Now)
	}
}

// handleAcceptTests takes the project's current number of tests as its floor: the person looked
// and the lower count is meant.
func (s *Server) handleAcceptTests(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	p, err := s.projects.update(r.PathValue("id"), func(q *Project) { q.TestsFloor = anchor.TestsIn(q.Dir) })
	switch {
	case errors.Is(err, errProjectGone):
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		s.gateWatch.record(p.ID, nil)
		s.remoteWatch.feed.kick()
		writeJSON(w, http.StatusOK, map[string]int{"tests_floor": p.TestsFloor})
	}
}

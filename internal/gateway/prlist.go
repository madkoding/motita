package gateway

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
)

// The pull request overview answers, in one read, which pull requests are open in the repositories
// of the projects, what their CI says, whether the host would merge them, and which session follows
// each: it is where a failing CI or a stale branch shows up without opening every session.

// PRListItem is one open pull request of a project's repository.
type PRListItem struct {
	ProjectID string               `json:"project_id"`
	Project   string               `json:"project"`
	Repo      string               `json:"repo"`
	PR        gitforge.PullRequest `json:"pr"`
	CI        *gitforge.CIStatus   `json:"ci,omitempty"`
	Merge     *gitforge.MergeState `json:"merge,omitempty"`
	// Session follows this pull request (it opened it, or started following it), empty when none.
	Session string       `json:"session,omitempty"`
	Watch   *PRWatchView `json:"watch,omitempty"`
}

// PRListProblem is a project whose pull requests could not be read, and why.
type PRListProblem struct {
	Project string `json:"project"`
	Error   string `json:"error"`
}

// PRList is the answer of GET /v1/pull-requests.
type PRList struct {
	PullRequests []PRListItem    `json:"pull_requests"`
	Problems     []PRListProblem `json:"problems,omitempty"`
}

// prListTTL is how long the overview is remembered: the hosts are asked about every pull request
// of every project, and the answer does not move by the second.
const prListTTL = 30 * time.Second

type prListCache struct {
	mu   sync.Mutex
	at   time.Time
	list PRList
}

func (c *prListCache) fresh() (PRList, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.list, !c.at.IsZero() && time.Since(c.at) < prListTTL
}

func (c *prListCache) put(l PRList) {
	c.mu.Lock()
	c.at, c.list = time.Now(), l
	c.mu.Unlock()
}

// handleListPRs lists the open pull requests of every project. ?refresh=1 skips the short memory.
func (s *Server) handleListPRs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("refresh") == "" {
		if l, ok := s.prLists.fresh(); ok {
			writeJSON(w, http.StatusOK, l)
			return
		}
	}
	l := s.listPRs(r.Context())
	s.prLists.put(l)
	writeJSON(w, http.StatusOK, l)
}

// listPRs reads the pull requests of each project's repository, once per repository: projects that
// share one see the same list under the first of them.
func (s *Server) listPRs(ctx context.Context) PRList {
	out := PRList{PullRequests: []PRListItem{}}
	seen := map[string]bool{}
	for _, p := range s.allProjects() {
		t, err := s.forgeOf(ctx, p)
		if err != nil {
			out.Problems = append(out.Problems, PRListProblem{Project: p.Title, Error: err.Error()})
			continue
		}
		if seen[t.key()] {
			continue
		}
		seen[t.key()] = true
		prs, err := t.api.OpenPRsFrom(ctx, t.remote, "")
		if err != nil {
			out.Problems = append(out.Problems, PRListProblem{Project: p.Title, Error: err.Error()})
			continue
		}
		items := s.describePRs(ctx, p, t, prs)
		out.PullRequests = append(out.PullRequests, items...)
	}
	return out
}

// describePRs reads the CI and the merge state of each pull request, all at once: they are
// independent reads of the same host. A pull request whose CI cannot be read is still listed.
func (s *Server) describePRs(ctx context.Context, p Project, t forgeTarget, prs []gitforge.PullRequest) []PRListItem {
	items := make([]PRListItem, len(prs))
	var wg sync.WaitGroup
	for i, pr := range prs {
		items[i] = PRListItem{ProjectID: p.ID, Project: p.Title, Repo: t.remote.Path, PR: pr}
		s.followerOf(&items[i])
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ci, err := t.api.CI(ctx, t.remote, pr.Number); err == nil {
				items[i].CI = &ci
			}
			if m, err := t.api.MergeState(ctx, t.remote, pr.Number); err == nil {
				items[i].Merge = &m
			}
		}()
	}
	wg.Wait()
	return items
}

// followerOf names the session of the project that follows the pull request, and what it does about it.
func (s *Server) followerOf(item *PRListItem) {
	for _, c := range s.snapshot() {
		if c.projectID != item.ProjectID || c.prNumber() != item.PR.Number {
			continue
		}
		item.Session, item.Watch = c.id, c.prWatchView()
		return
	}
}

// prCaches are the short memories of what the git hosts said: the CI of base branches, shared by
// the sessions that follow pull requests into them, and the pull request overview.
type prCaches struct {
	baseCI  map[string]baseCIEntry
	prLists prListCache
}

package gateway

import (
	"context"
	"errors"
	"fmt"
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
	// Failing is how many of them have a failing CI, for the badge in the sidebar.
	Failing int `json:"failing"`
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

// prRepo is one repository of a project whose pull requests are listed.
type prRepo struct{ name, dir, url string }

// prReposOf are the project's own repository, the other repositories added to it and the ones its
// plain folder holds. A plain folder is no repository to read: only what it holds is.
func prReposOf(ctx context.Context, p Project) []prRepo {
	var repos []prRepo
	found := foundRepos(ctx, &p)
	if len(found) == 0 {
		repos = append(repos, prRepo{"", p.Dir, p.GitURL})
	}
	for _, r := range p.Repos {
		repos = append(repos, prRepo{r.Name, r.Dir, r.GitURL})
	}
	for _, f := range found {
		repos = append(repos, prRepo{f.rel, f.dir, ""})
	}
	return repos
}

// listPRs reads the pull requests of each repository of each project, once per repository:
// projects that share one see the same list under the first of them.
func (s *Server) listPRs(ctx context.Context) PRList {
	out := PRList{PullRequests: []PRListItem{}}
	seen := map[string]bool{}
	for _, p := range s.allProjects() {
		for _, r := range prReposOf(ctx, p) {
			t, err := s.forgeOfRepo(ctx, r.dir, r.url)
			if err == nil && !seen[t.key()] {
				seen[t.key()] = true
				var prs []gitforge.PullRequest
				if prs, err = t.api.OpenPRsFrom(ctx, t.remote, ""); err == nil {
					out.PullRequests = append(out.PullRequests, s.describePRs(ctx, p, t, prs)...)
				}
			}
			if err != nil {
				label := p.Title
				if r.name != "" {
					label += " / " + r.name
				}
				out.Problems = append(out.Problems, PRListProblem{Project: label, Error: err.Error()})
			}
		}
	}
	for _, it := range out.PullRequests {
		if it.CI != nil && it.CI.State == gitforge.StateFailure {
			out.Failing++
		}
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
	red     redPRs
}

// invalidate forgets the remembered overview, so the next read asks the hosts.
func (c *prListCache) invalidate() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

// handleUpdatePRBranch asks the host to merge a pull request's base into its branch, for the
// pull request the person picked in the overview: it needs no session. Nothing is rebased or
// force-pushed, and a branch that conflicts is refused by the host.
func (s *Server) handleUpdatePRBranch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"project_id"`
		Repo      string `json:"repo"`
		Number    int    `json:"number"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	p := s.projectOf(body.ProjectID)
	if p == nil || body.Number <= 0 {
		writeError(w, http.StatusNotFound, "there is no such project or pull request")
		return
	}
	for _, repo := range prReposOf(r.Context(), *p) {
		t, err := s.forgeOfRepo(r.Context(), repo.dir, repo.url)
		if err != nil || t.remote.Path != body.Repo {
			continue
		}
		err = t.api.UpdateBranch(r.Context(), t.remote, body.Number)
		if errors.Is(err, gitforge.ErrNoBranchUpdate) {
			writeError(w, http.StatusConflict, err.Error())
		} else if err != nil {
			s.writePRError(w, prHostError(t.api.Service, err))
		} else {
			s.prLists.invalidate()
			writeJSON(w, http.StatusAccepted, map[string]bool{"updating": true})
		}
		return
	}
	writeError(w, http.StatusNotFound, "that repository is not one of the project's")
}

// redPRs tracks which pull requests had a failing CI at the last look, so a phone is told once when
// one turns red. The first look only takes the baseline: what was red before is not news.
type redPRs struct {
	mu     sync.Mutex
	seen   map[string]bool
	primed bool
}

// fresh records the failing pull requests of list and returns the ones that were not failing at the
// last look.
func (r *redPRs) fresh(list PRList) []PRListItem {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := map[string]bool{}
	var fresh []PRListItem
	for _, it := range list.PullRequests {
		if it.CI == nil || it.CI.State != gitforge.StateFailure {
			continue
		}
		key := fmt.Sprintf("%s#%d", it.Repo, it.PR.Number)
		now[key] = true
		if r.primed && !r.seen[key] {
			fresh = append(fresh, it)
		}
	}
	r.seen, r.primed = now, true
	return fresh
}

// announceRedPRs reads the overview and tells the phones about each pull request whose CI just
// turned red, and says how many that were. It also refreshes the remembered overview.
func (s *Server) announceRedPRs(ctx context.Context) int {
	list := s.listPRs(ctx)
	s.prLists.put(list)
	fresh := s.red.fresh(list)
	for _, it := range fresh {
		s.notifyAutopilot(fmt.Sprintf("motita-pr-red-%s-%d", it.ProjectID, it.PR.Number),
			"CI failing on a pull request", "%s #%d: the CI is failing. %s", it.Repo, it.PR.Number, it.PR.Title)
	}
	return len(fresh)
}

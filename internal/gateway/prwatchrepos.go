package gateway

import (
	"context"
	"strings"
)

// A session of a project that holds several repositories can have a pull request in each (one per
// repository, see projectReposNote). Each one is followed by a watch of its own, pinned to its
// repository: the first is the session's watch (shown in the session list, saved and resumed), the
// others are extra ones, kept in prRepoWatches.

// firstWatchRepo is the repository a watch started without one follows: the one the session's watch
// already follows, else the one its pull request is about (see sessionRepoDir).
func (s *Server) firstWatchRepo(c *conversation) string {
	if v := c.prWatchView(); v != nil && v.Repo != "" {
		return v.Repo
	}
	dir := c.sessionRepoDir(context.Background(), "")
	if dir == "" {
		return ""
	}
	return c.repoName(dir)
}

// firstWatchElsewhere reports whether the session's watch is about another repository's pull
// request that is still open, so a watch of repo is an extra one. The caller holds s.prMu.
func (s *Server) firstWatchElsewhere(c *conversation, repo string) bool {
	v := c.prWatchView()
	if v == nil || v.Repo == "" || v.Repo == repo {
		return false
	}
	return s.prWatching[c.id+"\x00"+v.Repo] || prStillOpen(v)
}

// prStillOpen reports whether the pull request a view is about has not ended (merged or closed).
func prStillOpen(v *PRWatchView) bool {
	return v != nil && v.Status != prMerged && v.Status != prClosed
}

// watchViewOf is the view of the watch w.
func (c *conversation) watchViewOf(w *prWatchState) *PRWatchView {
	if !w.extra {
		return c.prWatchView()
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if v := c.prRepoWatches[w.repo]; v != nil {
		copied := *v
		return &copied
	}
	return nil
}

// setWatchViewOf replaces the view of the watch w (nil: no watch).
func (c *conversation) setWatchViewOf(w *prWatchState, v *PRWatchView) {
	if !w.extra {
		c.setPRWatch(v)
		return
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if v == nil {
		delete(c.prRepoWatches, w.repo)
		return
	}
	if c.prRepoWatches == nil {
		c.prRepoWatches = map[string]*PRWatchView{}
	}
	c.prRepoWatches[w.repo] = v
}

// watchNumberOf is the number of the pull request the watch w follows, 0 when it has none yet.
func (c *conversation) watchNumberOf(w *prWatchState) int {
	if v := c.watchViewOf(w); v != nil {
		return v.Number
	}
	return 0
}

// watchDone reports that the pull request of the watch w is over: the session was integrated, or
// the pull request was merged.
func (c *conversation) watchDone(w *prWatchState) bool {
	if !w.extra {
		return c.prDone()
	}
	v := c.watchViewOf(w)
	return c.isMerged() || v != nil && v.Status == prMerged
}

// otherPRsOpen reports whether a pull request of another repository than repo is still open: the
// session is integrated only once all of them are.
func (c *conversation) otherPRsOpen(repo string) bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if v := c.prWatch; v != nil && v.Repo != repo && prStillOpen(v) {
		return true
	}
	for name, v := range c.prRepoWatches {
		if name != repo && prStillOpen(v) {
			return true
		}
	}
	return false
}

// repoWatchView is the view of the watch of the repository called repo, nil when none.
func (c *conversation) repoWatchView(repo string) *PRWatchView {
	if v := c.prWatchView(); v != nil && v.Repo == repo {
		return v
	}
	return c.watchViewOf(&prWatchState{repo: repo, extra: true})
}

// watchRepo starts following the pull request of the repository called repo.
func (s *Server) watchRepo(c *conversation, repo string) {
	s.startPRWatch(c, &prWatchState{status: prFollowing, repo: repo})
}

// watchingAny reports whether a watch of any of the session's repositories is running. The caller
// holds s.prMu.
func (s *Server) watchingAny(id string) bool {
	for key, on := range s.prWatching {
		if on && strings.HasPrefix(key, id+"\x00") {
			return true
		}
	}
	return false
}

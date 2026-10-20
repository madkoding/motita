package gateway

import (
	"cmp"
	"context"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// REMOTE CHANGES. Every so often the gateway asks origin whether the main branch of each project
// (and of the other repositories it uses) moved, and tells the person once per change. It only
// reads: a fetch of origin's copy of the branch, then a comparison. No checkout, no local branch
// and no working tree is touched, so nothing is updated by itself - the person decides, with
// "Update main branch" or by reviewing the commits first.

// remoteWatchInterval is how often origin is asked. Fifteen minutes: news arrives the same
// working hour, and a fetch per repository is cheap.
const remoteWatchInterval = 15 * time.Minute

// remoteWatchFirstDelay lets the gateway finish starting before the first look.
const remoteWatchFirstDelay = time.Minute

// remoteWatchTimeout bounds one look at one repository, so a dead network cannot pile them up.
const remoteWatchTimeout = time.Minute

// remoteLatest is how many subjects of the newest commits a change carries, to say what arrived.
const remoteLatest = 3

// RemoteChange is what origin has on a branch that the project's copy does not. It is read from
// the last look and never saved.
type RemoteChange struct {
	// Repo is the folder name of one of the project's other repositories, empty for the project's own.
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch"`
	// Behind counts the commits origin has that the local branch does not; Ahead the ones only the
	// local branch has (the two together mean the branches diverged).
	Behind int `json:"behind"`
	Ahead  int `json:"ahead,omitempty"`
	// Commit is the newest commit on origin, and what tells one change from the next.
	Commit string `json:"commit"`
	// Latest are the subjects of the newest commits, newest first.
	Latest []string `json:"latest,omitempty"`
}

// remoteWatchState is what the gateway knows about origin's news.
type remoteWatchState struct {
	mu      sync.Mutex
	changes map[string][]RemoteChange
	// alerted is the commit each repository was last announced at: the same state is told once.
	alerted map[string]string
}

// of is the changes of the project found by the last look.
func (r *remoteWatchState) of(id string) []RemoteChange {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changes[id]
}

// record keeps the changes of a project and returns the ones not announced yet, marking them as told.
func (r *remoteWatchState) record(id string, found []RemoteChange) (fresh []RemoteChange) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.changes == nil {
		r.changes, r.alerted = map[string][]RemoteChange{}, map[string]string{}
	}
	if len(found) == 0 {
		delete(r.changes, id)
	} else {
		r.changes[id] = found
	}
	seen := map[string]bool{}
	for _, c := range found {
		key := id + "/" + c.Repo
		seen[key] = true
		if r.alerted[key] != c.Commit {
			r.alerted[key] = c.Commit
			fresh = append(fresh, c)
		}
	}
	// A repository that caught up forgets what it was told, so the next news is news.
	for key := range r.alerted {
		if len(key) > len(id) && key[:len(id)+1] == id+"/" && !seen[key] {
			delete(r.alerted, key)
		}
	}
	return fresh
}

// startRemoteWatch runs the periodic look. Zero Options.RemoteWatchInterval is the default and a
// negative one turns it off, like the update checker's.
func (s *Server) startRemoteWatch() {
	interval := s.opts.RemoteWatchInterval
	if interval == 0 {
		interval = remoteWatchInterval
	}
	if interval < 0 {
		return
	}
	first := min(remoteWatchFirstDelay, interval)
	s.remoteWG.Go(func() {
		timer := time.NewTimer(first)
		defer timer.Stop()
		for {
			select {
			case <-s.baseCtx.Done():
				return
			case <-timer.C:
				s.lookForRemoteChanges(s.baseCtx)
				timer.Reset(interval)
			}
		}
	})
}

// lookForRemoteChanges asks origin about every project, one after the other. A repository shared
// by several projects is asked once.
func (s *Server) lookForRemoteChanges(ctx context.Context) {
	asked := map[string]error{}
	for _, p := range s.allProjects() {
		if ctx.Err() != nil {
			return
		}
		s.watchRemote(ctx, &p, asked)
	}
}

// watchRemote looks at one project. asked holds the repositories already fetched in this round
// (nil: fetch none, only compare with what the last fetch brought, as after an update).
func (s *Server) watchRemote(ctx context.Context, p *Project, asked map[string]error) {
	type target struct{ repo, dir, branch string }
	var targets []target
	if main := mainBranchOf(ctx, p); main != "" && gitx.Repo(ctx, p.Dir) == nil {
		targets = append(targets, target{"", p.Dir, main})
	}
	for _, repo := range p.Repos {
		targets = append(targets, target{repo.Name, repo.Dir, cmp.Or(repo.MainBranch, defaultMainBranch)})
	}
	var found []RemoteChange
	for _, t := range targets {
		key := t.dir + "\x00" + t.branch
		if asked != nil {
			if _, done := asked[key]; !done {
				fctx, cancel := context.WithTimeout(ctx, remoteWatchTimeout)
				asked[key] = gitx.FetchRemoteBranch(fctx, t.dir, t.branch)
				cancel()
			}
			if asked[key] != nil {
				continue // offline or refused: say nothing, the next look tries again
			}
		}
		commits, ahead, err := gitx.RemoteDrift(ctx, t.dir, t.branch)
		if err != nil || len(commits) == 0 {
			continue
		}
		change := RemoteChange{Repo: t.repo, Branch: t.branch, Behind: len(commits), Ahead: ahead, Commit: commits[0].SHA}
		for _, c := range commits[:min(len(commits), remoteLatest)] {
			change.Latest = append(change.Latest, c.Subject)
		}
		found = append(found, change)
	}
	fresh := s.remoteWatch.record(p.ID, found)
	if asked == nil {
		return // an update just ran: the person is looking at it, nothing to announce
	}
	for _, c := range fresh {
		s.announceRemoteChange(p, c)
	}
}

// announceRemoteChange pushes the news to the phones.
func (s *Server) announceRemoteChange(p *Project, c RemoteChange) {
	name := p.Title
	if c.Repo != "" {
		name += " / " + c.Repo
	}
	s.notifyAutopilot("motita-remote-"+p.ID, "New changes on origin",
		"%s has %d new commit(s) on origin. Update the main branch to bring them in, or review them first.",
		name, c.Behind)
}

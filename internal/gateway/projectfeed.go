package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// PROJECT FEED. What a project shows (branch, changes, worktrees, news on origin, nested repos)
// is read live from git when the page asks, but nothing told the page when it changed, so a file
// edited or a branch switched outside the page stayed unseen until the next action. The feed is
// a server-sent stream: while at least one page is connected the gateway looks at the projects
// every projectFeedInterval and, only when what GET /v1/projects would answer differs from the
// last look, sends one "projects" event. The page answers by reading the projects again.

// projectFeedInterval is how often the projects are compared while a page is listening.
const projectFeedInterval = 5 * time.Second

// projectFeed is the pages listening and the one goroutine that looks for them.
type projectFeed struct {
	mu     sync.Mutex
	subs   map[chan struct{}]struct{}
	cancel context.CancelFunc
	nudge  chan struct{}
}

// subscribe registers a listener; the returned function removes it, and stops the look when it
// was the last one. The channel holds at most one pending "something changed".
func (f *projectFeed) subscribe(s *Server) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subs == nil {
		f.subs = map[chan struct{}]struct{}{}
	}
	f.subs[ch] = struct{}{}
	if len(f.subs) == 1 {
		ctx, cancel := context.WithCancel(context.Background())
		f.cancel = cancel
		f.nudge = make(chan struct{}, 1)
		go f.watch(ctx, s, f.nudge)
	}
	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.subs, ch)
		if len(f.subs) == 0 && f.cancel != nil {
			f.cancel()
			f.cancel = nil
		}
	}
}

// kick asks for a look now, for the moments the gateway knows something moved (a run ended, news
// from origin).
func (f *projectFeed) kick() {
	f.mu.Lock()
	n := f.nudge
	f.mu.Unlock()
	if n == nil {
		return
	}
	select {
	case n <- struct{}{}:
	default:
	}
}

// watch compares the projects until ctx ends and tells every listener when they differ.
func (f *projectFeed) watch(ctx context.Context, s *Server, nudge <-chan struct{}) {
	last := projectsSignature(s)
	tick := time.NewTicker(projectFeedInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-nudge:
		}
		if sig := projectsSignature(s); sig != last {
			last = sig
			f.broadcast()
		}
	}
}

func (f *projectFeed) broadcast() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// projectsSignature is a digest of what GET /v1/projects answers; "" when it cannot be read.
func projectsSignature(s *Server) string {
	all, err := s.projectViews()
	if err != nil {
		return ""
	}
	b, err := json.Marshal(all)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return string(sum[:])
}

// handleProjectEvents streams "projects" events: the projects changed, read them again.
func (s *Server) handleProjectEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	if err := startStream(w, rc); err != nil {
		return
	}
	changed, unsubscribe := s.remoteWatch.feed.subscribe(s)
	defer unsubscribe()
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-changed:
			if writeEvent(w, rc, 0, "projects", map[string]bool{"changed": true}) != nil {
				return
			}
		case <-keepalive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

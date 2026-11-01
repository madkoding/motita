package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sort"
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
var projectFeedInterval = 5 * time.Second

// projectFeedKeepalive is how often a quiet stream writes a comment, so proxies keep it open.
var projectFeedKeepalive = 30 * time.Second

// projectFeed is the pages listening and the one goroutine that looks for them.
type projectFeed struct {
	mu     sync.Mutex
	subs   map[chan struct{}]struct{}
	cancel context.CancelFunc
	nudge  chan struct{}
	// sign is the digest the feed compares, and every how often it looks. Left zero they are the
	// projects' (projectsSignature, projectFeedInterval); the sessions feed sets its own.
	sign  func(*Server) string
	every *time.Duration
}

// signature is the digest of what this feed watches.
func (f *projectFeed) signature(s *Server) string {
	if f.sign != nil {
		return f.sign(s)
	}
	return projectsSignature(s)
}

// interval is how often this feed looks while a page listens.
func (f *projectFeed) interval() time.Duration {
	if f.every != nil {
		return *f.every
	}
	return projectFeedInterval
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
	last := f.signature(s)
	tick := time.NewTicker(f.interval())
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-nudge:
		}
		if sig := f.signature(s); sig != last {
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

// projectsSignature is a digest of what GET /v1/projects answers.
func projectsSignature(s *Server) string {
	all, _ := s.projectViews() // an unreadable store is "null": the next readable look differs
	b, _ := json.Marshal(all)
	sum := sha256.Sum256(b)
	return string(sum[:])
}

// handleProjectEvents streams "projects" events: the projects changed, read them again.
func (s *Server) handleProjectEvents(w http.ResponseWriter, r *http.Request) {
	s.streamFeed(w, r, &s.remoteWatch.feed, "projects")
}

// streamFeed writes one event named name each time the feed sees a change, and a comment on a
// timer so proxies keep a quiet stream open, until the page goes away.
func (s *Server) streamFeed(w http.ResponseWriter, r *http.Request, f *projectFeed, name string) {
	s.streamFeeds(w, r, map[string]*projectFeed{name: f})
}

// streamFeeds is streamFeed over several feeds on one connection: each change is an event named
// after the feed that saw it.
func (s *Server) streamFeeds(w http.ResponseWriter, r *http.Request, feeds map[string]*projectFeed) {
	rc := http.NewResponseController(w)
	if err := startStream(w, rc); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	changed := &feedChanges{signal: make(chan struct{}, 1), names: map[string]bool{}}
	for name, f := range feeds {
		ch, unsubscribe := f.subscribe(s)
		defer unsubscribe()
		go changed.forward(ctx, ch, name)
	}
	keepalive := time.NewTicker(projectFeedKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed.signal:
			for _, name := range changed.take() {
				if writeEvent(w, rc, 0, name, map[string]bool{"changed": true}) != nil {
					return
				}
			}
		case <-keepalive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// feedChanges are the feeds that saw a change the stream has not written yet. A feed that changes
// twice before the stream writes is one event, as it is on a single feed.
type feedChanges struct {
	mu     sync.Mutex
	names  map[string]bool
	signal chan struct{}
}

// forward marks name as changed each time its feed says so, until ctx ends.
func (c *feedChanges) forward(ctx context.Context, from <-chan struct{}, name string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-from:
			c.mu.Lock()
			c.names[name] = true
			c.mu.Unlock()
			select {
			case c.signal <- struct{}{}:
			default:
			}
		}
	}
}

// take returns the changed feeds, in name order, and forgets them.
func (c *feedChanges) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.names))
	for n := range c.names {
		names = append(names, n)
	}
	clear(c.names)
	sort.Strings(names)
	return names
}

package gateway

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// SESSION FEED. A page learned what the other pages did only by polling the session list, and it
// polled only while a run of its own was in flight: a phone that sat on a conversation while a
// laptop asked something in it never saw the turn start, never saw it end, and showed the old
// conversation until it was reloaded. The feed is the projects feed's twin: while a page listens,
// the gateway compares a small digest of the sessions (id, title, running, rev) every
// sessionFeedInterval and sends one "sessions" event when it differs. The page reads the list
// again, and reads the conversation on screen again when its rev moved.

// sessionFeedInterval is how often the sessions are compared while a page is listening. The digest
// reads a few fields under each session's lock and touches no disk, so the look is cheap.
var sessionFeedInterval = 2 * time.Second

// sessionsSignature is a digest of what a page needs to notice another page's work: which sessions
// exist, their titles, whether they run, and how many times a turn started or ended in them.
func sessionsSignature(s *Server) string {
	all := s.snapshot()
	lines := make([]string, 0, len(all))
	for _, c := range all {
		if c.dormant() {
			continue
		}
		c.stateMu.Lock()
		lines = append(lines, fmt.Sprintf("%s\x00%s\x00%t\x00%d", c.id, c.title, c.running, c.rev))
		c.stateMu.Unlock()
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return string(sum[:])
}

// handleSessionEvents streams "sessions" events: the sessions changed, read them again.
func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	s.streamFeed(w, r, &s.sessionFeed, "sessions")
}

// handleEvents is every change feed on one stream ("projects" and "sessions" events), so a page
// keeps one connection open instead of one per feed. The single-feed routes stay for older pages.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s.streamFeeds(w, r, map[string]*projectFeed{"projects": &s.remoteWatch.feed, "sessions": &s.sessionFeed})
}

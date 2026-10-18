package gateway

import (
	"context"
	"time"
)

// updateCheckInterval is how often the background checker asks GitHub whether
// a newer release exists. One hour: often enough that a user sees a new
// release the same day, rare enough that GitHub's rate limit is never a
// concern (the unauthenticated limit is 60 requests/hour).
const updateCheckInterval = time.Hour

// updateCacheTTL is how long a cached update check is served as is. Shorter than the
// frontend's poll (ten minutes), so each poll sees the release GitHub lists now, and
// still far from the unauthenticated limit of 60 requests/hour.
const updateCacheTTL = 5 * time.Minute

// startUpdateChecker runs a goroutine that periodically checks for a newer
// release and caches the result. The cache is what handleUpdateCheck returns
// to the frontend while it is younger than updateCacheTTL.
func (s *Server) startUpdateChecker() {
	if s.updater == nil {
		return
	}
	// Resolved here for the same reason Options.Heartbeat is: zero means the default, negative
	// means off, and the decision is made once. Negative is what a test needs so that no goroutine
	// ever polls GitHub, and a test that asserted "the gateway did not call the network" would
	// otherwise have to wait for an hour to say so.
	interval := s.opts.UpdateCheckInterval
	if interval == 0 {
		interval = updateCheckInterval
	}
	if interval < 0 {
		return
	}
	// The first check waits for the gateway to finish binding. Five seconds is the production
	// figure, capped by the interval so that a test which shortens the interval to milliseconds is
	// not left waiting five seconds for a check that was supposed to be quick.
	firstDelay := 5 * time.Second
	if interval < firstDelay {
		firstDelay = interval
	}
	go func() {
		time.Sleep(firstDelay)
		s.runOneCheck()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.baseCtx.Done():
				return
			case <-ticker.C:
				s.runOneCheck()
			}
		}
	}()
}

// runOneCheck performs one update check and caches the result.
func (s *Server) runOneCheck() {
	ctx, cancel := context.WithTimeout(s.baseCtx, 15*time.Second)
	defer cancel()
	s.storeCheck(s.updater.Check(ctx))
}

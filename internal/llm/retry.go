package llm

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// verdict is what a failed attempt leads to.
type verdict int

const (
	// verdictRetry: wait the returned pause and ask again.
	verdictRetry verdict = iota
	// verdictFatal: asking again cannot help.
	verdictFatal
	// verdictExhausted: the budget for this kind of failure is spent.
	verdictExhausted
)

// retryState is the retry policy of one call: how long the next pause is and how much of each
// budget the failures so far have spent.
//
// Two budgets, because two kinds of failure say different things. A failure of the connection
// or an overloaded provider (transient) says nothing about the request, so it does not spend
// llm.max_attempts; it is bounded by the time spent waiting for it (llm.transient_budget).
// Anything else that is worth asking again for spends one attempt.
type retryState struct {
	c        *Client
	attempts int           // failures that spent llm.max_attempts
	wait     time.Duration // the backoff before the next pause is jittered
	spent    time.Duration // time paused for transient failures
	last     error
}

func (c *Client) newRetry() *retryState {
	return &retryState{c: c, wait: c.cfg.BackoffInitial}
}

// after judges the failed attempt err and, to retry, the pause before the next one.
func (s *retryState) after(err error) (time.Duration, verdict) {
	s.last = err
	if !retryable(err) {
		return 0, verdictFatal
	}
	pause := s.pause(err)
	if budget := s.c.cfg.TransientBudget; budget > 0 && transient(err) {
		if s.spent+pause > budget {
			return 0, verdictExhausted
		}
		s.spent += pause
		return pause, verdictRetry
	}
	s.attempts++
	if s.attempts >= s.c.cfg.MaxAttempts {
		return 0, verdictExhausted
	}
	return pause, verdictRetry
}

// pause is the wait before the next attempt: what the provider asked for in Retry-After when
// it did and the configuration honours it, else the backoff with its jitter. The backoff
// grows either way.
func (s *retryState) pause(err error) time.Duration {
	base := s.wait
	s.wait = s.c.nextWait(s.wait)
	if ask := s.c.cfg.RetryAfterMax; ask > 0 {
		var he *HTTPError
		if errors.As(err, &he) && he.RetryAfter > 0 {
			return min(he.RetryAfter, ask)
		}
	}
	return s.c.jittered(base)
}

// exhausted is the error of a call that ran out of its budget.
func (s *retryState) exhausted() error {
	if budget := s.c.cfg.TransientBudget; budget > 0 && transient(s.last) {
		return fmt.Errorf("the provider stayed unavailable for %s: %w", budget, s.last)
	}
	return fmt.Errorf("all %d attempts were exhausted: %w", s.c.cfg.MaxAttempts, s.last)
}

// jittered is d reduced by up to llm.jitter of itself at random, so clients that failed together
// do not all come back together. The pause never exceeds d, so llm.backoff_max still bounds it.
func (c *Client) jittered(d time.Duration) time.Duration {
	j := c.cfg.Jitter
	if j <= 0 {
		return d
	}
	draw := rand.Float64
	if c.rand != nil {
		draw = c.rand
	}
	return d - time.Duration(float64(d)*min(j, 1)*draw())
}

// transient says whether err is the connection or the provider's load rather than the request:
// a network failure, or a status that means "busy, come back".
func transient(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		switch he.Code {
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
			http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout,
			529: // Anthropic's "overloaded"
			return true
		default:
			return false
		}
	}
	var nf *netFailure
	var ne net.Error
	return errors.As(err, &nf) || errors.As(err, &ne)
}

// parseRetryAfter reads a Retry-After header: a number of seconds or an HTTP date. It returns 0
// when the header is absent, unreadable or already past.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs <= 0 || secs > float64(24*time.Hour/time.Second) {
			return 0
		}
		return time.Duration(secs * float64(time.Second))
	}
	if at, err := http.ParseTime(v); err == nil {
		return max(at.Sub(now), 0)
	}
	return 0
}

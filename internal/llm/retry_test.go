package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

const okBody = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

// failThenAnswer serves `failures` responses with status, then a good answer.
func failThenAnswer(status, failures int, header map[string]string) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if int(n.Add(1)) <= failures {
			for k, v := range header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(status)
			fmt.Fprint(w, "busy")
			return
		}
		fmt.Fprint(w, okBody)
	}))
	return srv, &n
}

func TestOverloadDoesNotSpendMaxAttempts(t *testing.T) {
	srv, n := failThenAnswer(http.StatusServiceUnavailable, 4, nil)
	defer srv.Close()
	c := testClient(t, "openai", srv.URL, func(l *config.LLM) {
		l.MaxAttempts = 2
		l.TransientBudget = time.Minute
	})
	got, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil || got != "ok" {
		t.Fatalf("got %q, %v; want ok after 4 overloads with max_attempts 2", got, err)
	}
	if n.Load() != 5 {
		t.Errorf("requests = %d, want 5", n.Load())
	}
}

func TestOverloadSpendsMaxAttemptsWhenBudgetIsOff(t *testing.T) {
	srv, _ := failThenAnswer(http.StatusServiceUnavailable, 4, nil)
	defer srv.Close()
	c := testClient(t, "openai", srv.URL, func(l *config.LLM) { l.MaxAttempts = 2 })
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "all 2 attempts were exhausted") {
		t.Fatalf("err = %v, want attempts exhausted", err)
	}
}

func TestTransientBudgetEndsTheCall(t *testing.T) {
	srv, _ := failThenAnswer(http.StatusBadGateway, 1000, nil)
	defer srv.Close()
	c := testClient(t, "openai", srv.URL, func(l *config.LLM) {
		l.MaxAttempts = 2
		l.BackoffInitial, l.BackoffMax = time.Millisecond, time.Millisecond
		l.TransientBudget = 5 * time.Millisecond
	})
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "stayed unavailable") {
		t.Fatalf("err = %v, want the provider stayed unavailable", err)
	}
}

func TestOtherFailuresStillSpendMaxAttempts(t *testing.T) {
	srv, n := failThenAnswer(http.StatusInternalServerError, 1000, nil)
	defer srv.Close()
	c := testClient(t, "openai", srv.URL, func(l *config.LLM) {
		l.MaxAttempts = 3
		l.TransientBudget = time.Minute
	})
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "all 3 attempts were exhausted") || n.Load() != 3 {
		t.Fatalf("err = %v after %d requests, want 3 attempts", err, n.Load())
	}
}

func TestToolsAndStreamShareThePolicy(t *testing.T) {
	srv, _ := failThenAnswer(http.StatusTooManyRequests, 3, nil)
	defer srv.Close()
	c := testClient(t, "openai", srv.URL, func(l *config.LLM) {
		l.MaxAttempts = 1
		l.TransientBudget = time.Minute
	})
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("CompleteTools: %v", err)
	}

	srv2, _ := failThenAnswer(http.StatusTooManyRequests, 1000, nil)
	defer srv2.Close()
	c2 := testClient(t, "openai", srv2.URL, func(l *config.LLM) {
		l.BackoffMax = time.Millisecond
		l.TransientBudget = 5 * time.Millisecond
	})
	var last StreamChunk
	for ch := range c2.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		last = ch
	}
	if last.Event != StreamError || !strings.Contains(last.Error.Error(), "stayed unavailable") {
		t.Fatalf("last chunk = %+v, want the budget error", last)
	}
}

func TestRetryAfterIsHonouredAndCapped(t *testing.T) {
	s := &retryState{c: &Client{cfg: config.LLM{
		MaxAttempts: 9, BackoffInitial: time.Second, BackoffMax: 4 * time.Second, RetryAfterMax: 30 * time.Second,
	}}}
	s.wait = time.Second
	err := &HTTPError{Code: 429, RetryAfter: 7 * time.Second}
	if p, v := s.after(err); v != verdictRetry || p != 7*time.Second {
		t.Errorf("pause = %s (%v), want Retry-After 7s", p, v)
	}
	if p, _ := s.after(&HTTPError{Code: 429, RetryAfter: time.Hour}); p != 30*time.Second {
		t.Errorf("pause = %s, want the 30s cap", p)
	}
	// Without the header, or with it ignored, the backoff applies: it kept growing meanwhile.
	if p, _ := s.after(&HTTPError{Code: 429}); p != 4*time.Second {
		t.Errorf("pause = %s, want the backoff (4s)", p)
	}
	s.c.cfg.RetryAfterMax = 0
	if p, _ := s.after(&HTTPError{Code: 429, RetryAfter: 7 * time.Second}); p != 4*time.Second {
		t.Errorf("pause = %s, want the backoff when Retry-After is off", p)
	}
}

func TestRetryAfterHeaderReachesTheError(t *testing.T) {
	srv, _ := failThenAnswer(http.StatusTooManyRequests, 1, map[string]string{"Retry-After": "3"})
	defer srv.Close()
	c := testClient(t, "openai", srv.URL, nil)
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	var he *HTTPError
	if !errors.As(err, &he) || he.RetryAfter != 3*time.Second {
		t.Fatalf("err = %v, want an HTTPError with RetryAfter 3s", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":          0,
		"  ":        0,
		"5":         5 * time.Second,
		"1.5":       1500 * time.Millisecond,
		"0":         0,
		"-3":        0,
		"999999999": 0,
		"soon":      0,
		now.Add(9 * time.Second).Format(http.TimeFormat): 9 * time.Second,
		now.Add(-time.Minute).Format(http.TimeFormat):    0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestJitterCutsTheBackoff(t *testing.T) {
	c := &Client{cfg: config.LLM{Jitter: 0.5}, rand: func() float64 { return 0.5 }}
	if got := c.jittered(8 * time.Second); got != 6*time.Second {
		t.Errorf("jittered = %s, want 6s", got)
	}
	c.cfg.Jitter = 5 // beyond 1 it is 1: never below zero
	c.rand = func() float64 { return 0.999999 }
	if got := c.jittered(time.Second); got < 0 || got > time.Millisecond {
		t.Errorf("jittered = %s, want ~0", got)
	}
	c.cfg.Jitter = 0
	if got := c.jittered(time.Second); got != time.Second {
		t.Errorf("jitter off: %s, want 1s", got)
	}
	c.cfg.Jitter, c.rand = 0.5, nil // the runtime's generator stays within [d/2, d]
	for range 50 {
		if got := c.jittered(time.Second); got < 500*time.Millisecond || got > time.Second {
			t.Fatalf("jittered = %s outside [0.5s, 1s]", got)
		}
	}
}

func TestTransient(t *testing.T) {
	timeout := Explain(&net.DNSError{IsTimeout: true})
	cases := []struct {
		err  error
		want bool
	}{
		{&HTTPError{Code: 429}, true},
		{&HTTPError{Code: 503}, true},
		{&HTTPError{Code: 529}, true},
		{&HTTPError{Code: 408}, true},
		{&HTTPError{Code: 500}, false},
		{&HTTPError{Code: 400}, false},
		{timeout, true},
		{&net.OpError{Op: "dial"}, true},
		{errors.New("empty reply"), false},
	}
	for _, tc := range cases {
		if got := transient(tc.err); got != tc.want {
			t.Errorf("transient(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

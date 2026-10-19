package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/llm"
)

// fakeClaudeProbe counts the questions the panel had claude asked, until the test ends.
func fakeClaudeProbe(t *testing.T) chan struct{} {
	t.Helper()
	asked := make(chan struct{}, 10)
	old := probeClaudeQuota
	probeClaudeQuota = func(context.Context) error {
		asked <- struct{}{}
		return nil
	}
	t.Cleanup(func() { probeClaudeQuota = old })
	return asked
}

// probed reports whether claude was asked, waiting a little for the background question.
func probed(asked chan struct{}) bool {
	select {
	case <-asked:
		return true
	case <-time.After(time.Second):
		return false
	}
}

func TestThePanelAsksClaudeForItsQuotaOnceAndAgainWhenItGoesStale(t *testing.T) {
	asked := fakeClaudeProbe(t)
	srv := usageServer(t)
	limits := &llm.RateLimits{}
	srv.usageTotals = func() []llm.ProviderUsage {
		return []llm.ProviderUsage{{Provider: "claude-code", Calls: 1, Limits: limits}}
	}
	readUsage(t, srv)
	if !probed(asked) {
		t.Fatal("no quota yet, and claude was not asked")
	}
	readUsage(t, srv)
	if probed(asked) {
		t.Error("claude was asked again right away")
	}
	// A quota claude stated a moment ago needs no question, even once the last one is old.
	srv.claudeProbe.mu.Lock()
	srv.claudeProbe.last = time.Now().Add(-time.Hour)
	srv.claudeProbe.mu.Unlock()
	limits.Quotas, limits.At = []llm.Quota{{Name: "session", Used: 10}}, time.Now()
	readUsage(t, srv)
	if probed(asked) {
		t.Error("a fresh quota made claude be asked")
	}
	limits.At = time.Now().Add(-claudeQuotaFresh)
	readUsage(t, srv)
	if !probed(asked) {
		t.Error("a stale quota did not make claude be asked")
	}
}

func TestThePanelAsksNoClaudeThatIsNotUsed(t *testing.T) {
	asked := fakeClaudeProbe(t)
	readUsage(t, usageServer(t))
	if probed(asked) {
		t.Error("claude was asked though nothing uses it")
	}
}

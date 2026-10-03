package llm

import (
	"context"
	"sync"
)

// TOKENS SPENT, per agent.
//
// Nothing in motita counted them: the only figure on screen was an estimate of the conversation's
// size, so a run that spent a million tokens re-reading files looked the same as one that spent
// ten thousand. With agents running side by side the question "which one is spending what" has no
// answer without a real count, and every provider already reports one in its response.

// Usage is what one or more calls consumed, as the provider reported it.
type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read,omitempty"`
	CacheWrite int64 `json:"cache_write,omitempty"`
}

// Total is every token the calls moved, cached ones included.
func (u Usage) Total() int64 { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

// Add returns the sum of u and o.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		Input:      u.Input + o.Input,
		Output:     u.Output + o.Output,
		CacheRead:  u.CacheRead + o.CacheRead,
		CacheWrite: u.CacheWrite + o.CacheWrite,
	}
}

// Meter accumulates the usage of every call made with it in the context. It is safe for
// concurrent use: agents running in parallel each have their own, and one agent's calls may
// overlap (a streamed phase and a retry).
type Meter struct {
	mu    sync.Mutex
	usage Usage
	calls int64
}

// Add records one call's usage.
func (m *Meter) Add(u Usage) {
	m.mu.Lock()
	m.usage = m.usage.Add(u)
	m.calls++
	m.mu.Unlock()
}

// Usage is what the calls recorded so far consumed.
func (m *Meter) Usage() Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usage
}

// Calls is how many calls have been recorded.
func (m *Meter) Calls() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// meterKey carries the Meter of the agent making a call. See WithMeter.
type meterKey struct{}

// WithMeter makes every provider call made with ctx record its usage in m. A nil m is ignored.
func WithMeter(ctx context.Context, m *Meter) context.Context {
	if m == nil {
		return ctx
	}
	return context.WithValue(ctx, meterKey{}, m)
}

// recordUsage adds u to the Meter in ctx, if there is one. A call that reported nothing (a
// provider or server that does not send usage) records nothing, not a zero call.
func recordUsage(ctx context.Context, u Usage) {
	if u.Total() == 0 {
		return
	}
	if m, ok := ctx.Value(meterKey{}).(*Meter); ok {
		m.Add(u)
	}
}

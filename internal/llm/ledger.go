package llm

import (
	"sort"
	"sync"
	"time"
)

// TOKENS SPENT, per provider.
//
// A Meter answers "what did this agent spend"; this answers "what has each provider been asked for
// since motita started", which is what the bottom bars show. It counts only what a provider
// reported in its responses: a quota or a balance is the provider's to state, and none of the
// providers reports one in a response, so none is guessed here.

// ProviderUsage is what one provider's calls consumed since the process started.
type ProviderUsage struct {
	Provider string `json:"provider"`
	Usage    Usage  `json:"usage"`
	Calls    int64  `json:"calls"`
	// Last is when the latest call that reported usage finished.
	Last time.Time `json:"last"`
}

// ledger is the totals of every provider, safe for concurrent use.
type ledger struct {
	mu   sync.Mutex
	by   map[string]*ProviderUsage
	tick func() time.Time // injectable so a test does not read the clock
}

// totals is the process-wide ledger recordUsage writes to.
var totals = &ledger{by: map[string]*ProviderUsage{}, tick: time.Now}

// add records one call of provider.
func (l *ledger) add(provider string, u Usage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.by[provider]
	if p == nil {
		p = &ProviderUsage{Provider: provider}
		l.by[provider] = p
	}
	p.Usage = p.Usage.Add(u)
	p.Calls++
	p.Last = l.tick()
}

// snapshot is the totals so far, most recently used first.
func (l *ledger) snapshot() []ProviderUsage {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ProviderUsage, 0, len(l.by))
	for _, p := range l.by {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}

// ProviderTotals is what each provider has consumed since the process started, most recently used
// first. A provider that made no call that reported usage is not in it.
func ProviderTotals() []ProviderUsage { return totals.snapshot() }

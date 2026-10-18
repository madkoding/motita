package llm

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// TOKENS SPENT, per provider.
//
// A Meter answers "what did this agent spend"; this answers "what has each provider been asked for",
// which is what the bottom bars show. It counts what a provider reported in its responses and keeps
// the rate-limit window the provider last reported in its headers. A plan's quota or balance is the
// provider's to state, and none of them sends one with an answer, so none is guessed here. The totals
// are kept in a file once PersistUsage names one.

// usageSource is who a call is counted under.
type usageSource struct {
	provider string
	// local is a server of the user's own (a self-hosted Ollama): it has no quota to follow, so
	// the ledger does not count it.
	local bool
}

// Window is one rate limit a provider reported: how much the window allows and how much is left.
type Window struct {
	Limit     int64 `json:"limit"`
	Remaining int64 `json:"remaining"`
}

// Quota is a plan's allowance over a longer window, as a provider reports it: a share used, the
// length of the window and when it starts over. Name is "session" for the short window (hours),
// "week" for the weekly one and "month" for a monthly one (Copilot's premium requests). Status is
// set instead of a share when the provider only says where the usage stands: "ok" below its warning
// level, "warning" past it (a Claude subscription says no more than that until a threshold nears).
type Quota struct {
	Name     string     `json:"name"`
	Used     float64    `json:"used_percent"`
	Status   string     `json:"status,omitempty"`
	Minutes  int        `json:"window_minutes,omitempty"`
	ResetsAt *time.Time `json:"resets_at,omitempty"`
}

// RateLimits is what a provider reported about its limits with its latest answer: requests and
// tokens per minute, and the plan's session and weekly quotas when it sends them. What it did not
// report is nil or empty.
type RateLimits struct {
	Requests *Window   `json:"requests,omitempty"`
	Tokens   *Window   `json:"tokens,omitempty"`
	Quotas   []Quota   `json:"quotas,omitempty"`
	At       time.Time `json:"at"`
}

// ProviderUsage is what one provider's calls consumed.
type ProviderUsage struct {
	Provider string `json:"provider"`
	Usage    Usage  `json:"usage"`
	Calls    int64  `json:"calls"`
	// Since is when the totals began (the first call counted), Last when the latest finished.
	Since time.Time `json:"since"`
	Last  time.Time `json:"last"`
	// Limits is the rate-limit window of the latest answer that carried one.
	Limits *RateLimits `json:"limits,omitempty"`
}

// ledger is the totals of every provider, safe for concurrent use.
type ledger struct {
	mu   sync.Mutex
	by   map[string]*ProviderUsage
	tick func() time.Time // injectable so a test does not read the clock

	file    string
	pending bool
}

// totals is the process-wide ledger recordUsage writes to.
var totals = &ledger{by: map[string]*ProviderUsage{}, tick: time.Now}

// saveDelay is how long the file waits after a change, so a run of calls writes it once.
var saveDelay = 5 * time.Second

// entry is the provider's totals, created on first use. The caller holds l.mu.
func (l *ledger) entry(provider string) *ProviderUsage {
	p := l.by[provider]
	if p == nil {
		p = &ProviderUsage{Provider: provider, Since: l.tick()}
		l.by[provider] = p
	}
	return p
}

// add records one call.
func (l *ledger) add(src usageSource, u Usage) {
	if src.local {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.entry(src.provider)
	p.Usage = p.Usage.Add(u)
	p.Calls++
	p.Last = l.tick()
	l.changed()
}

// limits records the rate-limit window a provider reported.
func (l *ledger) limits(src usageSource, r RateLimits) {
	if src.local {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r.At = l.tick()
	l.entry(src.provider).Limits = &r
	l.changed()
}

// quota records one quota a provider reported, replacing the earlier one of the same window and
// keeping the provider's other limits.
func (l *ledger) quota(src usageSource, q Quota) {
	if src.local {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.entry(src.provider)
	if p.Limits == nil {
		p.Limits = &RateLimits{}
	}
	p.Limits.At = l.tick()
	quotas := make([]Quota, 0, len(p.Limits.Quotas)+1)
	for _, old := range p.Limits.Quotas {
		if old.Name != q.Name {
			quotas = append(quotas, old)
		}
	}
	p.Limits.Quotas = append(quotas, q)
	l.changed()
}

// changed schedules the file to be written. The caller holds l.mu.
func (l *ledger) changed() {
	if l.file == "" || l.pending {
		return
	}
	l.pending = true
	time.AfterFunc(saveDelay, func() { _ = l.flush() })
}

// flush writes the totals to the file, if there is one.
func (l *ledger) flush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending = false
	if l.file == "" {
		return nil
	}
	data, _ := json.Marshal(l.listLocked()) // plain structs and maps: they always encode
	tmp := l.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.file); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// load reads the file back into the ledger; a missing or damaged one is an empty ledger.
func (l *ledger) load(file string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.file = file
	data, err := os.ReadFile(file)
	if err != nil {
		return
	}
	var saved []ProviderUsage
	if json.Unmarshal(data, &saved) != nil {
		return
	}
	for i := range saved {
		p := saved[i]
		l.by[p.Provider] = &p
	}
}

// listLocked is the totals, most recently used first. The caller holds l.mu.
func (l *ledger) listLocked() []ProviderUsage {
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

// snapshot is the totals so far, most recently used first.
func (l *ledger) snapshot() []ProviderUsage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.listLocked()
}

// ProviderTotals is what each provider has consumed, most recently used first. A provider that
// made no call that reported usage or limits is not in it.
func ProviderTotals() []ProviderUsage {
	all := totals.snapshot()
	out := all[:0:0]
	for _, p := range all {
		if config.ReportsUsage(p.Provider) {
			out = append(out, p)
		}
	}
	return out
}

// PersistUsage keeps the totals in file: it reads what an earlier run left and, from then on,
// writes the totals a few seconds after they change. Without it they last as long as the process.
func PersistUsage(file string) { totals.load(file) }

// FlushUsage writes the totals now, for a process that is about to end.
func FlushUsage() error { return totals.flush() }

// windowFrom reads one window from the headers named; nil when the limit is not there.
func windowFrom(h http.Header, limit, remaining string) *Window {
	l, err := strconv.ParseInt(h.Get(limit), 10, 64)
	if err != nil {
		return nil
	}
	r, err := strconv.ParseInt(h.Get(remaining), 10, 64)
	if err != nil {
		return nil
	}
	return &Window{Limit: l, Remaining: r}
}

// weekMinutes is the length of the weekly window; anything shorter is the session's.
const weekMinutes = 7 * 24 * 60

// resetAt is the moment a window starts over: a Unix time, else seconds from now; nil if neither.
func resetAt(at, after string, now time.Time) *time.Time {
	if n, err := strconv.ParseInt(at, 10, 64); err == nil && n > 0 {
		t := time.Unix(n, 0).UTC()
		return &t
	}
	if n, err := strconv.ParseInt(after, 10, 64); err == nil && n >= 0 {
		t := now.Add(time.Duration(n) * time.Second).UTC()
		return &t
	}
	return nil
}

// codexQuota reads one quota from the Codex headers (x-codex-<which>-*): the share used, the
// window in minutes and when it starts over. The window says whether it is the session's or the
// week's; without one, primary is the session and secondary the week.
func codexQuota(h http.Header, which string, now time.Time) *Quota {
	key := "x-codex-" + which + "-"
	used, err := strconv.ParseFloat(h.Get(key+"used-percent"), 64)
	if err != nil {
		return nil
	}
	q := &Quota{Name: "session", Used: used}
	if which == "secondary" {
		q.Name = "week"
	}
	if m, err := strconv.Atoi(h.Get(key + "window-minutes")); err == nil && m > 0 {
		q.Minutes = m
		q.Name = "session"
		if m >= weekMinutes {
			q.Name = "week"
		}
	}
	q.ResetsAt = resetAt(h.Get(key+"reset-at"), h.Get(key+"reset-after-seconds"), now)
	return q
}

// unifiedQuota reads one window of Anthropic's subscription headers
// (anthropic-ratelimit-unified-5h-* and -7d-*), whose utilization is a fraction of 1.
func unifiedQuota(h http.Header, key, name string, minutes int) *Quota {
	key = "anthropic-ratelimit-unified-" + key + "-"
	u, err := strconv.ParseFloat(h.Get(key+"utilization"), 64)
	if err != nil {
		return nil
	}
	return &Quota{Name: name, Used: u * 100, Minutes: minutes, ResetsAt: resetAt(h.Get(key+"reset"), "", time.Time{})}
}

// quotasFrom reads the plan's session and weekly quotas: the headers of the Codex backend (a
// ChatGPT plan) and the subscription headers of Anthropic.
func quotasFrom(h http.Header, now time.Time) []Quota {
	var out []Quota
	for _, q := range []*Quota{
		codexQuota(h, "primary", now),
		codexQuota(h, "secondary", now),
		unifiedQuota(h, "5h", "session", 300),
		unifiedQuota(h, "7d", "week", weekMinutes),
	} {
		if q != nil {
			out = append(out, *q)
		}
	}
	return out
}

// limitsFrom reads what the headers of an answer say about the limits: the per-minute windows of
// OpenAI-style hosts (x-ratelimit-*) and Anthropic (anthropic-ratelimit-*), and the plan's
// quotas. It reports whether the answer carried any.
func limitsFrom(h http.Header, now time.Time) (RateLimits, bool) {
	r := RateLimits{
		Requests: windowFrom(h, "x-ratelimit-limit-requests", "x-ratelimit-remaining-requests"),
		Tokens:   windowFrom(h, "x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens"),
		Quotas:   quotasFrom(h, now),
	}
	if r.Requests == nil {
		r.Requests = windowFrom(h, "anthropic-ratelimit-requests-limit", "anthropic-ratelimit-requests-remaining")
	}
	if r.Tokens == nil {
		r.Tokens = windowFrom(h, "anthropic-ratelimit-tokens-limit", "anthropic-ratelimit-tokens-remaining")
	}
	return r, r.Requests != nil || r.Tokens != nil || len(r.Quotas) > 0
}

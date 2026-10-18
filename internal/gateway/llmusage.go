package gateway

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/oauth"
	"github.com/madkoding/motita/internal/onboard"
)

// copilotUserQuota reads a Copilot account's premium-request quota; a variable so a test needs no
// network.
var copilotUserQuota = oauth.CopilotUserQuota

// How long a Copilot quota read is kept, and how long a failed one: the endpoint is cached on
// GitHub's side too, and the page asks once a minute.
const (
	quotaKeep     = 5 * time.Minute
	quotaKeepFail = time.Minute
	quotaTimeout  = 5 * time.Second
)

// quotaCache is the last Copilot quota read, or the failure of it.
type quotaCache struct {
	mu    sync.Mutex
	at    time.Time
	token string
	quota *llm.Quota
}

// llmUsageRow is what one provider has consumed since the gateway started, as the bottom bars of
// the page show it. Only what the provider reported in its responses is here: its limits when it
// sends them (per minute, and a plan's session and weekly quotas), never a guess.
type llmUsageRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Default    bool   `json:"default"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cache_read"`
	CacheWrite int64  `json:"cache_write"`
	Calls      int64  `json:"calls"`
	// Since is when the count began, Last when the latest call that reported usage finished; both
	// are absent for a provider with no call yet.
	Since *time.Time `json:"since,omitempty"`
	Last  *time.Time `json:"last,omitempty"`
	// Limits is what the provider reported about its limits with its latest answer: requests and
	// tokens per minute, and the plan's session and weekly quotas. Absent when it reports none.
	Limits *llm.RateLimits `json:"limits,omitempty"`
}

// llmUsageView is what GET /v1/llm/usage answers.
type llmUsageView struct {
	Providers []llmUsageRow `json:"providers"`
}

// handleLLMUsage answers, for each provider with a key or a login (and any other that has been
// called), the tokens it has consumed since the gateway started.
func (s *Server) handleLLMUsage(w http.ResponseWriter, r *http.Request) {
	cfg := s.configuredLLM()
	totals := map[string]llm.ProviderUsage{}
	for _, u := range s.providerTotals() {
		totals[u.Provider] = u
	}
	rows := []llmUsageRow{}
	for _, p := range onboard.Providers() {
		if !config.ReportsUsage(p.ID) {
			continue // a self-hosted Ollama has no quota to follow
		}
		u, called := totals[p.ID]
		if !called && config.KeySource(s.opts.ConfigPath, cfg, p.ID) == config.KeyMissing {
			continue
		}
		row := llmUsageRow{ID: p.ID, Name: p.Name, Default: p.ID == cfg.Provider,
			Input: u.Usage.Input, Output: u.Usage.Output, CacheRead: u.Usage.CacheRead,
			CacheWrite: u.Usage.CacheWrite, Calls: u.Calls}
		if called {
			since, last := u.Since, u.Last
			if u.Calls > 0 {
				row.Since, row.Last = &since, &last
			}
			row.Limits = u.Limits
		}
		if p.ID == "copilot" {
			row.Limits = withQuota(row.Limits, s.copilotQuota(r.Context(), cfg))
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, llmUsageView{Providers: rows})
}

// providerTotals is what each provider has consumed so far: the process-wide ledger, unless a test
// put its own.
func (s *Server) providerTotals() []llm.ProviderUsage {
	if s.usageTotals != nil {
		return s.usageTotals()
	}
	return llm.ProviderTotals()
}

// withQuota adds q to the limits a provider reported, when there is one.
func withQuota(l *llm.RateLimits, q *llm.Quota) *llm.RateLimits {
	if q == nil {
		return l
	}
	out := llm.RateLimits{}
	if l != nil {
		out = *l
	}
	out.Quotas = append(append([]llm.Quota{}, out.Quotas...), *q)
	return &out
}

// copilotToken is the GitHub token behind the Copilot login (or the key configured for it), or "".
func copilotToken(cfg config.LLM) string {
	if cred, err := oauth.LoadCredential(config.AuthDir(), "copilot"); err == nil && cred.RefreshToken != "" {
		return cred.RefreshToken
	}
	if cfg.Provider == "copilot" {
		return cfg.APIKey
	}
	return ""
}

// copilotQuota is the month's premium-request quota of the Copilot account, read from GitHub and
// kept for a few minutes. It is nil when there is no login or GitHub says nothing: no quota is
// ever guessed.
func (s *Server) copilotQuota(ctx context.Context, cfg config.LLM) *llm.Quota {
	token := copilotToken(cfg)
	if token == "" {
		return nil
	}
	c := &s.quotaCache
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := quotaKeep
	if c.quota == nil {
		keep = quotaKeepFail
	}
	if c.token == token && time.Since(c.at) < keep {
		return c.quota
	}
	ctx, cancel := context.WithTimeout(ctx, quotaTimeout)
	defer cancel()
	c.token, c.at, c.quota = token, time.Now(), nil
	q, err := copilotUserQuota(ctx, nil, token)
	if err != nil || q.Unlimited {
		return nil
	}
	used := float64(q.Entitlement-q.Remaining) / float64(q.Entitlement) * 100
	c.quota = &llm.Quota{Name: "month", Used: used}
	if !q.ResetsAt.IsZero() {
		c.quota.ResetsAt = &q.ResetsAt
	}
	return c.quota
}

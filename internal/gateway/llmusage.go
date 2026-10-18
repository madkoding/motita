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

// ollamaQuotas reads an Ollama Cloud plan's quotas; a variable so a test needs no network.
var ollamaQuotas = llm.OllamaQuotas

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

// quotaEntry is one read of a provider's quotas, or the failure of it.
type quotaEntry struct {
	at     time.Time
	quotas []llm.Quota
}

// quotaCache keeps the last read of each provider's quotas, by provider and token.
type quotaCache struct {
	mu    sync.Mutex
	items map[string]quotaEntry
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
		row.Limits = withQuotas(row.Limits, s.planQuotas(r.Context(), s.opts.ConfigPath, cfg, p.ID))
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

// withQuotas adds qs to the limits a provider reported, when there are any.
func withQuotas(l *llm.RateLimits, qs []llm.Quota) *llm.RateLimits {
	if len(qs) == 0 {
		return l
	}
	out := llm.RateLimits{}
	if l != nil {
		out = *l
	}
	out.Quotas = append(append([]llm.Quota{}, out.Quotas...), qs...)
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

// planQuotas is the quotas of a provider's plan that the provider itself states over an API: the
// month's premium requests of Copilot, the included credits or the session and weekly limits of
// Ollama Cloud. It is nil for any other provider, and when there is no login or key or the provider
// says nothing: no quota is ever guessed.
func (s *Server) planQuotas(ctx context.Context, configPath string, cfg config.LLM, id string) []llm.Quota {
	switch id {
	case "copilot":
		token := copilotToken(cfg)
		return s.cachedQuotas(ctx, id, token, func(ctx context.Context) ([]llm.Quota, error) {
			return copilotMonth(ctx, token)
		})
	case "ollama", "ollama-cloud":
		key := config.ProviderKey(configPath, cfg, id)
		return s.cachedQuotas(ctx, "ollama", key, func(ctx context.Context) ([]llm.Quota, error) {
			return ollamaQuotas(ctx, nil, key)
		})
	}
	return nil
}

// copilotMonth is the month's premium-request quota of a Copilot account as one quota; none when the
// plan is unlimited.
func copilotMonth(ctx context.Context, token string) ([]llm.Quota, error) {
	q, err := copilotUserQuota(ctx, nil, token)
	if err != nil || q.Unlimited {
		return nil, err
	}
	month := llm.Quota{Name: "month", Used: float64(q.Entitlement-q.Remaining) / float64(q.Entitlement) * 100}
	if !q.ResetsAt.IsZero() {
		month.ResetsAt = &q.ResetsAt
	}
	return []llm.Quota{month}, nil
}

// cachedQuotas reads quotas with read, keeping the answer for a few minutes (a minute after a
// failure) so the page's one-minute polling does not ask the provider each time. Without a token
// there is nothing to ask.
func (s *Server) cachedQuotas(ctx context.Context, id, token string,
	read func(context.Context) ([]llm.Quota, error)) []llm.Quota {
	if token == "" {
		return nil
	}
	c := &s.quotaCache
	c.mu.Lock()
	defer c.mu.Unlock()
	key := id + "|" + token
	keep := quotaKeep
	if e, ok := c.items[key]; ok {
		if len(e.quotas) == 0 {
			keep = quotaKeepFail
		}
		if time.Since(e.at) < keep {
			return e.quotas
		}
	}
	ctx, cancel := context.WithTimeout(ctx, quotaTimeout)
	defer cancel()
	qs, _ := read(ctx)
	if c.items == nil {
		c.items = map[string]quotaEntry{}
	}
	c.items[key] = quotaEntry{at: time.Now(), quotas: qs}
	return qs
}

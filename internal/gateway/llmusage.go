package gateway

import (
	"net/http"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/onboard"
)

// llmUsageRow is what one provider has consumed since the gateway started, as the bottom bars of
// the page show it. Only what the provider reported in its responses is here: no quota or balance,
// because none of the providers sends one with an answer.
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
	// CostUSD is an upper estimate of what the tokens cost at the catalogue's per-token prices
	// (every token that is not output is priced as input); CostPartial says some tokens had no
	// price, so the real figure is higher. Absent when no model used has a price.
	CostUSD     *float64 `json:"cost_usd,omitempty"`
	CostPartial bool     `json:"cost_partial,omitempty"`
	// Limits is the rate-limit window (requests and tokens per minute) the provider reported with
	// its latest answer, when it reports one. It is not a plan's quota.
	Limits *llm.RateLimits `json:"limits,omitempty"`
}

// llmUsageView is what GET /v1/llm/usage answers.
type llmUsageView struct {
	Providers []llmUsageRow `json:"providers"`
}

// handleLLMUsage answers, for each provider with a key or a login (and any other that has been
// called), the tokens it has consumed since the gateway started.
func (s *Server) handleLLMUsage(w http.ResponseWriter, _ *http.Request) {
	cfg := s.configuredLLM()
	totals := map[string]llm.ProviderUsage{}
	for _, u := range s.providerTotals() {
		totals[u.Provider] = u
	}
	rows := []llmUsageRow{}
	for _, p := range onboard.Providers() {
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
			row.CostUSD, row.CostPartial = costOf(p, u)
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

// costOf prices what a provider consumed, model by model, at the catalogue's USD per million
// tokens. It answers nil when no model used has a price.
func costOf(p onboard.Provider, u llm.ProviderUsage) (*float64, bool) {
	prices := map[string]onboard.Model{}
	for _, m := range p.Models {
		prices[m.ID] = m
	}
	var total float64
	priced, partial := false, false
	for model, use := range u.Models {
		m, ok := prices[model]
		if !ok || m.PriceIn == 0 || m.PriceOut == 0 {
			partial = partial || use.Total() > 0
			continue
		}
		priced = true
		in := float64(use.Input + use.CacheRead + use.CacheWrite)
		total += (in*m.PriceIn + float64(use.Output)*m.PriceOut) / 1e6
	}
	if !priced {
		return nil, false
	}
	return &total, partial
}

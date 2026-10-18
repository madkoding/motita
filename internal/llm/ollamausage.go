package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ollamaCloudURL is where Ollama's hosted service answers the balance call.
var ollamaCloudURL = "https://ollama.com"

// ollamaLimit is one legacy plan limit: what is left of it and when it starts over.
type ollamaLimit struct {
	RemainingPercent float64   `json:"remaining_percent"`
	ResetsAt         time.Time `json:"resets_at"`
}

// ollamaBalance is the part of GET /api/balance the quotas need: a current plan states its included
// credits for a period, a legacy one its session and weekly limits.
type ollamaBalance struct {
	Included struct {
		BalanceUSD   float64 `json:"balance_usd"`
		AllowanceUSD float64 `json:"allowance_usd"`
		Period       struct {
			From  time.Time `json:"from"`
			Until time.Time `json:"until"`
		} `json:"period"`
		Session *ollamaLimit `json:"session"`
		Weekly  *ollamaLimit `json:"weekly"`
	} `json:"included"`
}

// usedOf is the share used of something with `left` of `all` remaining, within 0 to 100.
func usedOf(all, left float64) float64 {
	used := (all - left) / all * 100
	return min(100, max(0, used))
}

// quotas turns the balance into the plan's quotas: the period's included credits, or the session
// and weekly limits of a legacy plan. It is empty when the reply states neither.
func (b ollamaBalance) quotas() []Quota {
	in := b.Included
	var out []Quota
	if in.AllowanceUSD > 0 && !in.Period.Until.IsZero() {
		until := in.Period.Until
		out = append(out, Quota{Name: "month", Used: usedOf(in.AllowanceUSD, in.BalanceUSD),
			Minutes: int(until.Sub(in.Period.From) / time.Minute), ResetsAt: &until})
	}
	if in.Session != nil {
		out = append(out, Quota{Name: "session", Used: usedOf(100, in.Session.RemainingPercent), ResetsAt: &in.Session.ResetsAt})
	}
	if in.Weekly != nil {
		out = append(out, Quota{Name: "week", Used: usedOf(100, in.Weekly.RemainingPercent), ResetsAt: &in.Weekly.ResetsAt})
	}
	return out
}

// OllamaQuotas reads what is left of an Ollama Cloud plan from GET /api/balance with the account's
// API key. A reply that states no quota is an error, never a guess.
func OllamaQuotas(ctx context.Context, client *http.Client, key string) ([]Quota, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaCloudURL+"/api/balance", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama cloud balance: %s", resp.Status)
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // a body cut short does not parse
	var b ollamaBalance
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	q := b.quotas()
	if len(q) == 0 {
		return nil, errors.New("ollama cloud reported no quota for this account")
	}
	return q, nil
}

package llm

import (
	"bufio"
	"strings"
	"testing"
)

func TestClaudeSubscriptionLimitsBecomeQuotas(t *testing.T) {
	five := 0.42
	q, ok := (&claudeRateLimit{RateLimitType: "five_hour", Utilization: &five, ResetsAt: 1792300000}).quota()
	if !ok || q.Name != "session" || q.Minutes != 300 || q.ResetsAt == nil {
		t.Errorf("five hours: %+v %v", q, ok)
	}
	if q.Used < 41.9 || q.Used > 42.1 {
		t.Errorf("five hours used: %v", q.Used)
	}
	over := 1.7
	q, ok = (&claudeRateLimit{RateLimitType: "seven_day", Utilization: &over, ResetsAt: 1792300000000}).quota()
	if !ok || q.Name != "week" || q.Used != 100 || q.ResetsAt.Year() != 2026 {
		t.Errorf("seven days, in milliseconds: %+v %v", q, ok)
	}
	q, ok = (&claudeRateLimit{RateLimitType: "five_hour", Status: "rejected"}).quota()
	if !ok || q.Used != 100 || q.ResetsAt != nil {
		t.Errorf("rejected: %+v %v", q, ok)
	}
	q, ok = (&claudeRateLimit{RateLimitType: "five_hour", Status: "allowed", ResetsAt: 1792300000}).quota()
	if !ok || q.Status != "ok" || q.Used != 0 || q.ResetsAt == nil {
		t.Errorf("below the warning level: %+v %v", q, ok)
	}
	q, ok = (&claudeRateLimit{RateLimitType: "seven_day", Status: "allowed_warning"}).quota()
	if !ok || q.Status != "warning" || q.Name != "week" {
		t.Errorf("past the warning level, no share: %+v %v", q, ok)
	}
	for name, r := range map[string]*claudeRateLimit{
		"another window":    {RateLimitType: "overage", Utilization: &five},
		"no status":         {RateLimitType: "five_hour"},
		"opus only window":  {RateLimitType: "seven_day_opus", Utilization: &five},
		"empty information": {},
	} {
		if q, ok := r.quota(); ok {
			t.Errorf("%s: a quota was invented: %+v", name, q)
		}
	}
}

func TestEveryWindowClaudeSendsBecomesAQuotaWithItsShare(t *testing.T) {
	var none *claudeRateLimit
	if got := none.quotas(); got != nil {
		t.Errorf("no event: %+v", got)
	}
	r := &claudeRateLimit{Status: "allowed", RateLimitType: "five_hour", UnifiedWindows: map[string]claudeWindow{
		"five_hour":                  {Utilization: 0.12, ResetsAt: 1792300000},
		"seven_day":                  {Utilization: 1.4},
		"seven_day_overage_included": {Utilization: 0.5},
	}}
	got := map[string]Quota{}
	for _, q := range r.quotas() {
		got[q.Name] = q
	}
	session, week := got["session"], got["week"]
	if len(got) != 2 || session.Minutes != 300 || session.Status != "" || session.ResetsAt == nil {
		t.Fatalf("windows: %+v", got)
	}
	if session.Used < 11.9 || session.Used > 12.1 || week.Used != 100 || week.ResetsAt != nil {
		t.Errorf("shares: %+v", got)
	}
	// Without the windows, the one limit the event is about; without that either, nothing.
	single := (&claudeRateLimit{Status: "allowed", RateLimitType: "seven_day"}).quotas()
	if len(single) != 1 || single[0].Name != "week" || single[0].Status != "ok" {
		t.Errorf("single limit: %+v", single)
	}
	other := &claudeRateLimit{Status: "allowed", UnifiedWindows: map[string]claudeWindow{"overage": {}}}
	if q := other.quotas(); q != nil {
		t.Errorf("no window that is a quota: %+v", q)
	}
}

func TestARateLimitEventFromClaudeIsRecordedUnderClaudeCode(t *testing.T) {
	info := func(kind, rest string) string {
		return `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning",` +
			`"rateLimitType":"` + kind + `",` + rest + `}}` + "\n"
	}
	stream := info("five_hour", `"utilization":0.8,"resetsAt":1792300000`) +
		info("seven_day", `"utilization":0.3`) +
		`{"type":"assistant","rate_limit_info":{"rateLimitType":"five_hour","utilization":0.1}}` + "\n"
	p := &claudeProcess{out: bufio.NewScanner(strings.NewReader(stream))}
	for i := 0; i < 3; i++ {
		if _, err := p.event(); err != nil {
			t.Fatal(err)
		}
	}
	got := totalsOf("claude-code").Limits
	if got == nil || len(got.Quotas) != 2 {
		t.Fatalf("recorded %+v", got)
	}
	for _, q := range got.Quotas {
		if q.Name == "session" && (q.Used < 79.9 || q.Used > 80.1) {
			t.Errorf("a frame that is not a rate limit event changed the session: %+v", q)
		}
	}
	delete(totals.by, "claude-code")
}

func TestAQuotaReplacesTheEarlierOneOfItsWindowAndKeepsTheRest(t *testing.T) {
	l := newLedger(&quotaNow)
	l.limits(src("p"), RateLimits{Tokens: &Window{Limit: 9, Remaining: 8}})
	l.quota(src("p"), Quota{Name: "session", Used: 10})
	l.quota(src("p"), Quota{Name: "week", Used: 20})
	l.quota(src("p"), Quota{Name: "session", Used: 30})
	l.quota(usageSource{provider: "ollama", local: true}, Quota{Name: "week", Used: 1})
	got := l.snapshot()
	if len(got) != 1 || got[0].Limits.Tokens == nil || len(got[0].Limits.Quotas) != 2 {
		t.Fatalf("snapshot: %+v", got)
	}
	if got[0].Limits.Quotas[0].Name != "week" || got[0].Limits.Quotas[1].Used != 30 {
		t.Errorf("quotas: %+v", got[0].Limits.Quotas)
	}
}

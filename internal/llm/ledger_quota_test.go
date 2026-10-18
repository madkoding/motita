package llm

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

var quotaNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func headers(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestTheCodexSessionAndWeeklyQuotasAreRead(t *testing.T) {
	h := headers(
		"x-codex-primary-used-percent", "37.5", "x-codex-primary-window-minutes", "300",
		"x-codex-primary-reset-after-seconds", "7200",
		"x-codex-secondary-used-percent", "12", "x-codex-secondary-window-minutes", "10080",
		"x-codex-secondary-reset-at", "1792400000",
	)
	r, ok := limitsFrom(h, quotaNow)
	if !ok || len(r.Quotas) != 2 {
		t.Fatalf("quotas: %+v %v", r, ok)
	}
	s, w := r.Quotas[0], r.Quotas[1]
	if s.Name != "session" || s.Used != 37.5 || s.Minutes != 300 || !s.ResetsAt.Equal(quotaNow.Add(2*time.Hour)) {
		t.Errorf("session: %+v", s)
	}
	if w.Name != "week" || w.Used != 12 || w.Minutes != 10080 || !w.ResetsAt.Equal(time.Unix(1792400000, 0)) {
		t.Errorf("week: %+v", w)
	}
}

func TestACodexQuotaWithoutItsWindowIsNamedByPosition(t *testing.T) {
	r, ok := limitsFrom(headers("x-codex-primary-used-percent", "1", "x-codex-secondary-used-percent", "2"), quotaNow)
	if !ok || r.Quotas[0].Name != "session" || r.Quotas[1].Name != "week" {
		t.Errorf("by position: %+v", r.Quotas)
	}
	if r.Quotas[0].ResetsAt != nil || r.Quotas[0].Minutes != 0 {
		t.Errorf("nothing was reported about the window: %+v", r.Quotas[0])
	}
	// The window decides over the position: a long primary is the week.
	long, _ := limitsFrom(headers("x-codex-primary-used-percent", "1", "x-codex-primary-window-minutes", "10080"), quotaNow)
	if long.Quotas[0].Name != "week" {
		t.Errorf("window: %+v", long.Quotas[0])
	}
	if _, ok := limitsFrom(headers("x-codex-primary-used-percent", "lots"), quotaNow); ok {
		t.Error("an unreadable share is not a quota")
	}
}

func TestAnthropicSubscriptionWindowsAreRead(t *testing.T) {
	h := headers(
		"anthropic-ratelimit-unified-5h-utilization", "0.25", "anthropic-ratelimit-unified-5h-reset", "1792300000",
		"anthropic-ratelimit-unified-7d-utilization", "0.5",
	)
	r, ok := limitsFrom(h, quotaNow)
	if !ok || len(r.Quotas) != 2 {
		t.Fatalf("quotas: %+v %v", r, ok)
	}
	if q := r.Quotas[0]; q.Name != "session" || q.Used != 25 || q.Minutes != 300 || q.ResetsAt == nil {
		t.Errorf("5h: %+v", q)
	}
	if q := r.Quotas[1]; q.Name != "week" || q.Used != 50 || q.ResetsAt != nil {
		t.Errorf("7d: %+v", q)
	}
}

func TestAnAnswerWithoutAResetHasNoMoment(t *testing.T) {
	if resetAt("", "", quotaNow) != nil || resetAt("0", "-5", quotaNow) != nil {
		t.Error("no reset was reported")
	}
}

func TestASelfHostedServerIsNotCounted(t *testing.T) {
	l := newLedger(&quotaNow)
	local := usageSource{provider: "ollama", local: true}
	l.add(local, Usage{Input: 5})
	l.limits(local, RateLimits{Tokens: &Window{Limit: 1, Remaining: 1}})
	if got := l.snapshot(); len(got) != 0 {
		t.Errorf("a self-hosted server was counted: %+v", got)
	}
}

func TestOllamaOnTheUsersOwnServerIsNotCountedButTheCloudIs(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, `{"choices":[{"message":{"content":"ok"}}],`+openAIUsageJSON+`}`)
	before := totalsOf("ollama")
	own := clientFor(t, srv.URL, func(l *config.LLM) { l.Provider = "ollama"; l.BaseURL = srv.URL })
	if _, err := own.Complete(context.Background(), hi); err != nil {
		t.Fatal(err)
	}
	if after := totalsOf("ollama"); after.Calls != before.Calls {
		t.Errorf("the user's own Ollama was counted: %+v", after)
	}
	if !own.source().local {
		t.Error("a loopback Ollama is local")
	}
	cloud := clientFor(t, srv.URL, func(l *config.LLM) { l.Provider = "ollama"; l.BaseURL = "https://ollama.com/v1" })
	if cloud.source().local {
		t.Error("Ollama Cloud is not local")
	}
}

func TestPersistedUsageOfASelfHostedOllamaIsLeftOut(t *testing.T) {
	other := filepath.Join(t.TempDir(), "motita.yaml")
	if err := config.SetCredential(other, config.ProviderBaseURLVariable("ollama"), "http://localhost:11434/v1"); err != nil {
		t.Fatal(err)
	}
	config.UseCredentialsBeside(other)
	t.Cleanup(func() { config.UseCredentialsBeside(config.File()) })
	totals.add(usageSource{provider: "ollama"}, Usage{Input: 1})
	t.Cleanup(func() { delete(totals.by, "ollama") })
	if totalsOf("ollama").Calls != 0 {
		t.Error("a self-hosted Ollama is not listed")
	}
	if config.ReportsUsage("ollama") || !config.ReportsUsage("openai") {
		t.Error("only the self-hosted Ollama has no usage")
	}
}

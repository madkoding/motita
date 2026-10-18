package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func ollamaBalanceServer(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/balance" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("request: %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	old := ollamaCloudURL
	ollamaCloudURL = srv.URL
	t.Cleanup(func() { ollamaCloudURL = old; srv.Close() })
}

func TestACurrentOllamaPlanGivesItsIncludedCreditsAsAQuota(t *testing.T) {
	ollamaBalanceServer(t, 200, `{"included":{"balance_usd":72.5,"allowance_usd":100,`+
		`"period":{"from":"2026-09-15T09:30:00Z","until":"2026-10-15T09:30:00Z"}},"purchased":{"balance_usd":25}}`)
	qs, err := OllamaQuotas(context.Background(), nil, "k")
	if err != nil || len(qs) != 1 {
		t.Fatalf("quotas: %+v %v", qs, err)
	}
	q := qs[0]
	if q.Name != "month" || q.Used < 27.49 || q.Used > 27.51 || q.Minutes != 30*24*60 || q.ResetsAt == nil || q.ResetsAt.Day() != 15 {
		t.Errorf("month: %+v", q)
	}
}

func TestALegacyOllamaPlanGivesItsSessionAndWeeklyLimits(t *testing.T) {
	ollamaBalanceServer(t, 200, `{"included":{"session":{"remaining_percent":75,"resets_at":"2026-10-01T07:00:00Z"},`+
		`"weekly":{"remaining_percent":140,"resets_at":"2026-10-05T00:00:00Z"}},"purchased":{"balance_usd":0}}`)
	qs, err := OllamaQuotas(context.Background(), &http.Client{}, "k")
	if err != nil || len(qs) != 2 {
		t.Fatalf("quotas: %+v %v", qs, err)
	}
	if qs[0].Name != "session" || qs[0].Used != 25 || qs[1].Name != "week" || qs[1].Used != 0 {
		t.Errorf("limits: %+v", qs)
	}
}

func TestOllamaBalanceProblemsAreErrorsNotGuesses(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
	}{
		"refused":  {401, `{"error":"bad key"}`},
		"no quota": {200, `{"included":{},"purchased":{"balance_usd":3}}`},
		"not json": {200, `<html>`},
	} {
		ollamaBalanceServer(t, c.status, c.body)
		if qs, err := OllamaQuotas(context.Background(), nil, "k"); err == nil || qs != nil {
			t.Errorf("%s: %+v %v", name, qs, err)
		}
	}
	old := ollamaCloudURL
	ollamaCloudURL = "http://127.0.0.1:1"
	t.Cleanup(func() { ollamaCloudURL = old })
	if _, err := OllamaQuotas(context.Background(), nil, "k"); err == nil {
		t.Error("an unreachable service is an error")
	}
	ollamaCloudURL = "http://bad host/"
	if _, err := OllamaQuotas(context.Background(), nil, "k"); err == nil {
		t.Error("an unusable address is an error")
	}
}

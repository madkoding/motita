package llm

import (
	"context"
	"testing"
	"time"
)

func TestTheLedgerSumsEachProviderApartAndListsTheLatestFirst(t *testing.T) {
	clock := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	l := &ledger{by: map[string]*ProviderUsage{}, tick: func() time.Time { return clock }}
	l.add("openai", Usage{Input: 10, Output: 2})
	l.add("openai", Usage{Input: 5, CacheRead: 1})
	clock = clock.Add(time.Minute)
	l.add("anthropic", Usage{Output: 7})
	got := l.snapshot()
	if len(got) != 2 || got[0].Provider != "anthropic" || got[1].Provider != "openai" {
		t.Fatalf("order: %+v", got)
	}
	if want := (Usage{Input: 15, Output: 2, CacheRead: 1}); got[1].Usage != want || got[1].Calls != 2 {
		t.Errorf("openai: %+v", got[1])
	}
	if !got[0].Last.After(got[1].Last) {
		t.Errorf("last: %v %v", got[0].Last, got[1].Last)
	}
	// Two used at the same moment keep a stable order, by name.
	l.add("gemini", Usage{Input: 1})
	l.by["gemini"].Last = l.by["anthropic"].Last
	if again := l.snapshot(); again[0].Provider != "anthropic" || again[1].Provider != "gemini" {
		t.Errorf("tie: %+v", again)
	}
}

func TestProviderCallsFeedTheProcessWideTotals(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, `{"choices":[{"message":{"content":"ok"}}],`+openAIUsageJSON+`}`)
	c := clientFor(t, srv.URL, nil)
	before := totalsOf("openai")
	if _, err := c.Complete(context.Background(), hi); err != nil {
		t.Fatal(err)
	}
	after := totalsOf("openai")
	if after.Calls != before.Calls+1 || after.Usage.Input != before.Usage.Input+10 {
		t.Errorf("before %+v, after %+v", before, after)
	}
	if totalsOf("nobody").Calls != 0 {
		t.Error("a provider never called has no totals")
	}
}

// totalsOf is the process-wide totals of one provider, zero when it has none.
func totalsOf(provider string) ProviderUsage {
	for _, p := range ProviderTotals() {
		if p.Provider == provider {
			return p
		}
	}
	return ProviderUsage{}
}

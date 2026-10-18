package llm

import (
	"context"
	"github.com/madkoding/motita/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newLedger(clock *time.Time) *ledger {
	return &ledger{by: map[string]*ProviderUsage{}, tick: func() time.Time { return *clock }}
}

func src(provider string) usageSource { return usageSource{provider: provider} }

func TestTheLedgerSumsEachProviderApartAndListsTheLatestFirst(t *testing.T) {
	clock := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	l := newLedger(&clock)
	l.add(src("openai"), Usage{Input: 10, Output: 2})
	l.add(src("openai"), Usage{Input: 5, CacheRead: 1})
	clock = clock.Add(time.Minute)
	l.add(src("anthropic"), Usage{Output: 7})
	got := l.snapshot()
	if len(got) != 2 || got[0].Provider != "anthropic" || got[1].Provider != "openai" {
		t.Fatalf("order: %+v", got)
	}
	o := got[1]
	if want := (Usage{Input: 15, Output: 2, CacheRead: 1}); o.Usage != want || o.Calls != 2 {
		t.Errorf("openai: %+v", o)
	}
	if !got[0].Last.After(got[1].Last) || !o.Since.Equal(clock.Add(-time.Minute)) {
		t.Errorf("times: %+v %+v", got[0], o)
	}
	// The snapshot is a copy: changing it does not change the ledger.
	o.Calls = 99
	if l.snapshot()[1].Calls != 2 {
		t.Error("the snapshot shares its entries with the ledger")
	}
	// Two used at the same moment keep a stable order, by name.
	l.add(src("gemini"), Usage{Input: 1})
	l.by["gemini"].Last = l.by["anthropic"].Last
	if again := l.snapshot(); again[0].Provider != "anthropic" || again[1].Provider != "gemini" {
		t.Errorf("tie: %+v", again)
	}
}

func TestRateLimitHeadersOfOpenAIAndAnthropicAreRead(t *testing.T) {
	openai := http.Header{}
	openai.Set("x-ratelimit-limit-requests", "500")
	openai.Set("x-ratelimit-remaining-requests", "499")
	openai.Set("x-ratelimit-limit-tokens", "30000")
	openai.Set("x-ratelimit-remaining-tokens", "29000")
	r, ok := limitsFrom(openai)
	if !ok || *r.Requests != (Window{500, 499}) || *r.Tokens != (Window{30000, 29000}) {
		t.Errorf("openai: %+v %v", r, ok)
	}
	anthropic := http.Header{}
	anthropic.Set("anthropic-ratelimit-requests-limit", "50")
	anthropic.Set("anthropic-ratelimit-requests-remaining", "49")
	anthropic.Set("anthropic-ratelimit-tokens-limit", "40000")
	anthropic.Set("anthropic-ratelimit-tokens-remaining", "39000")
	r, ok = limitsFrom(anthropic)
	if !ok || *r.Requests != (Window{50, 49}) || *r.Tokens != (Window{40000, 39000}) {
		t.Errorf("anthropic: %+v %v", r, ok)
	}
	half := http.Header{}
	half.Set("x-ratelimit-limit-tokens", "100") // no remaining: not a window
	if _, ok := limitsFrom(half); ok {
		t.Error("a limit without its remaining is not a window")
	}
	half.Set("x-ratelimit-remaining-tokens", "abc")
	if _, ok := limitsFrom(half); ok {
		t.Error("an unreadable remaining is not a window")
	}
	if _, ok := limitsFrom(http.Header{}); ok {
		t.Error("no headers, no limits")
	}
}

func TestTheLatestRateLimitsStayWithTheProvider(t *testing.T) {
	clock := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	l := newLedger(&clock)
	l.limits("openai", RateLimits{Tokens: &Window{Limit: 10, Remaining: 4}})
	got := l.snapshot()
	if len(got) != 1 || got[0].Calls != 0 || got[0].Limits == nil || got[0].Limits.Tokens.Remaining != 4 ||
		!got[0].Limits.At.Equal(clock) {
		t.Errorf("limits: %+v", got)
	}
}

func TestTheTotalsSurviveARestart(t *testing.T) {
	old := saveDelay
	saveDelay = time.Millisecond
	t.Cleanup(func() { saveDelay = old })
	file := filepath.Join(t.TempDir(), "usage.json")
	clock := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	l := newLedger(&clock)
	l.load(file) // nothing saved yet
	l.add(src("openai"), Usage{Input: 3})
	l.add(src("openai"), Usage{Input: 4}) // one write for both: it is already scheduled
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(file); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", info, err)
	}
	if err := l.flush(); err != nil {
		t.Fatal(err)
	}
	again := newLedger(&clock)
	again.load(file)
	got := again.snapshot()
	if len(got) != 1 || got[0].Usage.Input != 7 || got[0].Calls != 2 {
		t.Errorf("after a restart: %+v", got)
	}
}

func TestTheLedgerFileIsBestEffort(t *testing.T) {
	clock := time.Now()
	dir := t.TempDir()
	// A damaged file is an empty ledger.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := newLedger(&clock)
	l.load(bad)
	if len(l.snapshot()) != 0 {
		t.Error("a damaged file was read")
	}
	// Without a file nothing is written and nothing fails.
	none := newLedger(&clock)
	none.add(src("p"), Usage{Input: 1})
	if err := none.flush(); err != nil || none.pending {
		t.Errorf("no file: %v %v", err, none.pending)
	}
	// A folder that is not there, and a target that is a folder, say why.
	missing := newLedger(&clock)
	missing.file = filepath.Join(dir, "nope", "usage.json")
	if err := missing.flush(); err == nil {
		t.Error("a missing folder was not reported")
	}
	target := filepath.Join(dir, "asdir")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	blocked := newLedger(&clock)
	blocked.file = target
	if err := blocked.flush(); err == nil {
		t.Error("a target that is a folder was not replaced silently")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
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
	if err := FlushUsage(); err != nil { // no file named: nothing to write
		t.Error(err)
	}
}

func TestAnAnswerWithRateLimitHeadersRecordsThem(t *testing.T) {
	srv := newHeaderServer(t, map[string]string{
		"x-ratelimit-limit-tokens": "1000", "x-ratelimit-remaining-tokens": "900",
	})
	c := clientFor(t, srv.URL, func(l *config.LLM) { l.Provider = "ollama" })
	if _, err := c.Complete(context.Background(), hi); err != nil {
		t.Fatal(err)
	}
	if l := totalsOf("ollama").Limits; l == nil || l.Tokens.Remaining != 900 {
		t.Errorf("limits: %+v", l)
	}
	PersistUsage(filepath.Join(t.TempDir(), "usage.json"))
	t.Cleanup(func() { totals.file = "" })
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

// newHeaderServer answers every request with a chat completion and the headers given.
func newHeaderServer(t *testing.T, headers map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],` + openAIUsageJSON + `}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

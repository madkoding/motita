package llm

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// claudeEventSeen is a rate_limit_event as claude 2.1.302 printed it for a Pro/Max account, ids
// removed: the session is below its warning level, so the top-level fields carry no share, and the
// shares come only from unifiedWindows.
const claudeEventSeen = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed",` +
	`"resetsAt":1792380600,"rateLimitType":"five_hour","overageStatus":"rejected",` +
	`"overageDisabledReason":"org_level_disabled","isUsingOverage":false,"unifiedWindows":{` +
	`"five_hour":{"utilization":0,"resetsAt":1792380600},` +
	`"seven_day":{"utilization":0.57,"resetsAt":1792656000}}}}` + "\n"

func TestARealClaudeEventGivesTheSessionAndTheWeek(t *testing.T) {
	p := &claudeProcess{out: bufio.NewScanner(strings.NewReader(claudeEventSeen))}
	if _, err := p.event(); err != nil {
		t.Fatal(err)
	}
	defer delete(totals.by, "claude-code")
	got := map[string]Quota{}
	for _, q := range totalsOf("claude-code").Limits.Quotas {
		got[q.Name] = q
	}
	session, week := got["session"], got["week"]
	if session.Used != 0 || session.Status != "" || session.ResetsAt.Unix() != 1792380600 {
		t.Errorf("session: %+v", session)
	}
	if week.Used < 56.9 || week.Used > 57.1 || week.Minutes != weekMinutes || week.ResetsAt.Unix() != 1792656000 {
		t.Errorf("week: %+v", week)
	}
}

// waitForClaudeQuotas is claude-code's quotas once the ledger has some, or nil after a few seconds.
func waitForClaudeQuotas(t *testing.T) []Quota {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if l := totalsOf("claude-code").Limits; l != nil && len(l.Quotas) > 0 {
			return l.Quotas
		}
	}
	return nil
}

func TestTheLimitsClaudePrintsAfterTheAnswerAreRecorded(t *testing.T) {
	// The order claude 2.1.303 prints: the answer's message_stop, then the limits, then the result.
	script := lines(evStart, textDelta("ok"), assistantText("ok"), stopReason("end_turn"), evStop,
		strings.TrimSuffix(claudeEventSeen, "\n"), "sleep")
	c, _ := claudeTestClient(t, script, nil)
	defer delete(totals.by, "claude-code")
	start := time.Now()
	reply, err := c.callClaudeCodeText(context.Background(), hi)
	if err != nil || reply != "ok" {
		t.Fatalf("reply %q, %v", reply, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("the answer waited for the limits (%v)", time.Since(start))
	}
	if q := waitForClaudeQuotas(t); len(q) != 2 {
		t.Errorf("quotas after the answer: %+v", q)
	}
}

func TestAClaudeThatPrintsNothingMoreIsStoppedAfterTheWait(t *testing.T) {
	old := claudeDrainWait
	claudeDrainWait = 50 * time.Millisecond
	t.Cleanup(func() { claudeDrainWait = old })
	c, record := claudeTestClient(t, textScript, nil)
	if _, err := c.callClaudeCodeText(context.Background(), hi); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(record + ".term"); err == nil {
			return
		}
	}
	if runtime.GOOS != "windows" {
		t.Error("claude was not stopped after the wait")
	}
}

func TestAProbeRecordsTheLimitsWithTheSmallestQuestion(t *testing.T) {
	_, record := claudeTestClient(t, lines(assistantText("ok"), strings.TrimSuffix(claudeEventSeen, "\n"), "sleep"), nil)
	defer delete(totals.by, "claude-code")
	if err := ProbeClaudeQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q := totalsOf("claude-code"); len(q.Limits.Quotas) != 2 || q.Calls != 0 {
		t.Errorf("probe: %+v", q)
	}
	rec := readRecord(t, record)
	args := strings.Join(rec.Args, " ")
	if !strings.Contains(args, "--model haiku") || !strings.Contains(args, "--tools  ") ||
		!strings.Contains(args, "--no-session-persistence") {
		t.Errorf("args: %q", args)
	}
	if len(rec.Frames) != 1 || !strings.Contains(string(rec.Frames[0]), `"text":"ok"`) {
		t.Errorf("question: %q", rec.Frames)
	}
}

func TestAProbeWithoutLimitsFails(t *testing.T) {
	claudeTestClient(t, `{"type":"result","subtype":"success"}`, nil)
	if err := ProbeClaudeQuota(context.Background()); err == nil {
		t.Error("a probe that saw no limits succeeded")
	}
	t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude"))
	if err := ProbeClaudeQuota(context.Background()); err == nil {
		t.Error("a probe without claude succeeded")
	}
}

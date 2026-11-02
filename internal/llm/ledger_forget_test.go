package llm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestForgetProviderUsageDropsItFromTheFileToo(t *testing.T) {
	clock := time.Now()
	l := newLedger(&clock)
	file := filepath.Join(t.TempDir(), "usage.json")
	l.load(file)
	l.add(usageSource{provider: "openai"}, Usage{Input: 3})
	l.add(usageSource{provider: "gemini"}, Usage{Input: 1})
	if err := l.flush(); err != nil {
		t.Fatal(err)
	}
	l.forget("openai")
	l.forget("nobody") // nothing to drop: nothing written
	if err := l.flush(); err != nil {
		t.Fatal(err)
	}
	again := newLedger(&clock)
	again.load(file)
	if got := again.snapshot(); len(got) != 1 || got[0].Provider != "gemini" {
		t.Errorf("after forget: %+v", got)
	}
	_ = os.Remove(file)
}

func TestTheProcessLedgerForgetsAProvider(t *testing.T) {
	totals.add(usageSource{provider: "zz-forget"}, Usage{Input: 1})
	ForgetProviderUsage("zz-forget")
	for _, p := range ProviderTotals() {
		if p.Provider == "zz-forget" {
			t.Error("still counted")
		}
	}
}

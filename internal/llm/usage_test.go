package llm

import (
	"context"
	"sync"
	"testing"
)

func TestUsageTotalAndAdd(t *testing.T) {
	a := Usage{Input: 10, Output: 5, CacheRead: 100, CacheWrite: 7}
	b := Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}
	if got := a.Total(); got != 122 {
		t.Fatalf("Total = %d, want 122", got)
	}
	if got := a.Add(b); got != (Usage{Input: 11, Output: 7, CacheRead: 103, CacheWrite: 11}) {
		t.Fatalf("Add = %+v", got)
	}
}

func TestMeterRecordsCallsMadeWithItsContext(t *testing.T) {
	m := &Meter{}
	ctx := WithMeter(context.Background(), m)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordUsage(ctx, Usage{Input: 2, Output: 1})
		}()
	}
	wg.Wait()
	if got := m.Usage(); got != (Usage{Input: 100, Output: 50}) {
		t.Fatalf("usage = %+v", got)
	}
	if m.Calls() != 50 {
		t.Fatalf("calls = %d, want 50", m.Calls())
	}
}

func TestRecordUsageWithoutAMeterOrUsageDoesNothing(t *testing.T) {
	recordUsage(context.Background(), Usage{Input: 1}) // no meter: nothing to record into
	m := &Meter{}
	ctx := WithMeter(context.Background(), m)
	recordUsage(ctx, Usage{}) // a provider that reports nothing is not a zero-token call
	if m.Calls() != 0 {
		t.Fatalf("calls = %d, want 0", m.Calls())
	}
	if WithMeter(ctx, nil) != ctx {
		t.Fatal("a nil meter must leave the context as it is")
	}
}

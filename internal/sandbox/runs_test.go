package sandbox

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/madkoding/motita/internal/execx"
)

// What the platform could not apply is known once, in New. Recording it again on every run grew
// the list by one copy of the same line per command, from goroutines that raced on it.
func TestRunsDoNotGrowWhatWasNotApplied(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	// The platform's warnings are recorded, once.
	_, warnings := childAttributes(Limits{}, false, 0, 0)
	for _, w := range warnings {
		if !slices.Contains(box.NotApplied(), w) {
			t.Errorf("NotApplied %q is missing the platform warning %q", box.NotApplied(), w)
		}
	}
	before := len(box.NotApplied())
	cmd, args := echoCommand("hi")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, _, _, _ = box.Run(context.Background(), execx.Request{Command: cmd, Args: args})
		})
	}
	wg.Wait()
	if after := len(box.NotApplied()); after != before {
		t.Errorf("NotApplied went from %d to %d entries: %q", before, after, box.NotApplied())
	}
}

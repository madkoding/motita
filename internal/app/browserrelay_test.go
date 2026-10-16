package app

import (
	"context"
	"errors"
	"testing"

	"github.com/madkoding/motita/internal/browserllm"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
)

// The browser provider asks the broker the gateway serves: with no tab open that is a plain,
// readable failure, not a wait.
func TestBrowserRelayServesTheProvider(t *testing.T) {
	t.Cleanup(func() { llm.SetBrowserRelay(nil) })
	if browserRelay() == nil {
		t.Fatal("no broker")
	}
	c, err := llm.New(config.LLM{Provider: llm.ProviderBrowser, Model: "m", MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Complete(context.Background(), nil); !errors.Is(err, browserllm.ErrNoTab) {
		t.Errorf("Complete = %v", err)
	}
}

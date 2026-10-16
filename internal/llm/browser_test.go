package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// fakeRelay answers like a tab would.
type fakeRelay struct {
	got  []Message
	text string
	err  error
}

func (f *fakeRelay) Ask(_ context.Context, m []Message) (string, error) {
	f.got = m
	return f.text, f.err
}

func browserClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(config.LLM{Provider: ProviderBrowser, Model: "qwen2.5-0.5b-instruct"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// The browser provider needs no key, and its text comes from the relay.
func TestBrowserProviderAsksTheRelay(t *testing.T) {
	r := &fakeRelay{text: "A short title"}
	SetBrowserRelay(r)
	t.Cleanup(func() { SetBrowserRelay(nil) })
	c := browserClient(t)
	got, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "name this"}})
	if err != nil || got != "A short title" || len(r.got) != 1 {
		t.Fatalf("Complete = %q, %v (relay saw %v)", got, err, r.got)
	}
	// A turn without tools goes through the streaming path the agent uses, to the same relay.
	var reply Reply
	for ch := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "x"}}, nil) {
		if ch.Event == StreamDone {
			reply = ch.Reply
		}
	}
	if reply.Content != "A short title" {
		t.Errorf("stream reply = %q", reply.Content)
	}
}

// A failure of the tab is the failure of the call; with no relay installed it is not retried.
func TestBrowserProviderFailures(t *testing.T) {
	c := browserClient(t)
	SetBrowserRelay(nil)
	if _, err := c.Complete(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "no browser model") {
		t.Errorf("without a relay: %v", err)
	}
	SetBrowserRelay(&fakeRelay{err: errors.New("no tab")})
	t.Cleanup(func() { SetBrowserRelay(nil) })
	c.cfg.MaxAttempts = 1
	if _, err := c.Complete(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "no tab") {
		t.Errorf("with a failing relay: %v", err)
	}
}

// A turn that needs tools is refused, and not retried.
func TestBrowserProviderHasNoTools(t *testing.T) {
	c := browserClient(t)
	_, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "x"}}, []Tool{{Type: "function"}})
	if err == nil || !strings.Contains(err.Error(), "cannot call tools") || retryable(err) {
		t.Errorf("CompleteTools = %v", err)
	}
}

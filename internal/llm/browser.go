package llm

// --- The browser model ----------------------------------------------------------
//
// The "browser" provider asks a small model that runs inside an open tab of the web UI (WebGPU,
// see web/src/browserLLM.ts) instead of an HTTP API. The tab is not part of this package: the
// gateway installs a BrowserRelay that hands each request to it. Only text goes through: the
// model is for the cheap jobs (titles, summaries) and does not call tools.

import (
	"context"
	"errors"
	"sync"
)

// ProviderBrowser is the provider that runs in the person's browser.
const ProviderBrowser = "browser"

// BrowserRelay hands a conversation to the model in a browser tab and returns its text. It fails
// when no tab is there to answer.
type BrowserRelay interface {
	Ask(ctx context.Context, messages []Message) (string, error)
}

var (
	relayMu      sync.RWMutex
	browserRelay BrowserRelay
)

// SetBrowserRelay installs the relay the browser provider asks, or removes it with nil.
func SetBrowserRelay(r BrowserRelay) {
	relayMu.Lock()
	defer relayMu.Unlock()
	browserRelay = r
}

// errBrowserTools is what a turn that needs tools gets: the browser model only writes text.
var errBrowserTools = fatalError{errors.New("the browser model cannot call tools: use it for titles and summaries")}

// errNoBrowserRelay is the failure on a gateway that serves no browser model.
var errNoBrowserRelay = fatalError{errors.New("this gateway has no browser model")}

// callBrowserText asks the tab for the answer.
func (c *Client) callBrowserText(ctx context.Context, messages []Message) (string, error) {
	relayMu.RLock()
	r := browserRelay
	relayMu.RUnlock()
	if r == nil {
		return "", errNoBrowserRelay
	}
	return r.Ask(ctx, messages)
}

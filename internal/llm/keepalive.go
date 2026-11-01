package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// KEEPING A LOCAL MODEL LOADED. A self-hosted Ollama unloads a model five minutes after the last
// request, and the OpenAI-compatible endpoint this client speaks has no keep_alive to say
// otherwise: a person who reads the answer for six minutes waits tens of seconds for the model to
// load again on their next message. After each call the client tells the native API how long to
// keep the model (llm.keep_alive), in the background. The request has no prompt, so it loads
// nothing new and generates nothing.

// warmTimeout bounds the keep-alive request: the model is already loaded when it is sent.
const warmTimeout = 30 * time.Second

// goWarm runs the keep-alive request; a variable so a test can wait for it.
var goWarm = func(f func()) { go f() }

// keepWarm tells a self-hosted Ollama to keep the model loaded for llm.keep_alive. Any other
// provider, or a keep_alive of 0, is left alone.
func (c *Client) keepWarm() {
	if c.cfg.KeepAlive <= 0 || !config.IsSelfHostedOllama(c.cfg) {
		return
	}
	goWarm(func() {
		ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
		defer cancel()
		body, _ := json.Marshal(map[string]any{"model": c.cfg.Model, "keep_alive": int(c.cfg.KeepAlive.Seconds())})
		// IsSelfHostedOllama has already parsed the address.
		u, _ := url.Parse(strings.TrimSpace(c.cfg.BaseURL))
		u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/v1") + "/api/generate"
		req := (&http.Request{Method: http.MethodPost, URL: u, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}).WithContext(ctx)
		if resp, err := c.http.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	})
}

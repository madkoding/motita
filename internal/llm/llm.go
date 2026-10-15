// Package llm implements Layer B: the reasoning engine.
//
// It is a lightweight, SDK-free client that talks to four families of API:
//
//   - chat completions: POST /chat/completions (Bearer) — openai, ollama, qwen, copilot
//   - responses:        POST /responses (Bearer) — codex, with a key or a ChatGPT login
//   - anthropic:        POST /v1/messages (x-api-key + anthropic-version)
//   - gemini:           POST /v1beta/models/<model>:generateContent (x-goog-api-key, or
//     a Google login's Bearer token)
//
// How each request is authenticated (a key, or a stored login that is renewed
// before it expires) is decided in auth.go.
//
// and claude-code, which drives the local claude CLI on the user's own Claude
// subscription instead of an HTTP API (see claudecode.go).
//
// Every provider is normalised to the same message structure and back to plain
// text, so the agent loop never needs to know which one is behind it. Parsing of
// structured responses and retrying with exponential backoff live here.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// Literals used more than once in this file.
const (
	providerClaudeCode = "claude-code"
)

// Message is one turn of the conversation.
type Message struct {
	Role       string     // system | user | assistant | tool
	Content    string     // text of the turn (empty when the turn is only calls)
	ToolCalls  []ToolCall // for assistant turns that ask for tool results
	ToolCallID string     // for tool turns, matching the assistant call
}

// Client talks to the configured provider.
type Client struct {
	cfg   config.LLM
	http  *http.Client
	log   *logx.Logger
	sleep func(time.Duration) // injectable so tests do not have to wait
	// openStream opens one streaming attempt. It is injectable so the retry
	// wrapper can be tested against a producer that fails, or closes without a
	// done chunk, without having to make a real server misbehave.
	openStream func(context.Context, []Message, []Tool) (<-chan StreamChunk, error)
	// login is the stored login the client authenticates with, nil for a key.
	login *loginState
	// outputCap is the output-token limit learned from a provider's refusal, 0 while unknown.
	outputCap atomic.Int64
}

// New creates the reasoning engine client.
func New(cfg config.LLM, log *logx.Logger) (*Client, error) {
	if log == nil {
		log = logx.Global()
	}
	known := false
	for _, p := range config.Providers {
		known = known || strings.EqualFold(cfg.Provider, p)
	}
	if !known {
		return nil, fmt.Errorf("unsupported LLM provider: %q", cfg.Provider)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 90 * time.Second
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.BackoffInitial <= 0 {
		cfg.BackoffInitial = time.Second
	}
	if cfg.BackoffMax < cfg.BackoffInitial {
		cfg.BackoffMax = cfg.BackoffInitial
	}

	hc := &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: TLSConfig(),
		},
	}
	login, err := newLoginState(cfg, hc)
	if err != nil {
		return nil, err
	}
	if cfg.APIKey == "" && login == nil && config.LLMNeedsKey(cfg) {
		if config.SupportsLogin(cfg.Provider) {
			return nil, fmt.Errorf("the LLM key is missing: set %s, or log in with `motita config`",
				config.ProviderKeyVariable(cfg.Provider))
		}
		return nil, errors.New("the LLM key is missing")
	}

	return &Client{
		cfg:   cfg,
		http:  hc,
		log:   log,
		login: login,
		sleep: func(d time.Duration) {
			time.Sleep(d)
		},
	}, nil
}

// WithTransport sends every request of this client, the login's included, through rt instead of
// the network. It is the seam that lets a caller's test see where a request would have gone
// (the provider's default endpoint, say) without making it.
func (c *Client) WithTransport(rt http.RoundTripper) *Client {
	c.http.Transport = rt
	return c
}

// openStreamOr returns the injected opener, or the real HTTP one.
func (c *Client) openStreamOr() func(context.Context, []Message, []Tool) (<-chan StreamChunk, error) {
	if c.openStream != nil {
		return c.openStream
	}
	switch strings.ToLower(c.cfg.Provider) {
	case providerClaudeCode:
		return c.callClaudeCodeStream
	case "codex":
		return c.callResponsesStream
	case "anthropic", "gemini":
		// Neither speaks /chat/completions. Both used to be sent there anyway - a plan-mode
		// turn with either provider was a request to the wrong API - and they are answered
		// by their own non-streaming call instead, delivered through the same channel.
		return c.callAsStream
	default:
		// openai, ollama, qwen and copilot all speak /chat/completions.
		return c.callOpenAIToolsStream
	}
}

// callAsStream makes one non-streaming request and delivers it as a stream: the text, one
// chunk per tool call, then the reply. A failure is returned before the channel exists, so
// the caller's retry policy applies to it like to any stream that could not open.
func (c *Client) callAsStream(ctx context.Context, messages []Message, tools []Tool) (<-chan StreamChunk, error) {
	var reply Reply
	var err error
	if len(tools) == 0 {
		// The text call returns only its text; a Meter of its own catches what it consumed, so
		// the streamed reply carries the usage like every other stream's does.
		own := &Meter{}
		reply.Content, err = c.call(WithMeter(ctx, own), messages)
		reply.Usage = own.Usage()
	} else {
		reply, err = c.callTools(ctx, messages, tools)
	}
	if err != nil {
		return nil, err
	}
	out := make(chan StreamChunk, len(reply.Calls)+2)
	if reply.Content != "" {
		out <- StreamChunk{Event: StreamText, Text: reply.Content}
	}
	for i := range reply.Calls {
		out <- StreamChunk{Event: StreamToolCall, Call: &reply.Calls[i]}
	}
	out <- StreamChunk{Event: StreamDone, Reply: reply}
	close(out)
	return out, nil
}

// CompleteStream is Complete with the text delivered while it is generated.
//
// onDelta receives every fragment as it arrives: thinking is true for the model's own
// reasoning tokens, which are shown and never returned. What it returns is the answer, the
// same text Complete would have.
//
// A stream that fails is retried as a plain Complete: an endpoint that mishandles
// `stream: true` must still answer, only without the live view.
func (c *Client) CompleteStream(
	ctx context.Context, messages []Message, onDelta func(text string, thinking bool),
) (string, error) {
	var text strings.Builder
	var streamErr error
	for chunk := range c.CompleteToolsStream(ctx, messages, nil) {
		switch chunk.Event {
		case StreamText:
			text.WriteString(chunk.Text)
			onDelta(chunk.Text, false)
		case StreamThinking:
			onDelta(chunk.Text, true)
		case StreamError:
			streamErr = chunk.Error
		case StreamDone:
			if text.Len() == 0 {
				text.WriteString(chunk.Reply.Content)
			}
		default:
			// Tool calls are not part of a text completion.
		}
	}
	if streamErr == nil && strings.TrimSpace(text.String()) != "" {
		return text.String(), nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	// The reason is named. A stream that ends with no error and no text used to be logged as
	// `error=<nil>`, which reads as a bug in the logger and hides what happened: the endpoint
	// closed the stream without an answer.
	reason := "the stream ended without any text"
	if streamErr != nil {
		reason = streamErr.Error()
	}
	c.log.Warn("the streamed completion failed; asking again without streaming", "error", reason)
	return c.Complete(ctx, messages)
}

// Complete sends the conversation and returns the model's text, retrying with
// exponential backoff on transient failures.
func (c *Client) Complete(ctx context.Context, messages []Message) (string, error) {
	var last error
	wait := c.cfg.BackoffInitial

	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		text, err := c.call(ctx, messages)
		if err == nil {
			return text, nil
		}
		last = err

		if !retryable(err) {
			// A credentials or request error does not improve by retrying:
			// fail fast instead of burning time and quota.
			c.log.Error("LLM call failed with no possibility of retry",
				"attempt", attempt, "error", err, "detail", Detail(err))
			return "", err
		}
		if attempt == c.cfg.MaxAttempts {
			break
		}

		c.log.Warn("retrying LLM call",
			"attempt", attempt, "max_attempts", c.cfg.MaxAttempts,
			"wait", wait.String(), "error", err, "detail", Detail(err))

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("cancelled while waiting to retry: %w", ctx.Err())
		case <-time.After(wait):
		}
		wait = c.nextWait(wait)
	}

	return "", fmt.Errorf("all %d attempts were exhausted: %w", c.cfg.MaxAttempts, last)
}

// nextWait is the wait before the attempt after one that waited wait: twice as long, up to the
// configured maximum.
func (c *Client) nextWait(wait time.Duration) time.Duration {
	return min(wait*2, c.cfg.BackoffMax)
}

// CompleteTools sends the conversation with the available tools and returns whatever
// the model answered: text, tool calls, or both. It uses the same retry policy as
// Complete so callers do not have to think about transient failures.
func (c *Client) CompleteTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	return c.completeWithTool(ctx, messages, tools)
}

// CompleteToolsStream is the streaming version of CompleteTools. It returns a
// channel that yields chunks as they arrive from the provider. The channel is always
// closed; the caller must read until StreamDone or StreamError.
func (c *Client) CompleteToolsStream(ctx context.Context, messages []Message, tools []Tool) <-chan StreamChunk {
	out := make(chan StreamChunk, 8)
	go func() {
		defer close(out)
		var last error
		wait := c.cfg.BackoffInitial
		for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
			chunkCh, err := c.openStreamOr()(ctx, messages, tools)
			if err == nil {
				if err = forwardStream(chunkCh, out); err == nil {
					return
				}
			}
			last = err
			if !retryable(err) {
				c.log.Error("LLM tool stream failed with no possibility of retry", "attempt", attempt, "error", err, "detail", Detail(err))
				out <- StreamChunk{Event: StreamError, Error: err}
				return
			}
			if attempt == c.cfg.MaxAttempts {
				break
			}
			c.log.Warn("retrying LLM tool stream", "attempt", attempt, "max_attempts", c.cfg.MaxAttempts,
				"wait", wait.String(), "error", err)
			select {
			case <-ctx.Done():
				out <- StreamChunk{Event: StreamError, Error: fmt.Errorf("cancelled while waiting to retry: %w", ctx.Err())}
				return
			case <-time.After(wait):
			}
			wait = c.nextWait(wait)
		}
		out <- StreamChunk{Event: StreamError,
			Error: fmt.Errorf("all %d attempts were exhausted: %w", c.cfg.MaxAttempts, last)}
	}()
	return out
}

// forwardStream forwards one opened stream to out. A failure before anything reached the caller
// is a failed attempt like one that could not open, and is returned so it goes through the same
// retry policy; once something was forwarded, the stream is the caller's to its end.
func forwardStream(chunkCh <-chan StreamChunk, out chan<- StreamChunk) error {
	forwarded := false
	for chunk := range chunkCh {
		if !forwarded && chunk.Event == StreamError {
			return chunk.Error
		}
		forwarded = true
		out <- chunk
		if chunk.Event == StreamError || chunk.Event == StreamDone {
			return nil
		}
	}
	return nil
}

func (c *Client) completeWithTool(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	var last error
	wait := c.cfg.BackoffInitial
	var empty Reply

	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		reply, err := c.callTools(ctx, messages, tools)
		if err == nil {
			return reply, nil
		}
		last = err

		if !retryable(err) {
			c.log.Error("LLM tool call failed with no possibility of retry",
				"attempt", attempt, "error", err, "detail", Detail(err))
			return empty, err
		}
		if attempt == c.cfg.MaxAttempts {
			break
		}

		c.log.Warn("retrying LLM tool call",
			"attempt", attempt, "max_attempts", c.cfg.MaxAttempts,
			"wait", wait.String(), "error", err, "detail", Detail(err))

		select {
		case <-ctx.Done():
			return empty, fmt.Errorf("cancelled while waiting to retry: %w", ctx.Err())
		case <-time.After(wait):
		}
		wait = c.nextWait(wait)
	}

	return empty, fmt.Errorf("all %d attempts were exhausted: %w", c.cfg.MaxAttempts, last)
}

// HTTPError describes a failure with a status code so that retryability can be
// decided.
type HTTPError struct {
	Code int
	Body string
}

// retryable says whether it is worth trying again.
func retryable(err error) bool {
	if errors.As(err, new(fatalError)) {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		switch {
		case he.Code == 429, he.Code >= 500:
			return true
		default:
			return false
		}
	}
	// Network errors (timeouts, DNS, dropped connection) are retried.
	return true
}

// call makes a single request to the provider.
func (c *Client) call(ctx context.Context, messages []Message) (string, error) {
	switch strings.ToLower(c.cfg.Provider) {
	case "anthropic":
		return c.callAnthropic(ctx, messages)
	case "gemini":
		return c.callGemini(ctx, messages)
	case providerClaudeCode:
		return c.callClaudeCodeText(ctx, messages)
	case "codex":
		return c.callCodexText(ctx, messages)
	default:
		// openai, ollama, qwen and copilot all speak /chat/completions.
		return c.callOpenAI(ctx, messages)
	}
}

// callCodexText is a text-only call to /responses, which must answer with text.
func (c *Client) callCodexText(ctx context.Context, messages []Message) (string, error) {
	reply, err := c.callResponses(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(reply.Content) == "" {
		return "", fmt.Errorf("codex returned no text (%d tool call(s))", len(reply.Calls))
	}
	return reply.Content, nil
}

// callTools makes a single tool-enabled request to the provider.
func (c *Client) callTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	switch strings.ToLower(c.cfg.Provider) {
	case "anthropic":
		return c.callAnthropicTools(ctx, messages, tools)
	case "gemini":
		return c.callGeminiTools(ctx, messages, tools)
	case providerClaudeCode:
		return c.callClaudeCode(ctx, messages, tools, nil)
	case "codex":
		return c.callResponses(ctx, messages, tools)
	default:
		// openai, ollama, qwen and copilot all speak /chat/completions.
		return c.callOpenAITools(ctx, messages, tools)
	}
}

func (c *Client) baseURL(defecto string) string {
	if c.cfg.BaseURL == "" {
		return defecto
	}
	return strings.TrimRight(c.cfg.BaseURL, "/")
}

// ListModels asks the provider for the catalogue it publishes and returns the
// model names in the order the host reports them.
//
// Two endpoint families are tried because the base URL may be either spelling:
//
//   - OpenAI-compatible: <base>/models  (works for OpenAI, Ollama Cloud with
//     base https://ollama.com/v1, Groq, OpenRouter, and similar hosts)
//   - Ollama native: <base without /v1>/api/tags
//
// The first one that answers with a usable list wins. Exported so the first-run
// wizard can present the live catalogue instead of a hard-coded one.
func ListModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("no API base URL to list models from")
	}

	// Build the candidate URLs in order. A base URL that ends in /v1 must not
	// produce /v1/api/tags: that is a 404 (measured against Ollama Cloud).
	candidates := []string{base + "/models"}
	if root, ok := strings.CutSuffix(base, "/v1"); ok {
		candidates = append(candidates, root+"/api/tags")
	} else {
		candidates = append(candidates, base+"/api/tags")
	}

	var firstErr error
	for _, url := range candidates {
		headers := map[string]string{}
		if apiKey != "" {
			headers["Authorization"] = "Bearer " + apiKey
		}
		names, err := fetchModelList(ctx, nil, url, headers)
		if err == nil && len(names) > 0 {
			return names, nil
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("no models were listed by %s", base)
}

// fetchModelList performs one request and decodes both response shapes the two
// endpoint families use: {"data":[{"id":...}]} and {"models":[{"name":...}]}.
//
// rt is the transport of the client asking (so a client's WithTransport covers its model list
// too); nil means a fresh one.
func fetchModelList(
	ctx context.Context, rt http.RoundTripper, url string, headers map[string]string,
) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if rt == nil {
		rt = &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: TLSConfig(),
		}
	}
	client := http.Client{Timeout: 15 * time.Second, Transport: rt}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}

	var payload struct {
		// OpenAI-compatible spelling.
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		// Ollama native spelling.
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("could not decode the model list from %s: %w", url, err)
	}

	names := make([]string, 0, len(payload.Data)+len(payload.Models))
	seen := make(map[string]struct{})
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, m := range payload.Data {
		add(m.ID)
	}
	for _, m := range payload.Models {
		add(m.Name)
	}
	return names, nil
}

// ListOllamaModels is kept as a named entry point for the Ollama provider; it is
// the same catalogue query.
func ListOllamaModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	return ListModels(ctx, baseURL, apiKey)
}

// --- transport --------------------------------------------------------------
//
// send and post live in auth.go, beside the credentials they attach.

// truncate limits a text for error messages.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- Structured responses ---------------------------------------------------

// DecodeJSON extracts and deserialises into dest, with explainable errors.
func DecodeJSON(text string, dest any) error {
	raw, err := ExtractJSON(text)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("the JSON in the response does not fit what was expected (%v): %s", err, truncate(string(raw), 300))
	}
	return nil
}

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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
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
			return nil, fmt.Errorf("the LLM key is missing: set %s, or log in with `motita -init`", config.ProviderKeyVariable(cfg.Provider))
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

// openStreamOr returns the injected opener, or the real HTTP one.
func (c *Client) openStreamOr() func(context.Context, []Message, []Tool) (<-chan StreamChunk, error) {
	if c.openStream != nil {
		return c.openStream
	}
	switch strings.ToLower(c.cfg.Provider) {
	case "claude-code":
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
func (c *Client) CompleteStream(ctx context.Context, messages []Message, onDelta func(text string, thinking bool)) (string, error) {
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
				"attempt", attempt, "error", err)
			return "", err
		}
		if attempt == c.cfg.MaxAttempts {
			break
		}

		c.log.Warn("retrying LLM call",
			"attempt", attempt, "max_attempts", c.cfg.MaxAttempts,
			"wait", wait.String(), "error", err)

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("cancelled while waiting to retry: %w", ctx.Err())
		case <-time.After(wait):
		}
		wait *= 2
		if wait > c.cfg.BackoffMax {
			wait = c.cfg.BackoffMax
		}
	}

	return "", fmt.Errorf("all %d attempts were exhausted: %w", c.cfg.MaxAttempts, last)
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
				forwarded := false
				for chunk := range chunkCh {
					// A failure before anything reached the caller is a failed attempt like
					// one that could not open: it goes through the same retry policy.
					if !forwarded && chunk.Event == StreamError {
						err = chunk.Error
						break
					}
					forwarded = true
					out <- chunk
					if chunk.Event == StreamError || chunk.Event == StreamDone {
						return
					}
				}
				if err == nil {
					return
				}
			}
			last = err
			if !retryable(err) {
				c.log.Error("LLM tool stream failed with no possibility of retry", "attempt", attempt, "error", err)
				out <- StreamChunk{Event: StreamError, Error: err}
				return
			}
			if attempt == c.cfg.MaxAttempts {
				break
			}
			c.log.Warn("retrying LLM tool stream", "attempt", attempt, "max_attempts", c.cfg.MaxAttempts, "wait", wait.String(), "error", err)
			select {
			case <-ctx.Done():
				out <- StreamChunk{Event: StreamError, Error: fmt.Errorf("cancelled while waiting to retry: %w", ctx.Err())}
				return
			case <-time.After(wait):
			}
			wait *= 2
			if wait > c.cfg.BackoffMax {
				wait = c.cfg.BackoffMax
			}
		}
		out <- StreamChunk{Event: StreamError, Error: fmt.Errorf("all %d attempts were exhausted: %w", c.cfg.MaxAttempts, last)}
	}()
	return out
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
				"attempt", attempt, "error", err)
			return empty, err
		}
		if attempt == c.cfg.MaxAttempts {
			break
		}

		c.log.Warn("retrying LLM tool call",
			"attempt", attempt, "max_attempts", c.cfg.MaxAttempts,
			"wait", wait.String(), "error", err)

		select {
		case <-ctx.Done():
			return empty, fmt.Errorf("cancelled while waiting to retry: %w", ctx.Err())
		case <-time.After(wait):
		}
		wait *= 2
		if wait > c.cfg.BackoffMax {
			wait = c.cfg.BackoffMax
		}
	}

	return empty, fmt.Errorf("all %d attempts were exhausted: %w", c.cfg.MaxAttempts, last)
}

// HTTPError describes a failure with a status code so that retryability can be
// decided.
type HTTPError struct {
	Code int
	Body string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Code, truncate(e.Body, 400))
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
	case "claude-code":
		return c.callClaudeCodeText(ctx, messages)
	case "codex":
		reply, err := c.callResponses(ctx, messages, nil)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(reply.Content) == "" {
			return "", fmt.Errorf("codex returned no text (%d tool call(s))", len(reply.Calls))
		}
		return reply.Content, nil
	default:
		// openai, ollama, qwen and copilot all speak /chat/completions.
		return c.callOpenAI(ctx, messages)
	}
}

// callTools makes a single tool-enabled request to the provider.
func (c *Client) callTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	switch strings.ToLower(c.cfg.Provider) {
	case "anthropic":
		return c.callAnthropicTools(ctx, messages, tools)
	case "gemini":
		return c.callGeminiTools(ctx, messages, tools)
	case "claude-code":
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
		names, err := fetchModelList(ctx, url, headers)
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
func fetchModelList(ctx context.Context, url string, headers map[string]string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: TLSConfig(),
		},
	}
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

// --- OpenAI ----------------------------------------------------------------

type openAIMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type openAIChoice struct {
	Message struct {
		Content   string     `json:"content"`
		ToolCalls []ToolCall `json:"tool_calls"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// emptyReason explains WHY a reply carried nothing usable.
//
// It exists because the bare `finish_reason` was not enough to act on. The four causes need
// different responses from the caller, and all four used to arrive as the same sentence:
//
//   - `length`  — the answer was cut off by the token budget. Retrying identically wastes a
//     call; the budget or the prompt has to change.
//   - `tool_calls` — the provider says it sent calls and none arrived. That is a PARSE defect
//     on this side, not a model that refused to answer, and retrying it is pointless.
//   - `content_filter` — the provider blocked the answer. Nothing to retry.
//   - anything else, including "stop" — the model genuinely produced nothing.
//
// The `tool_calls` case is not hypothetical: it is the message that sent a real run into three
// pointless retries while the calls it had asked for were sitting in the stream.
func emptyReason(finishReason, content string, toolCalls int) string {
	trimmed := strings.TrimSpace(content)
	switch finishReason {
	case "tool_calls":
		return fmt.Sprintf("finish_reason=%q with %d tool call(s) and %d byte(s) of text: the provider "+
			"reported calls that did not arrive, which is a parsing failure on this side rather than "+
			"an empty answer — retrying cannot help", finishReason, toolCalls, len(trimmed))
	case "length":
		return fmt.Sprintf("finish_reason=%q: the answer was cut off by the token budget (%d byte(s) "+
			"received); retrying unchanged will truncate again", finishReason, len(trimmed))
	case "content_filter":
		return fmt.Sprintf("finish_reason=%q: the provider blocked the answer", finishReason)
	default:
		return fmt.Sprintf("finish_reason=%q: the model produced no text and asked for no tools", finishReason)
	}
}

// ErrToolCallForText marks a reply that answered a text-only phase with a tool call. Asking
// again UNCHANGED returns the same thing, but asking again with the reason stated does not:
// the caller can tell the model, which a bare failure gave it no chance to do.
var ErrToolCallForText = errors.New("the model called a tool where text was required")

func (c *Client) callOpenAI(ctx context.Context, messages []Message) (string, error) {
	choice, _, err := c.openAIChoice(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	// A reply that carried TOOL CALLS is not an empty reply, whatever the text says.
	//
	// This guard used to look at the text alone, so a response of `tool_calls` with no
	// prose was reported as "no usable content ... the provider reported calls that did not
	// arrive" and thrown away — while the calls were in the very struct being examined. The
	// message even counted them, which is how it read as a contradiction.
	//
	// Measured on a real run: every phase of the agent asks for its JSON as TEXT, so a
	// provider that answers one of those calls with a tool call lost the whole turn, and
	// the user was told the LLM had produced nothing. The calls are now reported as what
	// they are, so the caller decides: the agent's text phases turn them into a named error
	// that says which call arrived (see the caller's own guard), instead of a retry of a
	// reply that will come back the same way.
	if strings.TrimSpace(choice.Message.Content) == "" && len(choice.Message.ToolCalls) == 0 {
		// Nothing at all: the model hit a length limit, was blocked by a content filter, or
		// genuinely produced nothing. Retryable on THIS path, because a text-only call that
		// came back empty may succeed on a second attempt.
		return "", fmt.Errorf("OpenAI returned no usable content (%s)", emptyReason(
			choice.FinishReason, choice.Message.Content, len(choice.Message.ToolCalls)))
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		// A tool call with no text. The text path cannot honour it, and saying so NAMES what
		// arrived — the alternative was an opaque "empty answer" and three retries.
		//
		// It is a fatalError for the same reason the tools path uses one: the provider has
		// ROUTED this answer through a tool, and asking again the same way returns the same
		// thing. Measured: three identical failures and a message that blamed a parse defect
		// — the most confusing outcome available, because the reader goes looking for a bug
		// in the parser instead of at the prompt or the provider's routing.
		return "", fatalError{fmt.Errorf("%w: OpenAI answered with %d tool call(s) instead of text (%s): "+
			"this phase asks for its answer as text, so the call cannot be honoured here, and asking "+
			"again unchanged returns the same thing", ErrToolCallForText, len(choice.Message.ToolCalls), describeCalls(choice.Message.ToolCalls))}
	}
	return choice.Message.Content, nil
}

// describeCalls renders the names of a reply's tool calls for an error message.
//
// The NAME is what makes the message actionable: it tells a reader which tool the provider
// wanted, which is the difference between "the model refused to answer" and "the provider
// routed this through a tool the text path does not have".
func describeCalls(calls []ToolCall) string {
	if len(calls) == 0 {
		return "none"
	}
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		if c.Function.Name != "" {
			names = append(names, c.Function.Name)
			continue
		}
		names = append(names, "(unnamed)")
	}
	return strings.Join(names, ", ")
}

func (c *Client) callOpenAITools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	choice, usage, err := c.openAIChoice(ctx, messages, tools)
	if err != nil {
		return Reply{}, err
	}
	if strings.TrimSpace(choice.Message.Content) == "" && len(choice.Message.ToolCalls) == 0 {
		// No text and no tool calls: the model produced nothing usable.
		//
		// The message NAMES the reason instead of only quoting finish_reason, because the
		// bare form cost a real diagnosis. A run failed three times with
		// `finish_reason="tool_calls"` and an empty call list, which reads as a contradiction:
		// the provider was saying it HAD sent calls. Deciding what to do about it — retry,
		// report a truncated answer, or fix the parser — depends on which of the four causes it
		// was, and the old message could not tell them apart.
		return Reply{}, fatalError{fmt.Errorf("OpenAI returned no usable content (%s)", emptyReason(
			choice.FinishReason, choice.Message.Content, len(choice.Message.ToolCalls)))}
	}
	return Reply{
		Content:      choice.Message.Content,
		Calls:        choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
		Usage:        usage.usage(),
	}, nil
}

// openAIStreamDelta is the incremental piece inside a streaming chunk.
type openAIStreamDelta struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls"`
	// ReasoningContent and Reasoning are the model's thinking, under the two names the
	// OpenAI-compatible servers use for it (DeepSeek and vLLM; Ollama and OpenRouter).
	ReasoningContent string `json:"reasoning_content"`
	Reasoning        string `json:"reasoning"`
}

// openAIStreamChunk is one SSE line from /chat/completions?stream=true.
type openAIStreamChunk struct {
	Choices []struct {
		Delta        openAIStreamDelta `json:"delta"`
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
	// Usage arrives on a chunk of its own, after the one with finish_reason and with no
	// choices, when the request asked for it (stream_options.include_usage). Some servers
	// put it on the last chunk with choices instead; both are read.
	Usage *openAIUsage `json:"usage"`
}

func (c *Client) callOpenAIToolsStream(ctx context.Context, messages []Message, tools []Tool) (<-chan StreamChunk, error) {
	body := c.openAIBody(messages, tools)
	body["stream"] = true
	// A stream reports no usage unless asked, and only OpenAI's own API is asked. The other
	// /chat/completions servers (Ollama, Qwen, Copilot, a local server behind the openai
	// provider) are not known to accept the field - one that refuses an unknown field would
	// refuse the whole request for a counter - and waiting for a usage chunk that never comes
	// would bring back the hang the finish_reason return below exists to prevent. Those that
	// put usage on their last chunk anyway are still read.
	wantUsage := isOfficialOpenAI(c.baseURL(DefaultBaseURL(c.cfg.Provider)))
	if wantUsage {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	resp, _, err := c.send(ctx, "/chat/completions", body, true)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamChunk, 8)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		var acc StreamResult
		var usage Usage
		finished := false
		done := func() {
			reply := acc.FinalReply()
			reply.Usage = usage
			recordUsage(ctx, usage)
			out <- StreamChunk{Event: StreamDone, Reply: reply}
		}
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					done()
				} else {
					out <- StreamChunk{Event: StreamError, Error: err}
				}
				return
			}
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			if bytes.HasPrefix(line, []byte(":")) {
				continue
			}
			const dataPrefix = "data: "
			if !bytes.HasPrefix(line, []byte(dataPrefix)) {
				continue
			}
			payload := bytes.TrimPrefix(line, []byte(dataPrefix))
			if string(payload) == "[DONE]" {
				done()
				return
			}
			var chunk openAIStreamChunk
			if err := json.Unmarshal(payload, &chunk); err != nil {
				continue
			}
			if u := chunk.Usage.usage(); u.Total() > 0 {
				usage = u
				if finished {
					// The usage chunk is the last thing a stream that asked for it sends.
					done()
					return
				}
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			delta := chunk.Choices[0].Delta
			if thought := delta.ReasoningContent + delta.Reasoning; thought != "" {
				out <- StreamChunk{Event: StreamThinking, Text: thought}
			}
			if delta.Content != "" {
				out <- StreamChunk{Event: StreamText, Text: delta.Content}
			}
			for i := range delta.ToolCalls {
				out <- StreamChunk{Event: StreamToolCall, Call: &delta.ToolCalls[i]}
			}
			for _, tc := range delta.ToolCalls {
				acc.Handle(StreamChunk{Event: StreamToolCall, Call: &tc})
			}
			if delta.Content != "" {
				acc.Handle(StreamChunk{Event: StreamText, Text: delta.Content})
			}
			// finish_reason is by definition the end of the completion: the
			// provider sets it on the last chunk that carries the answer, and
			// [DONE] merely confirms it. Returning here — for any reason, not
			// only tool_calls — is what keeps a provider that omits [DONE] and
			// holds the connection open from hanging the caller until the
			// client timeout expires.
			//
			// The one exception is a stream that asked OpenAI for its usage and has not had it:
			// OpenAI sends it on the next chunk and then [DONE], so reading on cannot hang there.
			if chunk.Choices[0].FinishReason != "" {
				if wantUsage && usage.Total() == 0 {
					finished = true
					continue
				}
				done()
				return
			}
		}
	}()
	return out, nil
}

func toOpenAIMessages(messages []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, m := range messages {
		role := m.Role
		if role == "" {
			role = "user"
		}
		out = append(out, openAIMessage{
			Role:       role,
			Content:    m.Content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		})
	}
	return out
}

// --- Anthropic --------------------------------------------------------------

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		ID   string `json:"id"`
		Name string `json:"name"`
		// Input is the Anthropic function arguments object.
		Input map[string]any `json:"input"`
	} `json:"content"`
	Usage *anthropicUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) callAnthropic(ctx context.Context, messages []Message) (string, error) {
	// Anthropic receives the system prompt separately and does not accept the
	// "system" role inside the list.
	var system string
	conversation := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		if m.Role == "system" {
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
			continue
		}
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		conversation = append(conversation, map[string]any{
			"role": role,
			"content": []map[string]any{
				{"type": "text", "text": m.Content},
			},
		})
	}

	body := map[string]any{
		"model":       c.cfg.Model,
		"messages":    conversation,
		"max_tokens":  maxInt(c.cfg.MaxTokens, 1),
		"temperature": c.cfg.Temperature,
	}
	if system != "" {
		body["system"] = system
	}

	data, err := c.post(ctx, "/v1/messages", body)
	if err != nil {
		return "", err
	}
	var resp anthropicResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("unreadable Anthropic response: %w", err)
	}
	recordUsage(ctx, resp.Usage.usage())
	if resp.Error != nil && resp.Error.Message != "" {
		return "", &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	var sb strings.Builder
	for _, part := range resp.Content {
		if part.Type == "text" || part.Type == "" {
			sb.WriteString(part.Text)
		}
	}
	if sb.Len() == 0 {
		return "", errors.New("anthropic returned a response with no text")
	}
	return sb.String(), nil
}

func (c *Client) callAnthropicTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	system, conversation := toAnthropicMessages(messages)

	body := map[string]any{
		"model":       c.cfg.Model,
		"messages":    conversation,
		"max_tokens":  maxInt(c.cfg.MaxTokens, 1),
		"temperature": c.cfg.Temperature,
		"tools":       toAnthropicTools(tools),
	}
	if system != "" {
		body["system"] = system
	}

	data, err := c.post(ctx, "/v1/messages", body)
	if err != nil {
		return Reply{}, err
	}
	var resp anthropicResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Reply{}, fmt.Errorf("unreadable Anthropic response: %w", err)
	}
	recordUsage(ctx, resp.Usage.usage())
	if resp.Error != nil && resp.Error.Message != "" {
		return Reply{}, &HTTPError{Code: 400, Body: resp.Error.Message}
	}

	reply := Reply{FinishReason: "stop", Usage: resp.Usage.usage()}
	for _, part := range resp.Content {
		switch part.Type {
		case "text":
			reply.Content += part.Text
		case "tool_use":
			args, _ := json.Marshal(part.Input)
			reply.Calls = append(reply.Calls, ToolCall{
				ID:   part.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      part.Name,
					Arguments: args,
				},
			})
		}
	}
	return reply, nil
}

func toAnthropicTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": toolSchema(t),
		})
	}
	return out
}

// toAnthropicMessages converts the conversation for the Messages API with tools.
//
// Three rules of that API shape it, and each one used to be broken:
//   - a tool result is a tool_result block in a USER turn, and nothing else: the
//     result text was also sent as a text block before it, and Anthropic refuses a
//     user turn whose text precedes its tool_result blocks;
//   - every tool_use must be answered in the very next turn, so the results of
//     parallel calls are merged into ONE user turn (consecutive turns of the same
//     role are merged in general);
//   - a tool_use input is an object, and a text block is never empty.
func toAnthropicMessages(messages []Message) (string, []map[string]any) {
	var system string
	var conversation []map[string]any
	for _, m := range messages {
		if m.Role == "system" {
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		var blocks []map[string]any
		switch {
		case m.Role == "tool" || m.ToolCallID != "":
			blocks = append(blocks, map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     nonEmpty(m.Content),
			})
		default:
			if strings.TrimSpace(m.Content) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := map[string]any{}
				_ = tc.Function.DecodeArguments(&input)
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
		}
		if len(blocks) == 0 {
			blocks = append(blocks, map[string]any{"type": "text", "text": "(empty)"})
		}
		if n := len(conversation); n > 0 && conversation[n-1]["role"] == role {
			prev := conversation[n-1]["content"].([]map[string]any)
			conversation[n-1]["content"] = append(prev, blocks...)
			continue
		}
		conversation = append(conversation, map[string]any{"role": role, "content": blocks})
	}
	return system, conversation
}

// nonEmpty keeps a block's text from being empty, which the Messages API refuses.
func nonEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no output)"
	}
	return s
}

// --- Gemini -----------------------------------------------------------------

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Role  string `json:"role"`
			Parts []struct {
				Text             string          `json:"text"`
				Thought          bool            `json:"thought"`
				FunctionCall     json.RawMessage `json:"functionCall"`
				ThoughtSignature string          `json:"thoughtSignature"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *geminiUsage `json:"usageMetadata"`
	Error         *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type geminiFunctionCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

func (c *Client) callGemini(ctx context.Context, messages []Message) (string, error) {
	var system string
	var contents []map[string]any
	for _, m := range messages {
		if m.Role == "system" {
			system += m.Content + "\n"
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]any{{"text": m.Content}},
		})
	}

	body := map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"maxOutputTokens": c.cfg.MaxTokens,
			"temperature":     c.cfg.Temperature,
		},
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": system}},
		}
	}

	data, err := c.post(ctx, c.geminiPath(), body)
	if err != nil {
		return "", err
	}
	var resp geminiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("unreadable Gemini response: %w", err)
	}
	recordUsage(ctx, resp.UsageMetadata.usage())
	if resp.Error != nil && resp.Error.Message != "" {
		return "", &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	if len(resp.Candidates) == 0 {
		return "", errors.New("gemini returned a response with no candidates (safety block?)")
	}
	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		sb.WriteString(part.Text)
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("gemini returned an empty response (finishReason=%q)", resp.Candidates[0].FinishReason)
	}
	return sb.String(), nil
}

func (c *Client) callGeminiTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	system, contents := toGeminiContents(messages)

	body := map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"maxOutputTokens": c.cfg.MaxTokens,
			"temperature":     c.cfg.Temperature,
		},
	}
	if len(tools) > 0 {
		body["tools"] = []map[string]any{
			{"functionDeclarations": toGeminiToolDeclarations(tools)},
		}
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": system}},
		}
	}

	data, err := c.post(ctx, c.geminiPath(), body)
	if err != nil {
		return Reply{}, err
	}
	var resp geminiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Reply{}, fmt.Errorf("unreadable Gemini response: %w", err)
	}
	recordUsage(ctx, resp.UsageMetadata.usage())
	if resp.Error != nil && resp.Error.Message != "" {
		return Reply{}, &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	if len(resp.Candidates) == 0 {
		return Reply{}, errors.New("gemini returned a response with no candidates (safety block?)")
	}

	reply := Reply{FinishReason: resp.Candidates[0].FinishReason, Usage: resp.UsageMetadata.usage()}
	for i, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" && !part.Thought {
			reply.Content += part.Text
		}
		if len(part.FunctionCall) > 0 {
			var fc geminiFunctionCall
			if err := json.Unmarshal(part.FunctionCall, &fc); err == nil {
				// Gemini names its calls only sometimes; the agent needs an id to pair
				// each result with its call, and the result needs the function NAME.
				id := fc.ID
				if id == "" {
					id = fmt.Sprintf("gemini-%d-%s", i, fc.Name)
				}
				args := fc.Args
				if len(args) == 0 || string(args) == "null" {
					args = json.RawMessage("{}")
				}
				reply.Calls = append(reply.Calls, ToolCall{
					ID:        id,
					Type:      "function",
					Function:  FunctionCall{Name: fc.Name, Arguments: args},
					signature: part.ThoughtSignature,
				})
			}
		}
	}
	if len(reply.Calls) > 0 {
		reply.FinishReason = "tool_calls"
	}
	return reply, nil
}

func toGeminiToolDeclarations(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		decl := map[string]any{
			"name":        t.Function.Name,
			"description": t.Function.Description,
		}
		// Gemini refuses an OBJECT with no properties: a tool that takes nothing is
		// declared with no parameters at all.
		schema := toolSchema(t)
		if props, _ := schema["properties"].(map[string]any); len(props) > 0 {
			decl["parameters"] = schema
		}
		out = append(out, decl)
	}
	return out
}

// geminiSkipSignature is the placeholder Google documents for a function call
// whose thought signature is not available (a history replayed from storage or
// from another model). Gemini 3 refuses a function call without one.
const geminiSkipSignature = "skip_thought_signature_validator"

// toGeminiContents converts the conversation for generateContent with tools.
//
// A tool result is a functionResponse part that names the FUNCTION (not the call
// id, which is what used to be sent) and carries an OBJECT (a bare string is
// refused); the results of parallel calls go in one turn, right after the turn
// that made the calls.
func toGeminiContents(messages []Message) (string, []map[string]any) {
	var system string
	var contents []map[string]any
	names := map[string]string{} // call id -> function name
	for _, m := range messages {
		if m.Role == "system" {
			system += m.Content + "\n"
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		var parts []map[string]any
		if m.Role == "tool" || m.ToolCallID != "" {
			name := names[m.ToolCallID]
			if name == "" {
				name = m.ToolCallID
			}
			parts = append(parts, map[string]any{
				"functionResponse": map[string]any{
					"name":     name,
					"response": map[string]any{"content": m.Content},
				},
			})
		} else {
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for i, tc := range m.ToolCalls {
				names[tc.ID] = tc.Function.Name
				args := map[string]any{}
				_ = tc.Function.DecodeArguments(&args)
				part := map[string]any{
					"functionCall": map[string]any{"name": tc.Function.Name, "args": args},
				}
				// Only the first call of a turn carries the signature.
				if i == 0 {
					sig := tc.signature
					if sig == "" {
						sig = geminiSkipSignature
					}
					part["thoughtSignature"] = sig
				}
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			parts = append(parts, map[string]any{"text": " "})
		}
		if n := len(contents); n > 0 && contents[n-1]["role"] == role {
			prev := contents[n-1]["parts"].([]map[string]any)
			contents[n-1]["parts"] = append(prev, parts...)
			continue
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	return system, contents
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

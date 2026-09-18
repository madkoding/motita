// Package llm implements Layer B: the reasoning engine.
//
// It is a lightweight, SDK-free client that talks to three families of API:
//
//   - openai:    POST /chat/completions  (Bearer)
//   - anthropic: POST /v1/messages       (x-api-key + anthropic-version)
//   - gemini:    POST /v1beta/models/<model>:generateContent (?key=)
//
// Every provider is normalised to the same message structure and back to plain
// text, so the agent loop never needs to know which one is behind it. Parsing of
// structured responses and retrying with exponential backoff live here.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
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
}

// New creates the reasoning engine client.
func New(cfg config.LLM, log *logx.Logger) (*Client, error) {
	if log == nil {
		log = logx.Global()
	}
	switch strings.ToLower(cfg.Provider) {
	case "openai", "ollama", "anthropic", "gemini":
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q", cfg.Provider)
	}
	// Ollama Cloud uses the OpenAI protocol; if no base URL is set we point at the
	// official endpoint so the user only has to provide the key.
	if strings.ToLower(cfg.Provider) == "ollama" && cfg.BaseURL == "" {
		cfg.BaseURL = "https://ollama.com/v1"
	}
	if cfg.APIKey == "" {
		return nil, errors.New("the LLM key is missing")
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

	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
		log:  log,
		sleep: func(d time.Duration) {
			time.Sleep(d)
		},
	}, nil
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
	default:
		// Both OpenAI-compatible hosts and Ollama Cloud speak /chat/completions.
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
	default:
		// Both OpenAI-compatible hosts and Ollama Cloud speak /chat/completions.
		return c.callOpenAITools(ctx, messages, tools)
	}
}

func (c *Client) baseURL(defecto string) string {
	if c.cfg.BaseURL == "" {
		return defecto
	}
	return strings.TrimRight(c.cfg.BaseURL, "/")
}

// ListOllamaModels queries the Ollama /api/tags endpoint and returns the model
// names in the order the host reports them. It is exported so the first-run
// wizard can present the live catalogue without hard-coding it.
func ListOllamaModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %s", resp.Status)
	}

	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("could not decode ollama tags: %w", err)
	}

	names := make([]string, 0, len(payload.Models))
	seen := make(map[string]struct{})
	for _, m := range payload.Models {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

// --- OpenAI ----------------------------------------------------------------

type openAIMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) callOpenAI(ctx context.Context, messages []Message) (string, error) {
	body := map[string]any{
		"model":       c.cfg.Model,
		"messages":    toOpenAIMessages(messages),
		"max_tokens":  c.cfg.MaxTokens,
		"temperature": c.cfg.Temperature,
	}
	url := c.baseURL("https://api.openai.com/v1") + "/chat/completions"
	headers := map[string]string{"Authorization": "Bearer " + c.cfg.APIKey}

	data, err := c.post(ctx, url, headers, body)
	if err != nil {
		return "", err
	}
	var resp openAIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("unreadable OpenAI response: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return "", &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("OpenAI returned empty choices")
	}
	return resp.Choices[0].Message.Content, nil
}

func (c *Client) callOpenAITools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	body := map[string]any{
		"model":       c.cfg.Model,
		"messages":    toOpenAIMessages(messages),
		"tools":       tools,
		"max_tokens":  c.cfg.MaxTokens,
		"temperature": c.cfg.Temperature,
	}
	url := c.baseURL("https://api.openai.com/v1") + "/chat/completions"
	headers := map[string]string{"Authorization": "Bearer " + c.cfg.APIKey}

	data, err := c.post(ctx, url, headers, body)
	if err != nil {
		return Reply{}, err
	}
	var resp openAIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Reply{}, fmt.Errorf("unreadable OpenAI response: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return Reply{}, &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	if len(resp.Choices) == 0 {
		return Reply{}, errors.New("OpenAI returned empty choices")
	}
	choice := resp.Choices[0]
	return Reply{
		Content:      choice.Message.Content,
		Calls:        choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
	}, nil
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

	url := c.baseURL("https://api.anthropic.com") + "/v1/messages"
	headers := map[string]string{
		"x-api-key":         c.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}

	data, err := c.post(ctx, url, headers, body)
	if err != nil {
		return "", err
	}
	var resp anthropicResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("unreadable Anthropic response: %w", err)
	}
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
		return "", errors.New("Anthropic returned a response with no text")
	}
	return sb.String(), nil
}

func (c *Client) callAnthropicTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
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
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		} else if m.Role == "tool" {
			role = "user"
		}
		var blocks []map[string]any
		if m.Content != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Function.Name,
				"input": tc.Function.Arguments,
			})
		}
		if m.ToolCallID != "" {
			blocks = append(blocks, map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			})
		}
		if len(blocks) == 0 {
			blocks = append(blocks, map[string]any{"type": "text", "text": ""})
		}
		conversation = append(conversation, map[string]any{
			"role":    role,
			"content": blocks,
		})
	}

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

	url := c.baseURL("https://api.anthropic.com") + "/v1/messages"
	headers := map[string]string{
		"x-api-key":         c.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}

	data, err := c.post(ctx, url, headers, body)
	if err != nil {
		return Reply{}, err
	}
	var resp anthropicResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Reply{}, fmt.Errorf("unreadable Anthropic response: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return Reply{}, &HTTPError{Code: 400, Body: resp.Error.Message}
	}

	reply := Reply{FinishReason: "stop"}
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
			"name":        t.Function.Name,
			"description": t.Function.Description,
			"input_schema": map[string]any{
				"type":       "object",
				"properties": t.Function.Parameters,
			},
		})
	}
	return out
}

// --- Gemini -----------------------------------------------------------------

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Role  string `json:"role"`
			Parts []struct {
				Text         string          `json:"text"`
				FunctionCall json.RawMessage `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type geminiFunctionCall struct {
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

	base := c.baseURL("https://generativelanguage.googleapis.com")
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", base, c.cfg.Model, c.cfg.APIKey)

	data, err := c.post(ctx, url, nil, body)
	if err != nil {
		return "", err
	}
	var resp geminiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("unreadable Gemini response: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return "", &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	if len(resp.Candidates) == 0 {
		return "", errors.New("Gemini returned a response with no candidates (safety block?)")
	}
	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		sb.WriteString(part.Text)
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("Gemini returned an empty response (finishReason=%q)", resp.Candidates[0].FinishReason)
	}
	return sb.String(), nil
}

func (c *Client) callGeminiTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
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
		var parts []map[string]any
		if m.Content != "" {
			parts = append(parts, map[string]any{"text": m.Content})
		}
		for _, tc := range m.ToolCalls {
			var args json.RawMessage
			if len(tc.Function.Arguments) > 0 {
				var obj map[string]any
				_ = json.Unmarshal(tc.Function.Arguments, &obj)
				args, _ = json.Marshal(obj)
			}
			parts = append(parts, map[string]any{
				"functionCall": map[string]any{
					"name": tc.Function.Name,
					"args": args,
				},
			})
		}
		if m.ToolCallID != "" {
			parts = append(parts, map[string]any{
				"functionResponse": map[string]any{
					"name":     m.ToolCallID,
					"response": m.Content,
				},
			})
		}
		if len(parts) == 0 {
			parts = append(parts, map[string]any{"text": ""})
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": parts,
		})
	}

	body := map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"maxOutputTokens": c.cfg.MaxTokens,
			"temperature":     c.cfg.Temperature,
		},
		"tools": []map[string]any{
			{"functionDeclarations": toGeminiToolDeclarations(tools)},
		},
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": system}},
		}
	}

	base := c.baseURL("https://generativelanguage.googleapis.com")
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", base, c.cfg.Model, c.cfg.APIKey)

	data, err := c.post(ctx, url, nil, body)
	if err != nil {
		return Reply{}, err
	}
	var resp geminiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Reply{}, fmt.Errorf("unreadable Gemini response: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return Reply{}, &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	if len(resp.Candidates) == 0 {
		return Reply{}, errors.New("Gemini returned a response with no candidates (safety block?)")
	}

	reply := Reply{FinishReason: resp.Candidates[0].FinishReason}
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			reply.Content += part.Text
		}
		if len(part.FunctionCall) > 0 {
			var fc geminiFunctionCall
			if err := json.Unmarshal(part.FunctionCall, &fc); err == nil {
				reply.Calls = append(reply.Calls, ToolCall{
					Type: "function",
					Function: FunctionCall{
						Name:      fc.Name,
						Arguments: fc.Args,
					},
				})
			}
		}
	}
	return reply, nil
}

func toGeminiToolDeclarations(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":        t.Function.Name,
			"description": t.Function.Description,
			"parameters": map[string]any{
				"type":       "object",
				"properties": t.Function.Parameters,
			},
		})
	}
	return out
}

// --- transport --------------------------------------------------------------

func (c *Client) post(ctx context.Context, url string, headers map[string]string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("could not serialise the request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err // network error: retryable
	}
	defer resp.Body.Close()

	response, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Code: resp.StatusCode, Body: strings.TrimSpace(string(response))}
	}
	return response, nil
}

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

var jsonBlock = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\}|\\[.*?\\])\\s*```")

// ExtractJSON gets the first JSON object out of an LLM response, tolerating the
// usual decorations: markdown blocks, text before and after, and nested braces.
func ExtractJSON(text string) ([]byte, error) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return nil, errors.New("the LLM response is empty")
	}

	if m := jsonBlock.FindStringSubmatch(clean); len(m) == 2 {
		return []byte(m[1]), nil
	}

	// First '{' and its balanced partner, respecting strings and escapes.
	start := strings.IndexAny(clean, "{[")
	if start < 0 {
		return nil, fmt.Errorf("the response contains no JSON: %q", truncate(clean, 200))
	}
	open := clean[start]
	closing := byte('}')
	if open == '[' {
		closing = ']'
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(clean); i++ {
		ch := clean[i]
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
			// nothing
		case ch == open:
			depth++
		case ch == closing:
			depth--
			if depth == 0 {
				return []byte(clean[start : i+1]), nil
			}
		}
	}
	return nil, fmt.Errorf("the JSON in the response is truncated: %q", truncate(clean, 200))
}

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

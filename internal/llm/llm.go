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
	Role    string // system | user | assistant
	Content string
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
	case "openai", "anthropic", "gemini":
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q", cfg.Provider)
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
		return c.callOpenAI(ctx, messages)
	}
}

func (c *Client) baseURL(defecto string) string {
	if c.cfg.BaseURL == "" {
		return defecto
	}
	return strings.TrimRight(c.cfg.BaseURL, "/")
}

// --- OpenAI ----------------------------------------------------------------

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
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

func toOpenAIMessages(messages []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, m := range messages {
		role := m.Role
		if role == "" {
			role = "user"
		}
		out = append(out, openAIMessage{Role: role, Content: m.Content})
	}
	return out
}

// --- Anthropic --------------------------------------------------------------

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
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

// --- Gemini -----------------------------------------------------------------

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
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

package mcp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// Tool is one thing a server can do.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Tools lists every tool of the server, following its pages.
func (c *Client) Tools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := c.Request(ctx, "tools/list", params, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// Content is one piece of a tool's answer.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
	Resource *struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"resource,omitempty"`
}

// CallResult is what a tool answered. IsError is the tool saying it failed, which is an answer
// like any other and not a failure of the protocol.
type CallResult struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// Call runs a tool with arguments (a JSON object, or empty for none).
func (c *Client) Call(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	var res CallResult
	err := c.Request(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res)
	return res, err
}

// Text is the answer as text for the model, cut to limit bytes. A picture or a file is named,
// not carried: the bytes of a binary would only spend the context.
func (r CallResult) Text(limit int) string {
	var parts []string
	for _, ct := range r.Content {
		switch {
		case ct.Type == "text":
			parts = append(parts, ct.Text)
		case ct.Resource != nil:
			parts = append(parts, ct.Resource.URI+"\n"+ct.Resource.Text)
		default:
			parts = append(parts, "["+ct.Type+" "+ct.MIMEType+" "+ct.URI+" not shown]")
		}
	}
	if len(parts) == 0 && len(r.StructuredContent) > 0 {
		parts = append(parts, string(r.StructuredContent))
	}
	out := strings.Join(parts, "\n")
	if len(out) > limit {
		out = out[:limit] + "\n[cut: " + "the answer is longer than " + strconv.Itoa(limit) + " bytes]"
	}
	return out
}

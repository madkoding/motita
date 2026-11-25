package mcp

import "encoding/json"

// ELICITATION: the server stops in the middle of a call and asks the person something. It is
// how a server confirms before it deletes, tells the cost before it spends, asks for a value
// or sends the person to a page (a payment, a sign-in) so that the value never passes through
// this program.
//
// Two modes exist. "form" carries a flat JSON schema of the values wanted; "url" carries a
// link. The answer is accept (with the values), decline, or cancel (the person did not choose).

// Actions of an elicitation answer.
const (
	Accept  = "accept"
	Decline = "decline"
	Cancel  = "cancel"
)

// ElicitRequest is a question of a server.
type ElicitRequest struct {
	// Server is the name the person gave the server that asks.
	Server  string `json:"-"`
	Mode    string `json:"mode,omitempty"`
	Message string `json:"message"`
	// Schema describes the values asked for in form mode.
	Schema json.RawMessage `json:"requestedSchema,omitempty"`
	// URL is the page the person is sent to in url mode.
	URL           string `json:"url,omitempty"`
	ElicitationID string `json:"elicitationId,omitempty"`
}

// ElicitResult is the answer to a question of a server.
type ElicitResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

// serveElicit answers an elicitation/create request. A client without a handler cannot ask the
// person, and says so rather than answering for them.
func (c *Client) serveElicit(m message) {
	if c.handler.Elicit == nil {
		c.reply(m.ID, nil, &rpcError{Code: -32601, Message: "elicitation is not supported"})
		return
	}
	var req ElicitRequest
	if err := json.Unmarshal(m.Params, &req); err != nil {
		c.reply(m.ID, nil, &rpcError{Code: -32602, Message: "invalid elicitation: " + err.Error()})
		return
	}
	req.Server = c.name
	if req.Mode == "" {
		req.Mode = "form"
	}
	res := c.handler.Elicit(c.activeContext(), req)
	if res.Action != Accept && res.Action != Decline {
		res.Action = Cancel
	}
	if res.Action != Accept {
		res.Content = nil
	}
	c.reply(m.ID, res, nil)
}

// Defaults returns the values of a form schema that have a default, and the names of the
// required properties that have none: the values this program can answer for without
// inventing anything.
func Defaults(schema json.RawMessage) (values map[string]any, missing []string) {
	var s struct {
		Properties map[string]struct {
			Default any `json:"default"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	values = map[string]any{}
	_ = json.Unmarshal(schema, &s)
	for name, p := range s.Properties {
		if p.Default != nil {
			values[name] = p.Default
		}
	}
	for _, name := range s.Required {
		if _, ok := values[name]; !ok {
			missing = append(missing, name)
		}
	}
	return values, missing
}

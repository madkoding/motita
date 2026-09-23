// The client half: a tui.Runner that reaches the agent through the gateway.
//
// It exists so the text interface can be a CLIENT of its own process's gateway instead of a
// second door into the agent. Two doors would be two places the conversation lives, and the phone
// and the terminal would then be talking to different agents.
//
// Two rules shape it, and both are tests in client_test.go:
//
//   - THE REPAINT PATH NEVER TOUCHES THE NETWORK. tui/render.go calls Config() and
//     ConversationSummary() on every frame - statusLines, stateGlyph, contextLabel - and a round
//     trip there is a stutter the user feels while typing. Both are cached.
//   - THE WIZARD IS A FRONT END CAPABILITY. It reads lines from the terminal it was launched
//     from (internal/onboard.Run takes its input as a reader), so it cannot travel over a socket.
//     Embedded, it is handed in; remote, it is refused with a reason.
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/session"
)

// Client speaks to a gateway.
type Client struct {
	baseURL string
	token   string

	// session is the conversation this client speaks for. Every scoped endpoint is addressed by
	// it, so the client never builds a path by hand and cannot address one conversation while
	// believing it is on another.
	//
	// It is set at construction and NOT changed from outside, which is the plan's rule for this
	// step: a client whose session moves under it is a client whose status bar can show one
	// conversation while its run lands in another. Moving between conversations is a capability
	// of its own and arrives later, with the interfaces that can use it.
	session string

	// http is the streaming client. Its Timeout is deliberately zero: a deadline on a response
	// is a deadline on the agent's work, and half of these responses are a run that streams until
	// it is done. Cancellation comes from the context, which is where a user's Ctrl+C arrives.
	http *http.Client

	mu     sync.Mutex
	cfg    config.Config
	hasCfg bool
	snap   session.Snapshot

	approverMu sync.Mutex
	approver   agent.Approver

	// Wizard is the first-run configuration wizard, for a client running on the machine that
	// hosts the gateway. Nil means there is none, and RunConfig says so.
	Wizard func(ctx context.Context) error
}

// NewClient returns a client for the gateway at baseURL, speaking for the default conversation.
//
// The default is not a fallback: it is the conversation every gateway HAS, and the one an
// embedded client has always used. A remote client that wants another one says so with
// NewClientForSession, so the common case stays one argument shorter.
func NewClient(baseURL, token string) *Client {
	return NewClientForSession(baseURL, token, DefaultSession)
}

// NewClientForSession returns a client that speaks for one named conversation.
func NewClientForSession(baseURL, token, session string) *Client {
	id := strings.TrimSpace(session)
	if id == "" {
		id = DefaultSession
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		session: id,
		http:    &http.Client{},
	}
}

// Session is the conversation this client speaks for.
func (c *Client) Session() string { return c.session }

// scoped builds the path of one endpoint of this client's conversation.
//
// Every request goes through it, so there is ONE place that knows where sessions live in the URL
// - and no call site can address the wrong conversation by writing a path by hand.
func (c *Client) scoped(rest string) string {
	return "/v1/sessions/" + url.PathEscape(c.session) + rest
}

// CreateSession opens one more conversation and returns it.
//
// The client that calls this does NOT adopt it. Its session was fixed at construction and nothing
// about a running client changes underneath it, so a caller that wants to drive the new
// conversation builds a second client with the id it gets back. That is the same rule seen from
// the other end, and it is what keeps a client's status bar and its runs talking about the same
// conversation.
func (c *Client) CreateSession(ctx context.Context) (SessionStatus, error) {
	var out SessionStatus
	// Not scoped: opening a conversation is how a client FINDS one, so it cannot name one.
	if err := c.do(ctx, http.MethodPost, "/v1/sessions", nil, &out); err != nil {
		return SessionStatus{}, err
	}
	return out, nil
}

// ListSessions answers what the gateway is holding.
func (c *Client) ListSessions(ctx context.Context) ([]SessionStatus, error) {
	var out struct {
		Sessions []SessionStatus `json:"sessions"`
	}
	// Not scoped, for the same reason as CreateSession.
	if err := c.do(ctx, http.MethodGet, "/v1/sessions", nil, &out); err != nil {
		return nil, err
	}
	if out.Sessions == nil {
		// An empty LIST rather than null: a caller that has to tell "none" from "the field is
		// missing" is a caller with a bug waiting to happen.
		out.Sessions = []SessionStatus{}
	}
	return out.Sessions, nil
}

// CloseSession drops one conversation. It is the other session's id that is named, never this
// client's: closing what you are speaking for would leave this client addressing nothing.
func (c *Client) CloseSession(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/sessions/"+url.PathEscape(id), nil, nil)
}

// SetApprover installs the callback a consequential command is confirmed through.
//
// The interface installs one on every runner that accepts it (tui.Run calls installApprover), and
// this is where it lands: the approval arrives on the run's stream, this callback decides, and the
// answer goes back over POST /v1/runs/approval.
func (c *Client) SetApprover(fn agent.Approver) {
	c.approverMu.Lock()
	defer c.approverMu.Unlock()
	c.approver = fn
}

// Config returns the configuration, from the cache.
//
// See the package comment: the repaint path must not reach the network. tui.Runner declares that
// this cannot fail, so a gateway that cannot be reached answers with the default configuration
// rather than with a lie - the calls that CAN report a failure are the ones that do.
func (c *Client) Config() config.Config {
	c.mu.Lock()
	if c.hasCfg {
		cfg := c.cfg
		c.mu.Unlock()
		return cfg
	}
	c.mu.Unlock()

	var v configView
	if err := c.getJSON(context.Background(), c.scoped("/config"), &v); err != nil {
		return config.Default()
	}
	cfg := configFromView(v)
	c.mu.Lock()
	c.cfg, c.hasCfg = cfg, true
	c.mu.Unlock()
	return cfg
}

// SetReasoning changes the level on the gateway and refreshes the cache, so /reasoning shows the
// level it just set instead of the one from before.
func (c *Client) SetReasoning(level string) {
	_ = c.postJSON(context.Background(), c.scoped("/reasoning"), map[string]string{"level": level}, nil)
	// The cache is dropped rather than patched: the gateway is the authority on what the level
	// now is, and re-reading it is one request instead of a guess that can drift.
	c.mu.Lock()
	c.hasCfg = false
	c.mu.Unlock()
}

// ConversationSummary returns the session figures, from the cache when there are any.
//
// A zero Snapshot means no conversation has started, which the caller renders as no figure at all
// rather than as a percentage of nothing - so a genuine zero is re-read rather than trusted.
func (c *Client) ConversationSummary() session.Snapshot {
	c.mu.Lock()
	snap := c.snap
	c.mu.Unlock()
	if snap.Window > 0 {
		return snap
	}
	return c.fetchSummary()
}

// fetchSummary reads the figures once and caches them.
func (c *Client) fetchSummary() session.Snapshot {
	var snap session.Snapshot
	if err := c.getJSON(context.Background(), c.scoped(""), &snap); err != nil {
		return session.Snapshot{}
	}
	c.mu.Lock()
	c.snap = snap
	c.mu.Unlock()
	return snap
}

// ConversationReport is a one-shot read, so it goes to the network.
//
// The failure is reported IN the text because tui.Runner says this cannot fail: a report that
// could not be read is the report, and "the gateway could not be reached" is what the user needs
// to see rather than an empty panel.
func (c *Client) ConversationReport() string {
	var out struct {
		Text string `json:"text"`
	}
	if err := c.getJSON(context.Background(), c.scoped("/report"), &out); err != nil {
		return "the gateway could not be reached: " + err.Error()
	}
	return out.Text
}

// ResetConversation clears the conversation on the gateway and drops the cached figures with it.
func (c *Client) ResetConversation() {
	_ = c.postJSON(context.Background(), c.scoped("/reset"), struct{}{}, nil)
	c.mu.Lock()
	c.snap = session.Snapshot{}
	c.mu.Unlock()
}

// RunModels asks the gateway for the catalogue report.
func (c *Client) RunModels(ctx context.Context) (string, error) {
	var out struct {
		Text string `json:"text"`
	}
	if err := c.do(ctx, http.MethodGet, c.scoped("/models"), nil, &out); err != nil {
		return "", err
	}
	return out.Text, nil
}

// RecordVerdict applies a verdict and returns the gateway's report.
func (c *Client) RecordVerdict(good bool, note string) string {
	var out struct {
		Text string `json:"text"`
	}
	if err := c.do(context.Background(), http.MethodPost, c.scoped("/verdict"), map[string]any{"good": good, "note": note}, &out); err != nil {
		return "the gateway could not be reached: " + err.Error()
	}
	return out.Text
}

// RewardReport is a one-shot read.
func (c *Client) RewardReport() string {
	var out struct {
		Text string `json:"text"`
	}
	if err := c.getJSON(context.Background(), c.scoped("/reward"), &out); err != nil {
		return "the gateway could not be reached: " + err.Error()
	}
	return out.Text
}

// TakePendingQuestions returns the questions of the last turn and clears them on the gateway.
func (c *Client) TakePendingQuestions() ([]agent.AskItem, string) {
	var out struct {
		Items  []agent.AskItem `json:"items"`
		Origin string          `json:"origin"`
	}
	if err := c.getJSON(context.Background(), c.scoped("/questions"), &out); err != nil {
		return nil, ""
	}
	return out.Items, out.Origin
}

// RunConfig runs the first-run wizard, when this client has one.
//
// It cannot be proxied, and that is not a missing feature: the wizard reads lines from a terminal,
// so the only machine that can run it is the one the user is sitting at. Embedded, that is the
// same machine as the gateway and the runner is handed over directly. Remote, there is nothing to
// hand over, and saying so is more useful than a wizard that cannot read its answers.
func (c *Client) RunConfig(ctx context.Context) error {
	if c.Wizard == nil {
		return errors.New("the configuration wizard runs on the machine hosting the gateway, in the terminal it was started from; this client has no terminal to run it in")
	}
	return c.Wizard(ctx)
}

// RunPlan runs the planner and returns the answer, streaming the progress lines.
func (c *Client) RunPlan(ctx context.Context, prompt string, progress func(string, ...any)) (string, error) {
	return c.run(ctx, c.scoped("/plan"), map[string]any{"prompt": prompt}, progress)
}

// RunTask runs a task and returns the summary, streaming the progress lines.
func (c *Client) RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
	return c.run(ctx, c.scoped("/task"), map[string]any{"task": task}, progress)
}

// run is the shared streaming path of both modes.
func (c *Client) run(ctx context.Context, path string, body any, progress func(string, ...any)) (string, error) {
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(body); err != nil {
		return "", fmt.Errorf("could not encode the request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, &payload)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", refusalError(resp)
	}

	var result string
	err = streamEvents(ctx, resp.Body, func(event string, data []byte) error {
		switch event {
		case EventProgress:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(data, &p); err != nil {
				return err
			}
			progress("%s", p.Text)
		case EventApproval:
			return c.answerApproval(ctx, data)
		case EventDone:
			var d doneEvent
			if err := json.Unmarshal(data, &d); err != nil {
				return err
			}
			result = d.Result
			// The figures that came WITH the answer replace the cache, so the status bar is
			// right the instant the turn ends instead of one request later.
			c.mu.Lock()
			c.snap = d.Session
			c.mu.Unlock()
		case EventError:
			var e struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(data, &e); err != nil {
				return err
			}
			return errors.New(e.Error)
		}
		// Any other event name is ignored and the stream keeps being read: a newer gateway may
		// add an event, and an older client must not break on it.
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

// answerApproval asks the installed approver and sends the answer back.
//
// With NO approver installed the answer is no. It is the same rule the agent already follows: a
// consequential command with nobody to ask is refused, and silence is not consent.
func (c *Client) answerApproval(ctx context.Context, data []byte) error {
	var ask approvalEvent
	if err := json.Unmarshal(data, &ask); err != nil {
		return err
	}

	c.approverMu.Lock()
	fn := c.approver
	c.approverMu.Unlock()

	approve := false
	if fn != nil {
		ok, err := fn(ctx, agent.ApprovalRequest{Command: ask.Command, Reason: ask.Reason, Rule: ask.Rule})
		// An approver that fails is a refusal, for the same reason a cancelled one is.
		if err == nil {
			approve = ok
		}
	}

	if err := c.do(ctx, http.MethodPost, c.scoped("/runs/approval"), map[string]any{"id": ask.ID, "approve": approve}, nil); err != nil {
		return fmt.Errorf("the approval could not be sent back: %w", err)
	}
	return nil
}

// streamEvents reads the wire format the server writes: one data line per event, the event name on
// the line before it, a blank line between events.
//
// The payload is JSON, which never contains a raw newline (JSON escapes them), so one line per
// event is a real invariant and not a simplification - and it is what lets this dispatch on the
// data line instead of buffering multi-line events. The buffer is grown past the default 64 KB
// scanner limit because a progress line can carry a long tool result.
func streamEvents(ctx context.Context, body io.Reader, fn func(event string, data []byte) error) error {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	event := ""
	for sc.Scan() {
		// A cancelled context stops the read: the run is over and this goroutine must not sit on
		// a socket waiting for lines nobody will send.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := fn(event, []byte(strings.TrimPrefix(line, "data: "))); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// refusalError turns a non-200 answer into the message the interface will show.
//
// The gateway's refusals carry {"error": "..."}, and that reason is what a user can act on. A
// status code alone would leave them guessing, and a 409's explanation ("a run is already in
// progress") is the difference between a puzzle and a message.
func refusalError(resp *http.Response) error {
	var e struct {
		Error string `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return errors.New(e.Error)
	}
	return fmt.Errorf("the gateway answered %d", resp.StatusCode)
}

// getJSON performs an authenticated GET and decodes the answer.
func (c *Client) getJSON(ctx context.Context, path string, dst any) error {
	return c.do(ctx, http.MethodGet, path, nil, dst)
}

// postJSON performs an authenticated POST with a JSON body.
func (c *Client) postJSON(ctx context.Context, path string, body, dst any) error {
	return c.do(ctx, http.MethodPost, path, body, dst)
}

// do is the one-shot request path.
func (c *Client) do(ctx context.Context, method, path string, body, dst any) error {
	var payload io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return fmt.Errorf("could not encode the request: %w", err)
		}
		payload = &buf
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Every 4xx and 5xx carries a reason on this gateway, so one branch covers them all.
	if resp.StatusCode >= http.StatusBadRequest {
		return refusalError(resp)
	}
	if dst == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

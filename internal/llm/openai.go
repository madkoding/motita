package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type openAIMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"` // the text, or text and image parts (see openAIContent)
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
			"again unchanged returns the same thing",
			ErrToolCallForText, len(choice.Message.ToolCalls), describeCalls(choice.Message.ToolCalls))}
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

func (c *Client) callOpenAIToolsStream(
	ctx context.Context, messages []Message, tools []Tool,
) (<-chan StreamChunk, error) {
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
		s := &openAIStream{ctx: ctx, out: out, wantUsage: wantUsage, src: c.source()}
		s.read(bufio.NewReader(resp.Body))
	}()
	return out, nil
}

// openAIStream reads a /chat/completions stream into chunks for the caller.
type openAIStream struct {
	ctx       context.Context
	src       usageSource
	out       chan<- StreamChunk
	wantUsage bool
	acc       StreamResult
	usage     Usage
	// finished is set when the answer ended but the usage asked for has not arrived yet.
	finished bool
}

// read forwards the stream until it ends.
func (s *openAIStream) read(reader *bufio.Reader) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			s.end(err)
			return
		}
		payload, ok := ssePayload(line)
		if !ok {
			continue
		}
		if string(payload) == "[DONE]" {
			s.done()
			return
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			continue
		}
		if s.handle(chunk) {
			return
		}
	}
}

// ssePayload is the data of one server-sent event line; comments, blank lines and other fields
// carry none. The space after "data:" is optional in SSE.
func ssePayload(line []byte) ([]byte, bool) {
	payload, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
	return bytes.TrimSpace(payload), ok
}

// end closes the stream on a read error: the end of the body is the end of the answer.
func (s *openAIStream) end(err error) {
	if err == io.EOF {
		s.done()
	} else {
		s.out <- StreamChunk{Event: StreamError, Error: err}
	}
}

// done sends the whole reply, with its usage.
func (s *openAIStream) done() {
	reply := s.acc.FinalReply()
	reply.Usage = s.usage
	recordUsage(s.ctx, s.src, s.usage)
	s.out <- StreamChunk{Event: StreamDone, Reply: reply}
}

// handle reads one chunk, and reports whether it ended the stream.
func (s *openAIStream) handle(chunk openAIStreamChunk) bool {
	if u := chunk.Usage.usage(); u.Total() > 0 {
		s.usage = u
		if s.finished {
			// The usage chunk is the last thing a stream that asked for it sends.
			s.done()
			return true
		}
	}
	if len(chunk.Choices) == 0 {
		return false
	}
	s.emit(chunk.Choices[0].Delta)
	// finish_reason is by definition the end of the completion: the
	// provider sets it on the last chunk that carries the answer, and
	// [DONE] merely confirms it. Returning here — for any reason, not
	// only tool_calls — is what keeps a provider that omits [DONE] and
	// holds the connection open from hanging the caller until the
	// client timeout expires.
	//
	// The one exception is a stream that asked OpenAI for its usage and has not had it:
	// OpenAI sends it on the next chunk and then [DONE], so reading on cannot hang there.
	if chunk.Choices[0].FinishReason == "" {
		return false
	}
	if s.wantUsage && s.usage.Total() == 0 {
		s.finished = true
		return false
	}
	s.done()
	return true
}

// emit forwards a delta's thinking, text and tool calls, and adds them to the reply.
func (s *openAIStream) emit(delta openAIStreamDelta) {
	if thought := delta.ReasoningContent + delta.Reasoning; thought != "" {
		s.out <- StreamChunk{Event: StreamThinking, Text: thought}
	}
	if delta.Content != "" {
		s.out <- StreamChunk{Event: StreamText, Text: delta.Content}
	}
	for i := range delta.ToolCalls {
		s.out <- StreamChunk{Event: StreamToolCall, Call: &delta.ToolCalls[i]}
	}
	for _, tc := range delta.ToolCalls {
		s.acc.Handle(StreamChunk{Event: StreamToolCall, Call: &tc})
	}
	if delta.Content != "" {
		s.acc.Handle(StreamChunk{Event: StreamText, Text: delta.Content})
	}
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
			Content:    openAIContent(m.Content, m.Images),
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		})
	}
	return out
}

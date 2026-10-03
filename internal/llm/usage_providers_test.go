package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// metered is a context whose calls record into a fresh Meter.
func metered() (context.Context, *Meter) {
	m := &Meter{}
	return WithMeter(context.Background(), m), m
}

func TestUsageBlocksOfEveryProvider(t *testing.T) {
	cases := []struct {
		name string
		got  Usage
		want Usage
	}{
		{"openai nil", (*openAIUsage)(nil).usage(), Usage{}},
		{"openai cached part is not input", (&openAIUsage{PromptTokens: 100, CompletionTokens: 7,
			PromptTokensDetails: &struct {
				CachedTokens int64 `json:"cached_tokens"`
			}{CachedTokens: 60}}).usage(), Usage{Input: 40, Output: 7, CacheRead: 60}},
		{"openai without details", (&openAIUsage{PromptTokens: 5, CompletionTokens: 1}).usage(), Usage{Input: 5, Output: 1}},
		{"responses nil", (*responsesUsage)(nil).usage(), Usage{}},
		{"responses cached part is not input", (&responsesUsage{InputTokens: 30, OutputTokens: 2,
			InputTokensDetails: &struct {
				CachedTokens int64 `json:"cached_tokens"`
			}{CachedTokens: 10}}).usage(), Usage{Input: 20, Output: 2, CacheRead: 10}},
		{"responses without details", (&responsesUsage{InputTokens: 3, OutputTokens: 4}).usage(), Usage{Input: 3, Output: 4}},
		{"anthropic nil", (*anthropicUsage)(nil).usage(), Usage{}},
		{"anthropic", (&anthropicUsage{InputTokens: 2, OutputTokens: 52, CacheReadInputTokens: 9,
			CacheCreationInputTokens: 3080}).usage(), Usage{Input: 2, Output: 52, CacheRead: 9, CacheWrite: 3080}},
		{"gemini nil", (*geminiUsage)(nil).usage(), Usage{}},
		{"gemini thinking is output", (&geminiUsage{PromptTokenCount: 50, CandidatesTokenCount: 5,
			CachedContentTokenCount: 20, ThoughtsTokenCount: 8}).usage(), Usage{Input: 30, Output: 13, CacheRead: 20}},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, c.got, c.want)
		}
	}
}

const openAIUsageJSON = `"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":2}}`

func TestOpenAICallsRecordTheirUsage(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, `{"choices":[{"message":{"content":"ok"}}],`+openAIUsageJSON+`}`)
	c := clientFor(t, srv.URL, nil)
	ctx, m := metered()
	if _, err := c.Complete(ctx, hi); err != nil {
		t.Fatal(err)
	}
	reply, err := c.CompleteTools(ctx, hi, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{Input: 10, Output: 3, CacheRead: 2}
	if reply.Usage != want {
		t.Errorf("reply usage = %+v, want %+v", reply.Usage, want)
	}
	if m.Usage() != want.Add(want) || m.Calls() != 2 {
		t.Errorf("meter = %+v over %d calls", m.Usage(), m.Calls())
	}
}

// TestARefusedReplyWasStillBilled: an answer the text path refuses (empty) is still a call the
// provider charged for, and it is counted.
func TestARefusedReplyWasStillBilled(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, `{"choices":[{"message":{"content":""},"finish_reason":"stop"}],`+openAIUsageJSON+`}`)
	c := clientFor(t, srv.URL, nil)
	ctx, m := metered()
	if _, err := c.Complete(ctx, hi); err == nil {
		t.Fatal("an empty answer must be refused")
	}
	if m.Calls() != 1 {
		t.Errorf("calls = %d, want 1", m.Calls())
	}
}

func TestAStreamWithUsageOnItsLastChunkIsRead(t *testing.T) {
	srv := sseServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}],"+openAIUsageJSON+"}\n\n", http.StatusOK)
	c := clientFor(t, srv.URL, nil)
	ctx, m := metered()
	var reply Reply
	for ch := range c.CompleteToolsStream(ctx, hi, nil) {
		if ch.Event == StreamDone {
			reply = ch.Reply
		}
	}
	if want := (Usage{Input: 10, Output: 3, CacheRead: 2}); reply.Usage != want || m.Usage() != want {
		t.Errorf("reply %+v meter %+v, want %+v", reply.Usage, m.Usage(), want)
	}
}

// toServer sends every request to srv, whatever host it names: the client keeps believing it
// talks to api.openai.com.
type toServer struct{ srv *httptest.Server }

func (s toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(s.srv.URL)
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(r)
}

// officialOpenAI is a client of api.openai.com whose requests reach handler, and the bodies it
// sent.
func officialOpenAI(t *testing.T, handler func(w http.ResponseWriter)) (*Client, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		handler(w)
	}))
	t.Cleanup(srv.Close)
	c := clientFor(t, "https://api.openai.com/v1", nil)
	c.http = &http.Client{Transport: toServer{srv}, Timeout: 5 * time.Second}
	return c, &bodies
}

func streamReply(t *testing.T, c *Client, ctx context.Context) Reply {
	t.Helper()
	var reply Reply
	got := false
	for ch := range c.CompleteToolsStream(ctx, hi, nil) {
		if ch.Event == StreamDone {
			reply, got = ch.Reply, true
		}
	}
	if !got {
		t.Fatal("the stream must end with a reply")
	}
	return reply
}

// TestOpenAIIsAskedForTheUsageOfAStream: OpenAI sends a stream's usage only when asked, on a chunk
// after the finish_reason one; the stream reads on for it instead of ending at finish_reason.
func TestOpenAIIsAskedForTheUsageOfAStream(t *testing.T) {
	c, bodies := officialOpenAI(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],"+openAIUsageJSON+"}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	ctx, m := metered()
	reply := streamReply(t, c, ctx)
	if want := (Usage{Input: 10, Output: 3, CacheRead: 2}); reply.Usage != want || m.Usage() != want || reply.Content != "hi" {
		t.Errorf("reply %+v meter %+v", reply, m.Usage())
	}
	opts, _ := (*bodies)[0]["stream_options"].(map[string]any)
	if opts["include_usage"] != true {
		t.Errorf("the request must ask for the usage: %v", (*bodies)[0])
	}
}

// TestAnOpenAIStreamWithoutItsUsageStillEnds: a stream that asked for its usage and ended (EOF or
// [DONE]) without it still delivers its reply.
func TestAnOpenAIStreamWithoutItsUsageStillEnds(t *testing.T) {
	for name, tail := range map[string]string{"done": "data: [DONE]\n\n", "eof": ""} {
		c, _ := officialOpenAI(t, func(w http.ResponseWriter) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"+tail)
		})
		ctx, m := metered()
		if reply := streamReply(t, c, ctx); reply.Content != "hi" || m.Calls() != 0 {
			t.Errorf("%s: reply %+v, calls %d", name, reply, m.Calls())
		}
	}
}

// TestOtherOpenAICompatibleServersAreNotAskedForUsage: the field is not sent where it may be
// refused, and the stream still ends at finish_reason there.
func TestOtherOpenAICompatibleServersAreNotAskedForUsage(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	t.Cleanup(srv.Close)
	c := clientFor(t, srv.URL, func(cfg *config.LLM) { cfg.Provider = "ollama" })
	_ = streamReply(t, c, context.Background())
	if _, ok := body["stream_options"]; ok {
		t.Errorf("stream_options must not be sent to %s: %v", srv.URL, body)
	}
}

func TestAnthropicCallsRecordTheirUsage(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":4,"output_tokens":2,"cache_read_input_tokens":1,"cache_creation_input_tokens":8}}`)
	c := clientFor(t, srv.URL, func(cfg *config.LLM) { cfg.Provider = "anthropic" })
	ctx, m := metered()
	if _, err := c.Complete(ctx, hi); err != nil {
		t.Fatal(err)
	}
	reply, err := c.CompleteTools(ctx, hi, []Tool{})
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{Input: 4, Output: 2, CacheRead: 1, CacheWrite: 8}
	if reply.Usage != want || m.Usage() != want.Add(want) {
		t.Errorf("reply %+v meter %+v", reply.Usage, m.Usage())
	}
	// A provider answered without streaming still streams its usage.
	ctx, m = metered()
	if r := streamReply(t, c, ctx); r.Usage != want || m.Usage() != want {
		t.Errorf("streamed reply %+v meter %+v", r.Usage, m.Usage())
	}
}

func TestGeminiCallsRecordTheirUsage(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":2,"cachedContentTokenCount":4,"thoughtsTokenCount":1}}`)
	c := clientFor(t, srv.URL, func(cfg *config.LLM) { cfg.Provider = "gemini" })
	ctx, m := metered()
	if _, err := c.Complete(ctx, hi); err != nil {
		t.Fatal(err)
	}
	reply, err := c.CompleteTools(ctx, hi, []Tool{})
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{Input: 5, Output: 3, CacheRead: 4}
	if reply.Usage != want || m.Usage() != want.Add(want) {
		t.Errorf("reply %+v meter %+v", reply.Usage, m.Usage())
	}
}

func TestResponsesStreamRecordsItsUsage(t *testing.T) {
	c := codexStream(t, `data: {"type":"response.output_text.delta","delta":"hi"}`+"\n"+
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":5,"input_tokens_details":{"cached_tokens":15}}}}`+"\n", nil)
	ctx, m := metered()
	reply, err := c.callResponses(ctx, hi, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{Input: 5, Output: 5, CacheRead: 15}
	if reply.Usage != want || m.Usage() != want || m.Calls() != 1 {
		t.Errorf("reply %+v meter %+v over %d calls", reply.Usage, m.Usage(), m.Calls())
	}
}

func usageDelta(stop string, u string) string {
	return fmt.Sprintf(`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":%q},"usage":%s}}`, stop, u)
}

// TestClaudeCodeAddsUpTheMessagesOfATurn: a turn that ends at message_stop is never followed by a
// result the client reads, so its usage is the sum of its messages - a continued answer
// (max_tokens) is two of them.
func TestClaudeCodeAddsUpTheMessagesOfATurn(t *testing.T) {
	script := lines(
		evStart, assistantText("part one "),
		usageDelta("max_tokens", `{"input_tokens":2,"output_tokens":100,"cache_read_input_tokens":3000}`), evStop,
		evStart, assistantText("part two"),
		usageDelta("end_turn", `{"input_tokens":1,"output_tokens":20,"cache_creation_input_tokens":40}`), evStop,
		"sleep")
	c, _ := claudeTestClient(t, script, nil)
	ctx, m := metered()
	reply, err := c.CompleteTools(ctx, hi, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{Input: 3, Output: 120, CacheRead: 3000, CacheWrite: 40}
	if reply.Usage != want || m.Usage() != want || m.Calls() != 1 {
		t.Errorf("reply %+v meter %+v over %d calls", reply.Usage, m.Usage(), m.Calls())
	}
}

// TestClaudeCodeTakesTheTurnTotalsFromItsResult: when the turn ends with its result, the result's
// usage is the turn's, replacing the sum of what the messages reported.
func TestClaudeCodeTakesTheTurnTotalsFromItsResult(t *testing.T) {
	script := lines(
		evStart, usageDelta("tool_use", `{"input_tokens":1,"output_tokens":1}`), evStop,
		`{"type":"result","subtype":"success","result":"{\"a\":1}","structured_output":{"a":1},`+
			`"usage":{"input_tokens":7,"output_tokens":9,"cache_read_input_tokens":11}}`)
	c, _ := claudeTestClient(t, script, nil)
	ctx, m := metered()
	text, err := c.Complete(ctx, hi)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Usage{Input: 7, Output: 9, CacheRead: 11}); m.Usage() != want || !strings.Contains(text, `"a"`) {
		t.Errorf("text %q meter %+v, want %+v", text, m.Usage(), want)
	}
}

// TestAFailedClaudeTurnRecordsNothing: an error is not a reply, and there is no total to record.
func TestAFailedClaudeTurnRecordsNothing(t *testing.T) {
	c, _ := claudeTestClient(t, lines(`{"type":"result","subtype":"error_during_execution","is_error":true,"usage":{"input_tokens":5}}`), nil)
	ctx, m := metered()
	if _, err := c.CompleteTools(ctx, hi, nil); err == nil {
		t.Fatal("a failed turn must fail")
	}
	if m.Calls() != 0 {
		t.Errorf("calls = %d, want 0", m.Calls())
	}
}

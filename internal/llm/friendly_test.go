package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/i18n"
)

// A provider's failure reads as one sentence chosen from the status and the provider's own error
// fields, and the JSON it sent never reaches the reader.
func TestFriendlyByStatusAndProviderFields(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want string
	}{
		{"openai bad key", 401, `{"error":{"message":"Incorrect API key provided: sk-***","type":"invalid_request_error",` +
			`"code":"invalid_api_key"}}`, msgBadKey},
		{"openai quota", 429, `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota",` +
			`"code":"insufficient_quota"}}`, msgQuota},
		{"openai rate limit", 429, `{"error":{"message":"Rate limit reached","type":"requests",` +
			`"code":"rate_limit_exceeded"}}`, msgRateLimit},
		{"openai no model", 404, `{"error":{"message":"The model 'x' does not exist","type":"invalid_request_error",` +
			`"code":"model_not_found"}}`, msgNoModel},
		{"openai too long", 400, `{"error":{"message":"maximum context length is 8192",` +
			`"code":"context_length_exceeded"}}`, msgTooLong},
		{"anthropic auth", 401, `{"type":"error","error":{"type":"authentication_error",` +
			`"message":"invalid x-api-key"}}`, msgBadKey},
		{"anthropic overloaded", 529, `{"type":"error","error":{"type":"overloaded_error",` +
			`"message":"Overloaded"}}`, msgOverloaded},
		{"anthropic not found", 404, `{"type":"error","error":{"type":"not_found_error","message":"model: x"}}`, msgNoModel},
		{"anthropic billing", 400, `{"type":"error","error":{"type":"invalid_request_error",` +
			`"message":"Your credit balance is too low"}}`, msgQuota},
		{"gemini bad key", 400, `{"error":{"code":400,"message":"API key not valid.",` +
			`"status":"INVALID_ARGUMENT"}}`, msgBadKey},
		{"gemini quota", 429, `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`, msgQuota},
		{"gemini list form", 403, `[{"error":{"code":403,"status":"PERMISSION_DENIED","message":"nope"}}]`, msgNoAccess},
		{"plain 402", 402, ``, msgQuota},
		{"gateway timeout", 504, `<html>504</html>`, msgTimeout},
		{"bad gateway", 502, `<html>bad gateway</html>`, msgServer},
		{"unknown 4xx", 418, `{"weird":true}`, msgUnknown},
	}
	for _, tc := range cases {
		if got := Friendly(tc.code, tc.body); got != tc.want {
			t.Errorf("%s: Friendly = %q, want %q", tc.name, got, tc.want)
		}
		text := (&HTTPError{Code: tc.code, Body: tc.body}).Error()
		if !strings.Contains(text, fmt.Sprintf("(HTTP %d)", tc.code)) || strings.ContainsAny(text, "{}") ||
			strings.Contains(text, "<html>") {
			t.Errorf("%s: the message must carry the code and no raw body: %q", tc.name, text)
		}
	}
}

// Where the sentence alone is not enough, the provider's plain reason follows it; the technical
// detail stays available for the logs.
func TestHTTPErrorKeepsPlainReasonAndDetail(t *testing.T) {
	e := &HTTPError{Code: 400, Body: `{"error":{"message":"max_tokens is too large:\n 99999"}}`}
	if got := e.Error(); got != "The provider could not accept the request: max_tokens is too large: 99999 (HTTP 400)" {
		t.Errorf("Error = %q", got)
	}
	if got := Detail(e); !strings.Contains(got, `"max_tokens`) || !strings.HasPrefix(got, "HTTP 400") {
		t.Errorf("Detail = %q", got)
	}
	long := &HTTPError{Code: 400, Body: strings.Repeat("é", 500)}
	if n := len([]rune(long.Error())); n > 250 {
		t.Errorf("a long reason must be cut, got %d runes", n)
	}
	if Detail(nil) != "" || Detail(errors.New("x")) != "x" {
		t.Error("Detail of nil and of a plain error")
	}
}

// The failures every user meets go through a real client: the key rejected, the server down and a
// connection that never opens.
func TestCompleteReportsReadableErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Incorrect API key provided","code":"invalid_api_key"}}`)
	}))
	c := newTestClient(t, srv.URL)
	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), msgBadKey) || strings.Contains(err.Error(), "{") {
		t.Errorf("a rejected key: %v", err)
	}
	srv.Close() // now nothing listens: a connection error

	c = newTestClient(t, srv.URL)
	_, err = c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), msgUnreachable) || strings.Contains(err.Error(), "dial tcp") {
		t.Errorf("an unreachable server: %v", err)
	}
	if !strings.Contains(Detail(err), "dial tcp") {
		t.Errorf("the transport's text belongs in the detail: %q", Detail(err))
	}
}

// A cancelled call is not a network failure and keeps its own identity.
func TestExplainLeavesCancellationAlone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	_, err := c.Complete(ctx, []Message{{Role: "user", Content: "hi"}})
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), msgUnreachable) {
		t.Errorf("err = %v", err)
	}
	if Explain(nil) != nil {
		t.Error("Explain(nil)")
	}
	plain := errors.New("plain")
	if Explain(plain) != plain {
		t.Error("an unrelated error must pass through")
	}
}

// Localize swaps the sentences inside a longer message, and every sentence has a Spanish entry.
func TestLocalizeTranslatesEverySentence(t *testing.T) {
	for _, en := range friendlyMessages {
		if es := Localize(i18n.ES, "error: "+en+" (HTTP 1)"); strings.Contains(es, en) {
			t.Errorf("%q has no Spanish entry", en)
		}
	}
	in := "error: " + msgBadKey + " (HTTP 401)"
	if got := Localize(i18n.EN, in); got != in {
		t.Errorf("English must be unchanged: %q", got)
	}
	if got := Localize(i18n.ES, ""); got != "" {
		t.Errorf("empty: %q", got)
	}
	if got := Localize(i18n.ES, in); !strings.Contains(got, "(HTTP 401)") || strings.Contains(got, "rejected") {
		t.Errorf("Spanish: %q", got)
	}
}

// Odd bodies never panic and never leak: a bare string, an empty list, a null code, a wrongly
// typed field, and the statuses that only the code can classify.
func TestFriendlyOddBodiesAndBareStatuses(t *testing.T) {
	cases := []struct {
		code int
		body string
		want string
	}{
		{400, `{"error":"insufficient_quota"}`, msgQuota},
		{500, `[]`, msgServer},
		{400, `[1]`, msgBadRequest},
		{500, `{"error":{"code":null,"message":"boom"}}`, msgServer},
		{400, `{"error":{"type":5}}`, msgBadRequest},
		{404, ``, msgNoModel},
		{413, ``, msgTooLong},
		{503, ``, msgOverloaded},
		{408, ``, msgTimeout},
		{429, ``, msgRateLimit},
		{422, ``, msgBadRequest},
		{302, ``, msgUnknown},
	}
	for _, tc := range cases {
		if got := Friendly(tc.code, tc.body); got != tc.want {
			t.Errorf("Friendly(%d, %q) = %q, want %q", tc.code, tc.body, got, tc.want)
		}
	}
	if got := parseProviderError(`{"error":{"code":null,"message":"boom"}}`); got.Code != "" || got.Message != "boom" {
		t.Errorf("a null code must read as none: %+v", got)
	}
	he := &HTTPError{Code: 401}
	if Explain(he) != error(he) {
		t.Error("an HTTP failure passes through Explain untouched")
	}
}

// A timeout is named as such.
func TestExplainNamesTimeouts(t *testing.T) {
	err := Explain(&net.OpError{Op: "read", Err: timeoutErr{}})
	if !strings.Contains(err.Error(), msgTimeout) {
		t.Errorf("err = %v", err)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

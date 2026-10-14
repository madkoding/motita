package oauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const wafPage = `<!DOCTYPE html><html><head><title>Access denied</title>
<style>body{color:red}</style><script>var x = "<b>";</script></head>
<body><h1>Sorry</h1><p>Your request was blocked &amp; logged.</p></body></html>`

// TestQwenLoginAnsweredWithAPageExplainsWhy: qwen.ai answers its retired OAuth
// endpoints with a web page. The login used to fail with "could not decode
// response: invalid character '<'"; it now says why and quotes the page.
func TestQwenLoginAnsweredWithAPageExplainsWhy(t *testing.T) {
	var requestID string
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2/device/code": func(r *http.Request) (*http.Response, error) {
			requestID = r.Header.Get("x-request-id")
			resp := errResp(200, wafPage)
			resp.Header.Set("Content-Type", "text/html; charset=utf-8")
			return resp, nil
		},
	})
	p, _ := NewPKCE()
	_, err := QwenRequestDeviceCode(context.Background(), client, p)
	if !errors.Is(err, ErrQwenLoginUnavailable) || !errors.Is(err, ErrNotJSON) {
		t.Fatalf("err = %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"HTTP 200", "text/html", "Access denied", "Sorry Your request was blocked & logged."} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	for _, unwanted := range []string{"invalid character", "<", "color:red", "var x"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("error %q still has %q", msg, unwanted)
		}
	}
	if len(requestID) != 36 || strings.Count(requestID, "-") != 4 {
		t.Errorf("x-request-id = %q", requestID)
	}
}

// TestNonJSONErrorKeepsStatusAndAShortExcerpt covers a non-2xx page and a long
// plain-text body: the status is kept and the body is cut to one short line.
func TestNonJSONErrorKeepsStatusAndAShortExcerpt(t *testing.T) {
	long := strings.Repeat("upstream unavailable ", 100)
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"page": func(*http.Request) (*http.Response, error) { return errResp(502, wafPage), nil },
		"text": func(*http.Request) (*http.Response, error) { return errResp(503, long), nil },
	})
	var dest map[string]any
	err := getJSON(context.Background(), client, "https://example.test/page", nil, &dest)
	var nj *NotJSONError
	if !errors.As(err, &nj) || nj.Status != 502 || !strings.HasPrefix(nj.Excerpt, "Access denied") {
		t.Fatalf("err = %v", err)
	}
	err = getJSON(context.Background(), client, "https://example.test/text", nil, &dest)
	if !errors.As(err, &nj) || nj.Status != 503 || len([]rune(nj.Excerpt)) != excerptLen+1 {
		t.Fatalf("err = %v (excerpt %d runes)", err, len([]rune(nj.Excerpt)))
	}
}

// TestQwenOtherErrorsAreNotBlamedOnTheRetiredLogin: a JSON error is not a page.
func TestQwenOtherErrorsAreNotBlamedOnTheRetiredLogin(t *testing.T) {
	client := newFakeClient(map[string]func(*http.Request) (*http.Response, error){
		"oauth2/device/code": func(*http.Request) (*http.Response, error) {
			return errResp(500, `{"error":"server_error"}`), nil
		},
	})
	p, _ := NewPKCE()
	_, err := QwenRequestDeviceCode(context.Background(), client, p)
	if err == nil || errors.Is(err, ErrQwenLoginUnavailable) || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v", err)
	}
}

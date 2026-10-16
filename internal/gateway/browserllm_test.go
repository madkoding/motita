package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/browserllm"
	"github.com/madkoding/motita/internal/llm"
)

// A tab asks for work, gets the job, and answers it: the agent's side gets the text.
func TestBrowserLLMRoundTrip(t *testing.T) {
	b := browserllm.New()
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.BrowserLLM = b })

	asked := make(chan string, 1)
	go func() {
		// Wait until the tab's poll has marked it as there.
		for !b.Online() {
			time.Sleep(time.Millisecond)
		}
		text, err := b.Ask(context.Background(), []llm.Message{{Role: "user", Content: "title?"}})
		if err != nil {
			text = err.Error()
		}
		asked <- text
	}()

	var w *httptest.ResponseRecorder
	for {
		w = get(t, srv, "/v1/browser-llm/next", testToken)
		if w.Code == http.StatusOK {
			break
		}
		if w.Code != http.StatusNoContent {
			t.Fatalf("next = %d %s", w.Code, w.Body)
		}
	}
	if !strings.Contains(w.Body.String(), `"content":"title?"`) {
		t.Fatalf("job = %s", w.Body)
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatalf("job id: %v %s", err, w.Body)
	}
	jobPath := "/v1/browser-llm/jobs/" + job.ID
	ans := post(t, srv, jobPath, `{"text":"A title"}`, testToken)
	if ans.Code != http.StatusNoContent {
		t.Fatalf("answer = %d %s", ans.Code, ans.Body)
	}
	if got := <-asked; got != "A title" {
		t.Errorf("asked got %q", got)
	}
	if again := post(t, srv, jobPath, `{"text":"x"}`, testToken); again.Code != http.StatusGone {
		t.Errorf("a second answer = %d", again.Code)
	}
	if bad := post(t, srv, jobPath, `{`, testToken); bad.Code != http.StatusBadRequest {
		t.Errorf("a broken body = %d", bad.Code)
	}
}

// With nothing to do the poll ends empty, and a gateway without the model says so.
func TestBrowserLLMWithoutWorkOrModel(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if w := get(t, srv, "/v1/browser-llm/next", testToken); w.Code != http.StatusNotImplemented {
		t.Errorf("next without a model = %d", w.Code)
	}
	if w := post(t, srv, "/v1/browser-llm/jobs/1", `{}`, testToken); w.Code != http.StatusNotImplemented {
		t.Errorf("answer without a model = %d", w.Code)
	}
	if w := get(t, srv, "/v1/browser-llm/next", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("next without a token = %d", w.Code)
	}

	b := browserllm.New()
	srv = newTestServer(t, &fakeService{}, func(o *Options) { o.BrowserLLM = b })
	req := httptest.NewRequest(http.MethodGet, srv.BaseURL()+"/v1/browser-llm/next", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req.WithContext(ctx))
	if w.Code != http.StatusNoContent {
		t.Errorf("a poll that ended = %d", w.Code)
	}
}

// The settings' "Try it" asks the tab for a title and gets the text and how long it took; with no
// tab it is told why, and a gateway without the model refuses.
func TestBrowserLLMTest(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if w := post(t, srv, "/v1/browser-llm/test", ``, testToken); w.Code != http.StatusNotImplemented {
		t.Errorf("without a model = %d", w.Code)
	}
	b := browserllm.New()
	srv = newTestServer(t, &fakeService{}, func(o *Options) { o.BrowserLLM = b })
	if w := post(t, srv, "/v1/browser-llm/test", ``, testToken); w.Code != http.StatusBadGateway ||
		!strings.Contains(w.Body.String(), "no browser tab") {
		t.Errorf("without a tab = %d %s", w.Code, w.Body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if job, ok := b.Next(ctx); ok {
				b.Answer(job.ID, "Fixing the gateway test", "")
			}
		}
	}()
	for !b.Online() {
		time.Sleep(time.Millisecond)
	}
	w := post(t, srv, "/v1/browser-llm/test", ``, testToken)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"text":"Fixing the gateway test"`) {
		t.Errorf("with a tab = %d %s", w.Code, w.Body)
	}
}

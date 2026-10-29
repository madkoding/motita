package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/voice"
)

// The settings' button asks the gateway to look for a speech server on its machine: nothing is
// changed, and the configured base_url is offered to the probe.
func TestDetectSpeechAnswersTheServersFound(t *testing.T) {
	clearSpeechEnv(t)
	old := detectSpeech
	t.Cleanup(func() { detectSpeech = old })
	var extra []string
	detectSpeech = func(_ context.Context, e ...string) []voice.Endpoint {
		extra = e
		return []voice.Endpoint{{Base: "http://127.0.0.1:8880/v1", Models: []string{"kokoro"}}}
	}
	path := networkConfig(t, "voice:\n  tts:\n    base_url: http://localhost:9000/v1\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	w := sendNetwork(t, srv, http.MethodGet, "/v1/voice/tts/detect", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"base_url":"http://127.0.0.1:8880/v1"`) ||
		!strings.Contains(w.Body.String(), `"kokoro"`) {
		t.Fatalf("detect = %d %s", w.Code, w.Body.String())
	}
	if len(extra) != 1 || extra[0] != "http://localhost:9000/v1" {
		t.Errorf("extra = %v", extra)
	}

	// Without a configuration file there is only the common ports to look at.
	bare := newTestServer(t, &fakeService{})
	if w := sendNetwork(t, bare, http.MethodGet, "/v1/voice/tts/detect", ""); w.Code != http.StatusOK || len(extra) != 0 {
		t.Errorf("bare = %d %v", w.Code, extra)
	}
}

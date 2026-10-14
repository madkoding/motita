package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

func decodeVoice(t *testing.T, w *httptest.ResponseRecorder) voiceView {
	t.Helper()
	var v voiceView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

// TestVoiceIsTurnedOnFromTheSettingsWithAConfirmation: turning speech input on is refused without
// confirm, a confirmed one is saved in the file and applied at once, and the key never comes back.
func TestVoiceIsTurnedOnFromTheSettingsWithAConfirmation(t *testing.T) {
	t.Setenv("MOTITA_VOICE_ENABLED", "")
	path := networkConfig(t, "# mine\nllm:\n  provider: gemini\n  model: m\n")
	built := 0
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ConfigPath = path
		o.VoiceFrom = func(config.Config) VoiceService { built++; return &fakeVoice{text: "hola"} }
	})

	v := decodeVoice(t, get(t, srv, "/v1/voice", testToken))
	if v.Enabled || !v.CanSave || v.Saved == nil || v.Saved.Enabled || v.Saved.Endpoint != "" || v.RestartNeeded {
		t.Fatalf("GET = %+v %+v", v, v.Saved)
	}

	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true}`); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "confirm") {
		t.Fatalf("no confirm = %d %s", w.Code, w.Body.String())
	}
	// Gemini has no OpenAI transcription endpoint: the file is left as it was.
	w := sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true,"confirm":true}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "voice.base_url") {
		t.Fatalf("no endpoint = %d %s", w.Code, w.Body.String())
	}
	if body, _ := os.ReadFile(path); strings.Contains(string(body), "voice") {
		t.Fatalf("a refused change must not stay in the file:\n%s", body)
	}

	w = sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true,"confirm":true,`+
		`"base_url":"https://api.groq.com/openai/v1","model":"whisper-large-v3-turbo","api_key":"gsk-secret",`+
		`"wake_phrases":["oye motita"," "]}`)
	v = decodeVoice(t, w)
	if !v.Enabled || v.RestartNeeded || built != 1 || len(v.WakePhrases) != 1 || v.WakePhrases[0] != "oye motita" ||
		v.Saved.Endpoint != "https://api.groq.com/openai/v1" || !v.Saved.HasKey || v.Saved.Inherits {
		t.Fatalf("PUT = %+v %+v", v, v.Saved)
	}
	if strings.Contains(w.Body.String(), "gsk-secret") {
		t.Fatal("the key must never be answered")
	}
	body, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(body), "# mine\n") || strings.Contains(string(body), "gsk-secret") {
		t.Fatalf("the file keeps its comments and never the key:\n%s", body)
	}
	creds, _ := os.ReadFile(config.CredentialsPath(path))
	if !strings.Contains(string(creds), "gsk-secret") {
		t.Fatalf("the key is kept beside the file: %q", creds)
	}
	if w := postAudio(t, srv, "/v1/voice/transcribe", "audio/webm", strings.NewReader("a")); w.Code != http.StatusOK {
		t.Fatalf("applied without a restart: %d %s", w.Code, w.Body.String())
	}

	// Changing the phrases of a voice that is on needs no question; pointing it elsewhere does.
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true,"wake_phrases":["motita"]}`); w.Code != http.StatusOK {
		t.Fatalf("phrases = %d %s", w.Code, w.Body.String())
	}
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true,"base_url":"http://127.0.0.1:8080/v1"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a new endpoint needs confirm: %d", w.Code)
	}

	// Off needs no confirmation, and the transcription endpoint says so at once.
	v = decodeVoice(t, sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":false}`))
	if v.Enabled || v.Saved.Enabled {
		t.Fatalf("off = %+v", v)
	}
	if w := postAudio(t, srv, "/v1/voice/transcribe", "audio/webm", strings.NewReader("a")); w.Code != http.StatusNotImplemented {
		t.Fatalf("off = %d", w.Code)
	}
}

// TestVoiceChangeWithoutABuilderWaitsForARestart: a gateway that cannot build a transcriber saves
// the change and says it applies at the next start.
func TestVoiceChangeWithoutABuilderWaitsForARestart(t *testing.T) {
	t.Setenv("MOTITA_VOICE_ENABLED", "")
	path := networkConfig(t, "llm:\n  provider: openai\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	v := decodeVoice(t, sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true,"confirm":true}`))
	if v.Enabled || !v.Saved.Enabled || !v.RestartNeeded || !v.Saved.Inherits ||
		v.Saved.Endpoint != "https://api.openai.com/v1" {
		t.Fatalf("PUT = %+v %+v", v, v.Saved)
	}
}

func TestVoiceSettingsRefusals(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":false}`); w.Code != http.StatusConflict {
		t.Errorf("no file = %d", w.Code)
	}
	path := networkConfig(t, "llm:\n  provider: openai\n")
	srv = newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{not json`); w.Code != http.StatusBadRequest {
		t.Errorf("bad body = %d", w.Code)
	}
	old := setVoice
	setVoice = func(string, config.VoiceChange) (config.Config, error) {
		return config.Config{}, errors.New("disk full")
	}
	t.Cleanup(func() { setVoice = old })
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":false}`); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "disk full") {
		t.Errorf("write = %d %s", w.Code, w.Body.String())
	}
}

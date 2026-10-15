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
		o.VoiceFrom = func(config.Config) VoiceService {
			built++
			return &fakeVoice{text: "hola"}
		}
	})

	v := decodeVoice(t, get(t, srv, "/v1/voice", testToken))
	if !voiceNeverTurnedOn(v) {
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
	if !voiceOnWithItsOwnEndpoint(v, built) {
		t.Fatalf("PUT = %+v %+v", v, v.Saved)
	}
	if strings.Contains(w.Body.String(), "gsk-secret") {
		t.Fatal("the key must never be answered")
	}
	assertVoiceKeyKeptBesideTheFile(t, path)
	if w := postAudio(t, srv, "/v1/voice/transcribe", "audio/webm", strings.NewReader("a")); w.Code != http.StatusOK {
		t.Fatalf("applied without a restart: %d %s", w.Code, w.Body.String())
	}

	// Changing the phrases of a voice that is on needs no question; pointing it elsewhere does.
	w = sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true,"wake_phrases":["motita"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("phrases = %d %s", w.Code, w.Body.String())
	}
	w = sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true,"base_url":"http://127.0.0.1:8080/v1"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a new endpoint needs confirm: %d", w.Code)
	}

	// Off needs no confirmation, and the transcription endpoint says so at once.
	v = decodeVoice(t, sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":false}`))
	if v.Enabled || v.Saved.Enabled {
		t.Fatalf("off = %+v", v)
	}
	w = postAudio(t, srv, "/v1/voice/transcribe", "audio/webm", strings.NewReader("a"))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("off = %d", w.Code)
	}
}

// voiceNeverTurnedOn reports the view of a voice the file has never turned on.
func voiceNeverTurnedOn(v voiceView) bool {
	saved := v.Saved != nil && !v.Saved.Enabled && v.Saved.Endpoint == ""
	return !v.Enabled && v.CanSave && saved && !v.RestartNeeded
}

// voiceOnWithItsOwnEndpoint reports the view of the voice the test turns on, at once, with its own
// endpoint, its key and one wake phrase.
func voiceOnWithItsOwnEndpoint(v voiceView, built int) bool {
	applied := v.Enabled && !v.RestartNeeded && built == 1
	phrases := len(v.WakePhrases) == 1 && v.WakePhrases[0] == "oye motita"
	saved := v.Saved.Endpoint == "https://api.groq.com/openai/v1" && v.Saved.HasKey && !v.Saved.Inherits
	return applied && phrases && saved
}

// assertVoiceKeyKeptBesideTheFile checks that the configuration file keeps its comments and never
// the key, which is saved in the credentials beside it.
func assertVoiceKeyKeptBesideTheFile(t *testing.T, path string) {
	t.Helper()
	body, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(body), "# mine\n") || strings.Contains(string(body), "gsk-secret") {
		t.Fatalf("the file keeps its comments and never the key:\n%s", body)
	}
	creds, _ := os.ReadFile(config.CredentialsPath(path))
	if !strings.Contains(string(creds), "gsk-secret") {
		t.Fatalf("the key is kept beside the file: %q", creds)
	}
}

// TestVoiceChangeWithoutABuilderWaitsForARestart: a gateway that cannot build a transcriber saves
// the change and says it applies at the next start.
func TestVoiceChangeWithoutABuilderWaitsForARestart(t *testing.T) {
	t.Setenv("MOTITA_VOICE_ENABLED", "")
	path := networkConfig(t, "llm:\n  provider: openai\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	v := decodeVoice(t, sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true,"confirm":true}`))
	saved := v.Saved.Enabled && v.Saved.Inherits && v.Saved.Endpoint == "https://api.openai.com/v1"
	if v.Enabled || !v.RestartNeeded || !saved {
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
	// A key may not travel in clear text to a remote host, the rule llm.base_url follows.
	t.Setenv("MOTITA_VOICE_ENABLED", "")
	t.Setenv("MOTITA_VOICE_API_KEY", "")
	w := sendNetwork(t, srv, http.MethodPut, "/v1/voice", `{"enabled":true,"confirm":true,`+
		`"base_url":"http://stt.example.com/v1","api_key":"k"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "plain http") {
		t.Errorf("clear text = %d %s", w.Code, w.Body.String())
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

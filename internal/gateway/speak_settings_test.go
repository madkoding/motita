package gateway

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

func clearSpeechEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"MOTITA_VOICE_TTS_ENABLED", "MOTITA_VOICE_TTS_API_KEY", "MOTITA_VOICE_TTS_API", "DASHSCOPE_API_KEY"} {
		t.Setenv(name, "")
	}
}

// The voice is turned on from the settings with a confirmation, saved in the file with the key
// beside it, applied at once, and the key never comes back.
func TestTheVoiceIsTurnedOnFromTheSettingsWithAConfirmation(t *testing.T) {
	clearSpeechEnv(t)
	path := networkConfig(t, "# mine\nllm:\n  provider: openai\n")
	built := 0
	sp := &fakeSpeech{}
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ConfigPath = path
		o.SpeechFrom = func(config.Config) SpeechService {
			built++
			return sp
		}
	})
	const on = `{"enabled":true,"api":"dashscope","api_key":"dk-secret","voice":"Cherry","model":"qwen3-tts-flash"`

	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", on+`}`); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "confirm") {
		t.Fatalf("no confirm = %d %s", w.Code, w.Body.String())
	}
	w := sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", on+`,"confirm":true}`)
	v := decodeVoice(t, w)
	saved := v.Saved.TTS
	if !v.TTS.Enabled || built != 1 || v.RestartNeeded || saved.API != "dashscope" || !saved.HasKey ||
		saved.Voice != "Cherry" || !strings.Contains(saved.Endpoint, "dashscope") {
		t.Fatalf("PUT = %+v %+v", v, saved)
	}
	if strings.Contains(w.Body.String(), "dk-secret") {
		t.Fatal("the key must never be answered")
	}
	body, _ := os.ReadFile(path)
	creds, _ := os.ReadFile(config.CredentialsPath(path))
	if !strings.HasPrefix(string(body), "# mine\n") || strings.Contains(string(body), "dk-secret") ||
		!strings.Contains(string(creds), "dk-secret") {
		t.Fatalf("the key is kept beside the file:\n%s\n%s", body, creds)
	}
	if got := post(t, srv, "/v1/voice/speak", `{"text":"Hola."}`, testToken); got.Code != http.StatusOK || sp.voice != "Cherry" {
		t.Fatalf("applied without a restart: %d %s voice=%q", got.Code, got.Body.String(), sp.voice)
	}

	// Another model of a voice that is on needs no question; another protocol or address does.
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", `{"enabled":true,"model":"qwen3-tts-flash"}`); w.Code != http.StatusOK {
		t.Fatalf("same endpoint = %d %s", w.Code, w.Body.String())
	}
	for _, change := range []string{`"api":"openai"`, `"base_url":"https://dashscope.aliyuncs.com/api/v1"`} {
		if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", `{"enabled":true,`+change+`}`); w.Code != http.StatusBadRequest {
			t.Fatalf("%s needs confirm: %d", change, w.Code)
		}
	}

	// Off needs no confirmation, and the speech endpoint says so at once.
	v = decodeVoice(t, sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", `{"enabled":false}`))
	if v.TTS.Enabled || v.Saved.TTS.Enabled {
		t.Fatalf("off = %+v", v)
	}
	if got := post(t, srv, "/v1/voice/speak", `{"text":"Hola."}`, testToken); got.Code != http.StatusNotImplemented {
		t.Fatalf("off = %d", got.Code)
	}
}

// A gateway that cannot build a voice saves the change and says it applies at the next start.
func TestTheVoiceChangeWithoutABuilderWaitsForARestart(t *testing.T) {
	clearSpeechEnv(t)
	path := networkConfig(t, "llm:\n  provider: openai\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	v := decodeVoice(t, sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", `{"enabled":true,"confirm":true}`))
	if v.TTS.Enabled || !v.RestartNeeded || !v.Saved.TTS.Enabled {
		t.Fatalf("PUT = %+v %+v", v, v.Saved.TTS)
	}
}

func TestTheVoiceSettingsRefusals(t *testing.T) {
	clearSpeechEnv(t)
	srv := newTestServer(t, &fakeService{})
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", `{"enabled":false}`); w.Code != http.StatusConflict {
		t.Errorf("no file = %d", w.Code)
	}
	path := networkConfig(t, "llm:\n  provider: openai\n")
	srv = newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", `{not json`); w.Code != http.StatusBadRequest {
		t.Errorf("bad body = %d", w.Code)
	}
	w := sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", `{"enabled":true,"confirm":true,`+
		`"base_url":"http://tts.example.com/v1","api_key":"k"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "plain http") {
		t.Errorf("clear text = %d %s", w.Code, w.Body.String())
	}
	old := setTTS
	setTTS = func(string, config.TTSChange) (config.Config, error) { return config.Config{}, errors.New("disk full") }
	t.Cleanup(func() { setTTS = old })
	if w := sendNetwork(t, srv, http.MethodPut, "/v1/voice/tts", `{"enabled":false}`); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "disk full") {
		t.Errorf("write = %d %s", w.Code, w.Body.String())
	}
}

// A file that no longer reads asks for the confirmation, whatever the request changes.
func TestTheVoiceAsksAgainWhenTheFileDoesNotRead(t *testing.T) {
	clearSpeechEnv(t)
	path := networkConfig(t, "llm:\n  provider: openai\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.ConfigPath = path
		o.Speech = &fakeSpeech{}
	})
	if err := os.WriteFile(path, []byte("llm: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !srv.ttsNeedsConfirm(nil, nil) {
		t.Error("an unreadable file must ask")
	}
}

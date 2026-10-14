package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// fakeSpeech answers fixed audio, or a fixed error, and records what it was asked to say.
type fakeSpeech struct {
	err         error
	text, voice string
}

func (f *fakeSpeech) Speak(_ context.Context, text, voice string) ([]byte, string, error) {
	f.text, f.voice = text, voice
	if f.err != nil {
		return nil, "", f.err
	}
	return []byte("MP3"), "audio/mpeg", nil
}

func speechServer(t *testing.T, sp *fakeSpeech, limit int) *Server {
	return newTestServer(t, &fakeService{}, func(o *Options) {
		o.Speech = sp
		o.SpeechVoices = []string{"alloy", "nova"}
		o.SpeechMaxChars = limit
	})
}

// The text is said with the voice asked for, or the default, and the audio comes back as is,
// never cached.
func TestSpeakAnswersTheAudio(t *testing.T) {
	sp := &fakeSpeech{}
	srv := speechServer(t, sp, 0)
	w := post(t, srv, "/v1/voice/speak", `{"text":"  Hola. ","voice":"nova"}`, testToken)
	if w.Code != http.StatusOK || w.Body.String() != "MP3" || w.Header().Get("Content-Type") != "audio/mpeg" ||
		w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("answer: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if sp.text != "Hola." || sp.voice != "nova" {
		t.Errorf("said %q with %q", sp.text, sp.voice)
	}
	post(t, srv, "/v1/voice/speak", `{"text":"x"}`, testToken)
	if sp.voice != "alloy" {
		t.Errorf("no voice means the default: %q", sp.voice)
	}

	g := get(t, srv, "/v1/voice", testToken)
	var v voiceView
	_ = json.Unmarshal(g.Body.Bytes(), &v)
	if !v.TTS.Enabled || strings.Join(v.TTS.Voices, ",") != "alloy,nova" || v.TTS.MaxChars != defaultSpeechMaxChars {
		t.Errorf("GET /v1/voice tts = %+v", v.TTS)
	}
}

func TestSpeakRefusesWhatItCannotSay(t *testing.T) {
	sp := &fakeSpeech{}
	srv := speechServer(t, sp, 5)
	for body, want := range map[string]int{
		`nope`:                         http.StatusBadRequest,
		`{"text":"  "}`:                http.StatusBadRequest,
		`{"text":"señores"}`:           http.StatusRequestEntityTooLarge,
		`{"text":"año","voice":"bob"}`: http.StatusBadRequest,
	} {
		if w := post(t, srv, "/v1/voice/speak", body, testToken); w.Code != want {
			t.Errorf("%s: %d, want %d: %s", body, w.Code, want, w.Body.String())
		}
	}
	// Five letters, whatever their bytes, are within a cap of five.
	if w := post(t, srv, "/v1/voice/speak", `{"text":"ñañañ"}`, testToken); w.Code != http.StatusOK {
		t.Errorf("the cap counts characters: %d", w.Code)
	}
	sp.err = errors.New("quota exceeded")
	if w := post(t, srv, "/v1/voice/speak", `{"text":"x"}`, testToken); w.Code != http.StatusBadGateway ||
		!strings.Contains(w.Body.String(), "quota exceeded") {
		t.Errorf("a service error: %d %s", w.Code, w.Body.String())
	}
}

// Without a voice configured, the endpoint says so and the view says it is off.
func TestSpeakIsOffWithoutAService(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.SpeechVoices = []string{"alloy"} })
	if w := post(t, srv, "/v1/voice/speak", `{"text":"x"}`, testToken); w.Code != http.StatusNotImplemented {
		t.Errorf("status %d, want 501", w.Code)
	}
	if v := srv.ttsViewOf(); v.Enabled || len(v.Voices) != 0 || v.MaxChars != defaultSpeechMaxChars {
		t.Errorf("view = %+v", v)
	}
	// A service with no voice listed lets the service pick its own.
	sp := &fakeSpeech{}
	bare := newTestServer(t, &fakeService{}, func(o *Options) { o.Speech = sp })
	if w := post(t, bare, "/v1/voice/speak", `{"text":"x"}`, testToken); w.Code != http.StatusOK || sp.voice != "" {
		t.Errorf("no voices: %d %q", w.Code, sp.voice)
	}
}

// The page may play the audio it fetched: blob: media is allowed, and nothing else new.
func TestPagePolicyPlaysBlobAudio(t *testing.T) {
	if !strings.Contains(pageCSP, "media-src 'self' blob:;") {
		t.Errorf("the page policy has no media-src: %s", pageCSP)
	}
}

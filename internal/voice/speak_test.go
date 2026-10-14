package voice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The request is the one the OpenAI speech API documents, and the audio comes back with its type.
func TestSpeakSendsTheOpenAIRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers = %v", r.Header)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		want := map[string]string{"model": "gpt-4o-mini-tts", "input": "Hello.", "voice": "nova", "response_format": "mp3"}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("%s = %q, want %q", k, body[k], v)
			}
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("MP3"))
	}))
	defer srv.Close()
	s := Speaker{BaseURL: srv.URL + "/v1/", APIKey: "k", Model: "gpt-4o-mini-tts", Format: "mp3", Client: srv.Client()}
	audio, ctype, err := s.Speak(context.Background(), "  Hello. ", "nova")
	if err != nil || string(audio) != "MP3" || ctype != "audio/mpeg" {
		t.Fatalf("got %q %q %v", audio, ctype, err)
	}
}

// A server that types its audio as bytes gets the type of the format asked for.
func TestSpeakNamesTheTypeAServerLeftOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("no key, no authorization")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("RIFF"))
	}))
	defer srv.Close()
	for format, want := range map[string]string{"wav": "audio/wav", "opus": "audio/ogg", "": "audio/mpeg"} {
		_, ctype, err := Speaker{BaseURL: srv.URL, Format: format}.Speak(context.Background(), "x", "v")
		if err != nil || ctype != want {
			t.Errorf("%q: %q %v", format, ctype, err)
		}
	}
}

func TestSpeakReportsWhatWentWrong(t *testing.T) {
	if _, _, err := (Speaker{}).Speak(context.Background(), " ", "v"); !errors.Is(err, ErrNoText) {
		t.Errorf("empty text: %v", err)
	}
	if _, _, err := (Speaker{BaseURL: "http://a b\x7f"}).Speak(context.Background(), "x", "v"); err == nil ||
		!strings.Contains(err.Error(), "could not be built") {
		t.Errorf("bad url: %v", err)
	}
	if _, _, err := (Speaker{BaseURL: "http://127.0.0.1:1"}).Speak(context.Background(), "x", "v"); err == nil ||
		!strings.Contains(err.Error(), "could not be reached") {
		t.Errorf("unreachable: %v", err)
	}

	old := maxSpeechAudio
	maxSpeechAudio = 8
	t.Cleanup(func() { maxSpeechAudio = old })
	cases := map[string]struct {
		h    http.HandlerFunc
		want string
	}{
		"refused": {func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(strings.Repeat("e", 600)))
		}, "answered 401: eeee"},
		"empty": {func(w http.ResponseWriter, _ *http.Request) {}, "no audio"},
		"too big": {func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("0123456789"))
		}, "more than"},
		"cut": {func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("abc"))
		}, "could not be read"},
	}
	for name, tc := range cases {
		srv := httptest.NewServer(tc.h)
		_, _, err := Speaker{BaseURL: srv.URL}.Speak(context.Background(), "x", "v")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
		if name == "refused" && !strings.HasSuffix(err.Error(), "...") {
			t.Errorf("a long refusal is cut: %v", err)
		}
	}
}

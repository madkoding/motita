package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Qwen3-TTS answers a document that names the audio, and the audio is a second, unauthenticated GET.
func TestSpeakDashScopeFetchesTheAudioItNames(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case dashScopePath:
			if r.Header.Get("Authorization") != "Bearer k" || r.Method != http.MethodPost {
				t.Errorf("request = %s %v", r.Method, r.Header)
			}
			var body struct {
				Model string            `json:"model"`
				Input map[string]string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Model != "qwen3-tts-flash" || body.Input["text"] != "Hola." || body.Input["voice"] != "Cherry" {
				t.Errorf("body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"output":{"audio":{"url":"` + srv.URL + `/audio.wav"}}}`))
		case "/audio.wav":
			if r.Header.Get("Authorization") != "" {
				t.Error("the signed link gets no key")
			}
			_, _ = w.Write([]byte("RIFF"))
		}
	}))
	defer srv.Close()
	s := Speaker{BaseURL: srv.URL + "/compatible-mode/v1", APIKey: "k", Model: "qwen3-tts-flash", API: "dashscope", Client: srv.Client()}
	audio, ctype, err := s.Speak(context.Background(), "Hola.", "Cherry")
	if err != nil || string(audio) != "RIFF" || ctype != "audio/wav" {
		t.Fatalf("got %q %q %v", audio, ctype, err)
	}
}

func TestSpeakDashScopeReportsWhatWentWrong(t *testing.T) {
	var srv *httptest.Server
	var current string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/audio.wav" {
			switch current {
			case "dead":
				http.Error(w, "gone", http.StatusForbidden)
			case "huge":
				_, _ = w.Write([]byte(strings.Repeat("x", 9)))
			}
			return
		}
		switch current {
		case "refused":
			_, _ = w.Write([]byte(`{"code":"InvalidParameter","message":"bad voice"}`))
		case "silent":
			_, _ = w.Write([]byte(`{}`))
		case "status":
			http.Error(w, "", http.StatusUnauthorized)
		case "statusbody":
			http.Error(w, "no key", http.StatusUnauthorized)
		default:
			_, _ = w.Write([]byte(`{"output":{"audio":{"url":"` + srv.URL + `/audio.wav"}}}`))
		}
	}))
	defer srv.Close()
	old := maxSpeechAudio
	maxSpeechAudio = 8
	defer func() { maxSpeechAudio = old }()
	for mode, want := range map[string]string{
		"refused": "bad voice", "silent": "no audio", "status": "401", "statusbody": "no key",
		"dead": "403", "huge": "more than", "empty": "no audio",
	} {
		t.Run(mode, func(t *testing.T) {
			current = mode
			s := Speaker{BaseURL: srv.URL, API: "dashscope", Client: srv.Client()}
			_, _, err := s.Speak(context.Background(), "x", "v")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %v", mode, err)
			}
		})
	}
}

func TestDashScopeBase(t *testing.T) {
	for in, want := range map[string]string{
		"https://dashscope-intl.aliyuncs.com/compatible-mode/v1": "https://dashscope-intl.aliyuncs.com",
		"https://dashscope-intl.aliyuncs.com/api/v1/":            "https://dashscope-intl.aliyuncs.com",
		"https://dashscope.aliyuncs.com":                         "https://dashscope.aliyuncs.com",
	} {
		if got := dashScopeBase(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

func TestDoRefusesWhatItCannotSend(t *testing.T) {
	s := Speaker{}
	if _, err := s.do(context.Background(), "BAD METHOD", "http://x", nil, "", 1); err == nil {
		t.Error("an invalid method must fail")
	}
	if _, err := s.do(context.Background(), http.MethodGet, "http://127.0.0.1:1", nil, "", 1); err == nil {
		t.Error("an unreachable endpoint must fail")
	}
}

func TestDoReportsAnAnswerCutShort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	if _, err := (Speaker{Client: srv.Client()}).do(context.Background(), http.MethodGet, srv.URL, nil, "", 1<<10); err == nil {
		t.Error("a truncated answer must fail")
	}
}

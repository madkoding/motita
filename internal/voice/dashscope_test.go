package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The request is DashScope's native one, and the wav behind output.audio.url comes back typed.
func TestSpeakDashScopeFetchesTheAudioURL(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/services/aigc/multimodal-generation/generation":
			if r.Header.Get("Authorization") != "Bearer k" {
				t.Errorf("headers = %v", r.Header)
			}
			var body struct {
				Model string            `json:"model"`
				Input map[string]string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Model != "qwen3-tts-flash" || body.Input["text"] != "Hola." || body.Input["voice"] != "Cherry" {
				t.Errorf("body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"code":"","output":{"audio":{"url":"` + srv.URL + `/a.wav"}}}`))
		case "/a.wav":
			if r.Header.Get("Authorization") != "" {
				t.Error("the key must not reach the audio host")
			}
			_, _ = w.Write([]byte("RIFF"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	s := Speaker{
		API: "dashscope", BaseURL: srv.URL + "/api/v1/", APIKey: "k",
		Model: "qwen3-tts-flash", Client: srv.Client(),
	}
	audio, ctype, err := s.Speak(context.Background(), "Hola.", "Cherry")
	if err != nil || string(audio) != "RIFF" || ctype != "audio/wav" {
		t.Fatalf("got %q %q %v", audio, ctype, err)
	}
}

func TestSpeakDashScopeReportsWhatWentWrong(t *testing.T) {
	cases := []struct {
		name, want string
		handler    http.HandlerFunc
	}{
		{"code", "InvalidApiKey", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":"InvalidApiKey","message":"bad key"}`))
		}},
		{"status", "answered 500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }},
		{"status body", "answered 400: nope", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("nope"))
		}},
		{"not json", "not JSON", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }},
		{"no url", "no audio address", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"output":{}}`))
		}},
		{"bad scheme", "no audio address", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"output":{"audio":{"url":"file:///etc/passwd"}}}`))
		}},
	}
	for _, c := range cases {
		srv := httptest.NewServer(c.handler)
		_, _, err := Speaker{API: "dashscope", BaseURL: srv.URL}.Speak(context.Background(), "x", "v")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestSpeakDashScopeAudioFailures(t *testing.T) {
	for name, handler := range map[string]func(w http.ResponseWriter, srvURL string){
		"empty":  func(w http.ResponseWriter, _ string) {},
		"status": func(w http.ResponseWriter, _ string) { w.WriteHeader(403) },
		"cut": func(w http.ResponseWriter, _ string) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("RI"))
		},
		"big": func(w http.ResponseWriter, _ string) { _, _ = w.Write([]byte("0123456789")) },
	} {
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/generation") {
				_, _ = w.Write([]byte(`{"output":{"audio":{"url":"` + srv.URL + `/a.wav"}}}`))
				return
			}
			handler(w, srv.URL)
		}))
		old := maxSpeechAudio
		maxSpeechAudio = 5
		_, _, err := Speaker{API: "dashscope", BaseURL: srv.URL}.Speak(context.Background(), "x", "v")
		maxSpeechAudio = old
		srv.Close()
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	unreachable := Speaker{API: "dashscope", BaseURL: "http://127.0.0.1:1"}
	if _, _, err := unreachable.Speak(context.Background(), "x", "v"); err == nil {
		t.Error("an unreachable endpoint must fail")
	}
}

func TestSpeakDashScopeBadBaseURL(t *testing.T) {
	if _, _, err := (Speaker{API: "dashscope", BaseURL: "http://a b/"}).Speak(context.Background(), "x", "v"); err == nil {
		t.Error("an unusable base must fail")
	}
}

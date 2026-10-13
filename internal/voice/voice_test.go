package voice

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtension(t *testing.T) {
	cases := map[string]string{
		"audio/webm;codecs=opus": "webm",
		"audio/mp4":              "mp4",
		"audio/x-wav":            "wav",
		"video/webm":             "webm",
	}
	for ct, want := range cases {
		if got, ok := Extension(ct); !ok || got != want {
			t.Errorf("Extension(%q) = %q, %v; want %q", ct, got, ok, want)
		}
	}
	for _, ct := range []string{"text/plain", "", ";;"} {
		if _, ok := Extension(ct); ok {
			t.Errorf("Extension(%q) must be refused", ct)
		}
	}
}

// TestTranscribeSendsTheOpenAIForm: the request is the one the OpenAI API documents, and the
// text comes back trimmed.
func TestTranscribeSendsTheOpenAIForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("authorization = %q", got)
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("no file: %v", err)
		}
		data, _ := io.ReadAll(f)
		if string(data) != "AUDIO" || h.Filename != "speech.webm" {
			t.Errorf("file = %q %q", data, h.Filename)
		}
		if r.FormValue("model") != "whisper-1" || r.FormValue("language") != "es" ||
			r.FormValue("response_format") != "json" {
			t.Errorf("form = %v", r.Form)
		}
		_, _ = io.WriteString(w, `{"text":"  hello there "}`)
	}))
	defer srv.Close()
	tr := Transcriber{BaseURL: srv.URL + "/v1/", APIKey: "k", Model: "whisper-1", Language: "es", Client: srv.Client()}
	got, err := tr.Transcribe(context.Background(), []byte("AUDIO"), "audio/webm;codecs=opus")
	if err != nil || got != "hello there" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// TestTranscribeWithoutKeyOrLanguage: a local server needs neither, and neither is sent.
func TestTranscribeWithoutKeyOrLanguage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("no key must be sent when none is configured")
		}
		_ = r.ParseMultipartForm(1 << 20)
		if _, ok := r.MultipartForm.Value["language"]; ok {
			t.Error("an empty language must not be sent")
		}
		_, _ = io.WriteString(w, `{"text":"ok"}`)
	}))
	defer srv.Close()
	// The nil client path: the default client reaches a plain httptest server.
	got, err := Transcriber{BaseURL: srv.URL, Model: "m"}.Transcribe(context.Background(), []byte("x"), "audio/wav")
	if err != nil || got != "ok" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTranscribeFailures(t *testing.T) {
	long := strings.Repeat("x", 2*maxErrorBody)
	cases := []struct {
		name, body, ctype, want string
		status                  int
		audio                   []byte
	}{
		{"empty", "", "audio/webm", "empty", 200, nil},
		{"type", "", "text/plain", "not one", 200, []byte("a")},
		{"refused", `{"error":"bad key"}`, "audio/webm", "answered 401: {\"error\":\"bad key\"}", 401, []byte("a")},
		{"long refusal", long, "audio/webm", "...", 500, []byte("a")},
		{"not json", "<html>", "audio/webm", "not JSON", 200, []byte("a")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			}))
			defer srv.Close()
			_, err := Transcriber{BaseURL: srv.URL, Model: "m"}.Transcribe(context.Background(), c.audio, c.ctype)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
			if c.name == "empty" && !errors.Is(err, ErrEmpty) {
				t.Errorf("an empty recording must be ErrEmpty: %v", err)
			}
		})
	}
}

func TestTranscribeRequestErrors(t *testing.T) {
	bad := Transcriber{BaseURL: "http://bad\x7fhost"}
	if _, err := bad.Transcribe(context.Background(), []byte("a"), "audio/wav"); err == nil ||
		!strings.Contains(err.Error(), "could not be built") {
		t.Errorf("a malformed base URL must be reported: %v", err)
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := (Transcriber{BaseURL: url}).Transcribe(context.Background(), []byte("a"), "audio/wav"); err == nil ||
		!strings.Contains(err.Error(), "could not be reached") {
		t.Errorf("an unreachable endpoint must be reported: %v", err)
	}
}

func TestMatch(t *testing.T) {
	phrases := []string{"hey motita", "motita", "motita new task", "", "ok go"}
	cases := []struct {
		text, phrase, rest string
		ok                 bool
	}{
		{"Hey Motita, run the tests.", "hey motita", "run the tests.", true},
		{"Motita new task: write docs", "motita new task", "write docs", true},
		{"uh, mutita  check the build", "motita", "check the build", true},
		{"MOTÍTA", "motita", "", true}, // spanish-fixture: accented transcript folds to the plain phrase
		{"well so then motita go", "", "", false},
		{"ok go now", "ok go", "now", true},
		{"ok no", "", "", false},
		{"", "", "", false},
		{"motor", "", "", false},
	}
	for _, c := range cases {
		w, ok := Match(c.text, phrases)
		if ok != c.ok || w.Phrase != c.phrase || w.Rest != c.rest {
			t.Errorf("Match(%q) = %+v, %v; want %q %q %v", c.text, w, ok, c.phrase, c.rest, c.ok)
		}
	}
	if _, ok := Match("motita", nil); ok {
		t.Error("no phrases means no match")
	}
}

func TestDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"", "", 0}, {"abc", "", 3}, {"kitten", "sitting", 3}, {"motita", "mutita", 1}}
	for _, c := range cases {
		if got := distance([]rune(c.a), []rune(c.b)); got != c.want {
			t.Errorf("distance(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

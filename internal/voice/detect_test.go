package voice

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// speechServer answers like an OpenAI-compatible server: the route and the listing.
func speechServer(t *testing.T, route http.HandlerFunc) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audio/speech", route)
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"kokoro"},{"id":" "},{"id":"tts-1"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func TestDetectSpeechFindsTheRouteWithoutSendingText(t *testing.T) {
	var methods []string
	base := speechServer(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.ContentLength > 0 {
			t.Error("the probe must carry no body")
		}
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	got := DetectSpeech(context.Background(), base+"/")
	if len(got) != 1 || got[0].Base != base || strings.Join(got[0].Models, ",") != "kokoro,tts-1" {
		t.Fatalf("found = %+v", got)
	}
	if len(methods) != 1 || methods[0] != http.MethodOptions {
		t.Errorf("methods = %v", methods)
	}
}

func TestHasSpeechRouteReadsTheStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		allow  string
		want   bool
	}{
		{"missing", 404, "", false},
		{"server error", 500, "", false},
		{"redirect", 302, "", false},
		{"other methods only", 405, "GET, HEAD", false},
		{"405 allows post", 405, "POST", true},
		{"405 silent", 405, "", true},
		{"needs a key", 401, "", true},
		{"no content", 204, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if c.allow != "" {
					w.Header().Set("Allow", c.allow)
				}
				w.WriteHeader(c.status)
			}))
			defer srv.Close()
			if got := hasSpeechRoute(context.Background(), detectClient(), srv.URL); got != c.want {
				t.Errorf("%d %q = %v", c.status, c.allow, got)
			}
		})
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/audio/speech" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		t.Error("a redirect must not be followed")
	}))
	defer redirect.Close()
	if hasSpeechRoute(context.Background(), detectClient(), redirect.URL) {
		t.Error("a redirect is no route")
	}
	if hasSpeechRoute(context.Background(), http.DefaultClient, "http://127.0.0.1:1") {
		t.Error("a closed port has no route")
	}
	if hasSpeechRoute(context.Background(), http.DefaultClient, "http://bad host") {
		t.Error("an unusable base has no route")
	}
}

func TestListModelsToleratesAnythingElse(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"not found": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) },
		"not json":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
	} {
		srv := httptest.NewServer(h)
		if got := listModels(context.Background(), http.DefaultClient, srv.URL); got == nil || len(got) != 0 {
			t.Errorf("%s = %#v", name, got)
		}
		srv.Close()
	}
	if got := listModels(context.Background(), http.DefaultClient, "http://127.0.0.1:1"); len(got) != 0 {
		t.Errorf("unreachable = %v", got)
	}
	if got := listModels(context.Background(), http.DefaultClient, "http://bad host"); len(got) != 0 {
		t.Errorf("bad base = %v", got)
	}
}

func TestListModelsIsCapped(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"data":[`)
	for i := 0; i < 50; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"id":"m` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `"}`)
	}
	sb.WriteString("]}")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sb.String()))
	}))
	defer srv.Close()
	if got := listModels(context.Background(), http.DefaultClient, srv.URL); len(got) != maxDetectModels {
		t.Errorf("models = %d", len(got))
	}
}

func TestLoopbackBase(t *testing.T) {
	for base, want := range map[string]bool{
		"http://localhost:8000/v1": true, "http://127.0.0.1:8880": true, "http://[::1]:5000/v1": true,
		"https://api.openai.com/v1": false, "http://192.168.1.5:8000": false, "ftp://localhost": false,
		"": false, "http://bad host": false,
	} {
		if got := loopbackBase(base); got != want {
			t.Errorf("%q = %v", base, got)
		}
	}
}

// A remote base_url is never probed, and the common ports are tried on the loopback only.
func TestDetectSpeechProbesTheLoopbackOnly(t *testing.T) {
	old, oldTimeout := DetectPorts, detectTimeout
	t.Cleanup(func() { DetectPorts, detectTimeout = old, oldTimeout })
	detectTimeout = 500 * time.Millisecond
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener")
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(422)
	}))
	srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	defer srv.Close()
	DetectPorts = []int{l.Addr().(*net.TCPAddr).Port}
	got := DetectSpeech(context.Background(), "https://api.openai.com/v1", "")
	if len(got) != 1 || got[0].Base != srv.URL+"/v1" || got[0].Models == nil {
		t.Fatalf("found = %+v", got)
	}
}

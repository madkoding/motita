package xerj

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const answer = `{"hits":{"hits":[
 {"_score":3.5,"_source":{"ax_path":"a/b.go","line":12,"title":"b"},"fields":{"_passage":[{"field":"title","ordinal":0,"text":"b"},{"field":"body","ordinal":4,"text":"  func Rotate() {}  "}]}},
 {"_score":1,"_source":{"ax_path":"c.go","start_line":7},"fields":{"_passage":[{"field":"body","ordinal":0,"text":"x"}]}},
 {"_score":1,"_source":{"ax_path":"d.md"},"fields":{"_passage":[{"field":"body","ordinal":9,"text":"` + "é" + `"}]}},
 {"_score":1,"_source":{},"fields":{}}
]}}`

func serve(t *testing.T, status int, body string, check func(*http.Request, []byte)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf [1 << 14]byte
		n, _ := r.Body.Read(buf[:])
		if check != nil {
			check(r, buf[:n])
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "k3y", "")
}

func TestSearchParsesPassages(t *testing.T) {
	c := serve(t, 200, answer, func(r *http.Request, body []byte) {
		if r.URL.Path != "/ax-*/_search" || r.Header.Get("Authorization") != "ApiKey k3y" {
			t.Errorf("request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var b map[string]any
		if err := json.Unmarshal(body, &b); err != nil || b["size"].(float64) != 20 {
			t.Errorf("body %s (%v)", body, err)
		}
	})
	hits, err := c.Search(context.Background(), " rotate ", 99)
	if err != nil || len(hits) != 4 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	if hits[0].Path != "a/b.go" || hits[0].Line != 12 || hits[0].Passage != "func Rotate() {}" {
		t.Errorf("first hit: %+v", hits[0])
	}
	if hits[1].Line != 7 || hits[2].Line != 10 || hits[3].Passage != "" {
		t.Errorf("lines: %+v", hits)
	}
	out := Format("rotate", hits)
	if !strings.Contains(out, "a/b.go:12\nfunc Rotate() {}") || !strings.Contains(out, "\n---\n") ||
		!strings.Contains(out, "d.md:10") || !strings.Contains(out, "\n?\n") && !strings.HasSuffix(out, "?") {
		t.Errorf("format:\n%s", out)
	}
}

func TestSearchDefaultsAndNoKey(t *testing.T) {
	c := serve(t, 200, `{"hits":{"hits":[]}}`, func(r *http.Request, body []byte) {
		if r.Header.Get("Authorization") != "" || !strings.Contains(string(body), `"size":5`) {
			t.Errorf("auth=%q body=%s", r.Header.Get("Authorization"), body)
		}
	})
	c.APIKey = ""
	hits, err := c.Search(context.Background(), "q", 0)
	if err != nil || len(hits) != 0 {
		t.Fatalf("%v %v", hits, err)
	}
	if got := Format("q", hits); !strings.Contains(got, "No indexed passage") {
		t.Error(got)
	}
}

func TestSearchErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := New("", "", "").Search(ctx, "  ", 1); err == nil {
		t.Error("empty query accepted")
	}
	if c := New("", "", ""); c.URL != DefaultURL || c.Index != DefaultIndex {
		t.Errorf("defaults: %+v", c)
	}
	if _, err := serve(t, 500, strings.Repeat("x", 400), nil).Search(ctx, "q", 1); err == nil ||
		!strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "…") {
		t.Errorf("status error: %v", err)
	}
	if _, err := serve(t, 200, "nope", nil).Search(ctx, "q", 1); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("json error: %v", err)
	}
	if _, err := New("http://127.0.0.1:1", "", "").Search(ctx, "q", 1); err == nil || !strings.Contains(err.Error(), "no XERJ node") {
		t.Errorf("down: %v", err)
	}
	if _, err := New("http://bad host", "", "").Search(ctx, "q", 1); err == nil || !strings.Contains(err.Error(), "not usable") {
		t.Errorf("url: %v", err)
	}
}

func TestSearchBodyRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("abc"))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "", "").Search(context.Background(), "q", 1); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Errorf("truncated body: %v", err)
	}
}

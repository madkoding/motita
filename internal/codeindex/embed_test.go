package codeindex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// topics is the fake embedding model: one axis per topic, a text scores on the axes of the words
// it holds. "log" and "journal" are the same axis, the way a real model would put them close.
var topics = [][]string{{"journal", "log", "segment"}, {"socket", "peer", "handshake"}, {"palette", "border", "glyphs"}}

type fakeEmbedder struct {
	calls int
	texts int
	fail  error
	short bool
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	f.texts += len(texts)
	if f.fail != nil {
		return nil, f.fail
	}
	if f.short {
		return nil, nil
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		v := make([]float32, len(topics))
		for axis, words := range topics {
			for _, w := range words {
				if strings.Contains(strings.ToLower(text), w) {
					v[axis]++
				}
			}
		}
		normalize(v)
		out[i] = v
	}
	return out, nil
}

func neuralSearcher(cache string, f *fakeEmbedder) *Searcher {
	o := Options{CacheDir: cache, EmbedURL: "http://fake", EmbedModel: "m1"}
	s := NewSearcher(o)
	s.embedder = f
	return s
}

func pathsOf(hits []Hit) string {
	var p []string
	for _, h := range hits {
		p = append(p, h.Path)
	}
	return strings.Join(p, ",")
}

func TestEmbeddingsRankByMeaningAndAreKeptOnDisk(t *testing.T) {
	root, cache := semanticProject(t), t.TempDir()
	first := &fakeEmbedder{}
	s := neuralSearcher(cache, first)
	// The query shares no word with ui/*.go or net/*.go; "log" is the journal axis.
	hits, err := s.Search(context.Background(), root, "write the log", 8)
	if err != nil || !strings.Contains(pathsOf(hits), "wal/") || s.Warning() != "" {
		t.Fatalf("hits = %v err = %v warning = %q", pathsOf(hits), err, s.Warning())
	}
	if first.texts < len(s.byAt[root].Chunks) {
		t.Errorf("every window must be embedded once: %d texts for %d windows", first.texts, len(s.byAt[root].Chunks))
	}
	second := &fakeEmbedder{}
	again := neuralSearcher(cache, second)
	if _, err := again.Search(context.Background(), root, "write the log", 3); err != nil {
		t.Fatal(err)
	}
	if second.texts != 1 {
		t.Errorf("a restart must send only the query, sent %d texts", second.texts)
	}
	other := neuralSearcher(cache, &fakeEmbedder{})
	other.opts.EmbedModel = "m2"
	if _, err := other.Search(context.Background(), root, "write the log", 3); err != nil {
		t.Fatal(err)
	}
	if other.byAt[root].EmbedID != "m2@http://fake" {
		t.Errorf("vectors of another model must be rebuilt: %q", other.byAt[root].EmbedID)
	}
}

func TestEmbeddingsFailureIsAWarningNotAnError(t *testing.T) {
	root := semanticProject(t)
	s := neuralSearcher("", &fakeEmbedder{fail: errors.New("boom")})
	hits, err := s.Search(context.Background(), root, "roll", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("the other rankings must still answer: %v %v", hits, err)
	}
	if w := s.Warning(); !strings.Contains(w, "boom") || !strings.Contains(w, "embeddings unavailable") {
		t.Errorf("warning = %q", w)
	}
	short := neuralSearcher("", &fakeEmbedder{short: true})
	_, err = short.Search(context.Background(), root, "roll", 5)
	if err != nil || !strings.Contains(short.Warning(), "wrong number") {
		t.Errorf("a short answer: %v %q", err, short.Warning())
	}
}

func TestQueryEmbeddingFailureAfterTheIndexIsEmbedded(t *testing.T) {
	root := semanticProject(t)
	f := &fakeEmbedder{}
	s := neuralSearcher("", f)
	if _, err := s.Search(context.Background(), root, "roll", 3); err != nil {
		t.Fatal(err)
	}
	f.fail = errors.New("down now")
	_, err := s.Search(context.Background(), root, "roll", 3)
	if err != nil || !strings.Contains(s.Warning(), "down now") {
		t.Errorf("err = %v warning = %q", err, s.Warning())
	}
	f.fail = nil
	_, err = s.Search(context.Background(), root, "roll", 3)
	if err != nil || s.Warning() != "" {
		t.Errorf("a search that works clears the warning: %v %q", err, s.Warning())
	}
	f.short = true
	_, err = s.Search(context.Background(), root, "roll", 3)
	if err != nil || !strings.Contains(s.Warning(), "wrong number") {
		t.Errorf("err = %v warning = %q", err, s.Warning())
	}
}

func TestEmbeddingsAreCappedAndSkippedWithoutAnEndpoint(t *testing.T) {
	old := maxEmbedChunks
	maxEmbedChunks = 3
	defer func() { maxEmbedChunks = old }()
	root := semanticProject(t)
	f := &fakeEmbedder{}
	s := neuralSearcher("", f)
	if _, err := s.Search(context.Background(), root, "roll", 3); err != nil {
		t.Fatal(err)
	}
	if f.texts != 3+1 {
		t.Errorf("only %d windows plus the query must be sent, sent %d", maxEmbedChunks, f.texts)
	}
	plain := NewSearcher(Options{})
	if _, err := plain.Search(context.Background(), root, "roll", 3); err != nil || plain.byAt[root].Emb != nil {
		t.Errorf("no endpoint, no vectors: %v", err)
	}
	if plain.embed(context.Background(), plain.byAt[root]) {
		t.Error("nothing to embed")
	}
}

func TestChunkTextFallsBackToThePathWhenTheFileChanged(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "one\ntwo\n")
	ix, _ := Build(context.Background(), root, Options{})
	lc := &lineCache{root: root, byFile: map[string][]string{}}
	if got := ix.chunkText(lc, &ix.Chunks[0]); !strings.HasPrefix(got, "a.txt\none") {
		t.Errorf("text = %q", got)
	}
	ix.Chunks[0].N = 99
	if got := ix.chunkText(lc, &ix.Chunks[0]); got != "a.txt" {
		t.Errorf("a window that no longer fits sends the path: %q", got)
	}
	ix.Files[0] = "gone.txt"
	if got := ix.chunkText(lc, &ix.Chunks[0]); got != "gone.txt" {
		t.Errorf("a missing file sends the path: %q", got)
	}
}

func TestEmbedChunksReportsAWrongVectorCount(t *testing.T) {
	ix, _ := Build(context.Background(), semanticProject(t), Options{})
	if _, err := embedChunks(context.Background(), ix, &countOff{}); err == nil {
		t.Error("an embedder that answers the wrong number of vectors must fail")
	}
}

type countOff struct{}

func (countOff) Embed(context.Context, []string) ([][]float32, error) { return [][]float32{{1}}, nil }

func embeddingsServer(t *testing.T, status int, body string, check func(*http.Request, map[string]any)) *httpEmbedder {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if check != nil {
			check(r, in)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return newEmbedder(Options{EmbedURL: srv.URL + "/v1/", EmbedModel: "m", EmbedKey: "k3y"}).(*httpEmbedder)
}

func TestHTTPEmbedder(t *testing.T) {
	e := embeddingsServer(t, 200, `{"data":[{"index":1,"embedding":[0,2]},{"index":0,"embedding":[3,0]}]}`,
		func(r *http.Request, in map[string]any) {
			if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer k3y" ||
				in["model"] != "m" || len(in["input"].([]any)) != 2 {
				t.Errorf("request: %s auth=%q body=%v", r.URL.Path, r.Header.Get("Authorization"), in)
			}
		})
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][1] != 1 {
		t.Fatalf("vecs = %v err = %v (index order and normalization)", vecs, err)
	}
}

func TestHTTPEmbedderErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"status", 401, strings.Repeat("x", 400), "HTTP 401"},
		{"json", 200, "nope", "not JSON"},
		{"count", 200, `{"data":[]}`, "0 vectors for 1 texts"},
		{"empty vector", 200, `{"data":[{"index":0,"embedding":[]}]}`, "different or zero length"},
	}
	for _, tc := range cases {
		_, err := embeddingsServer(t, tc.status, tc.body, nil).Embed(ctx, []string{"a"})
		if !errHas(err, tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	_, err := embeddingsServer(t, 200, `{"data":[{"index":0,"embedding":[1]},{"index":1,"embedding":[1,2]}]}`, nil).
		Embed(ctx, []string{"a", "b"})
	if !errHas(err, "different or zero length") {
		t.Errorf("lengths: %v", err)
	}
	down := newEmbedder(Options{EmbedURL: "http://127.0.0.1:1"})
	if _, err := down.Embed(ctx, []string{"a"}); !errHas(err, "did not answer") {
		t.Errorf("down: %v", err)
	}
	bad := newEmbedder(Options{EmbedURL: "http://bad host"})
	if _, err := bad.Embed(ctx, []string{"a"}); !errHas(err, "not usable") {
		t.Errorf("url: %v", err)
	}
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("abc"))
	}))
	defer cut.Close()
	if _, err := newEmbedder(Options{EmbedURL: cut.URL}).Embed(ctx, []string{"a"}); !errHas(err, "reading") {
		t.Errorf("truncated: %v", err)
	}
}

func TestNewEmbedderNeedsAnAddress(t *testing.T) {
	if newEmbedder(Options{EmbedURL: "  "}) != nil || (Options{}).embedID() != "" {
		t.Error("no address, no embedder")
	}
	if got := (Options{EmbedURL: "http://h/v1/", EmbedModel: "m"}).embedID(); got != "m@http://h/v1" {
		t.Errorf("id = %q", got)
	}
}

func errHas(err error, want string) bool { return err != nil && strings.Contains(err.Error(), want) }

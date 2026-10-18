package gateway

import (
	"context"
	"encoding/json"
	"github.com/madkoding/motita/internal/codeindex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decodeCodeIndex(t *testing.T, w *httptest.ResponseRecorder) codeIndexView {
	t.Helper()
	var v codeIndexView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

func TestTheCodeIndexIsReadFromTheFile(t *testing.T) {
	path := networkConfig(t, "llm:\n  provider: openai\ncode_index:\n  cache_dir: "+t.TempDir()+"\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	want := codeIndexView{
		Enabled: true, Semantic: true, EmbeddingsAuto: true, MaxFileKB: 256, MaxFiles: 5000,
		Embeddings: "auto", CanSave: true,
	}
	if v := decodeCodeIndex(t, get(t, srv, "/v1/code-index", testToken)); v != want {
		t.Errorf("default = %+v", v)
	}

	noFile := newTestServer(t, &fakeService{})
	if v := decodeCodeIndex(t, get(t, noFile, "/v1/code-index", testToken)); v.CanSave {
		t.Errorf("no file, nothing to save in: %+v", v)
	}

	broken := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, "llm: [\n") })
	if w := get(t, broken, "/v1/code-index", testToken); w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable file: %d", w.Code)
	}
}

func TestTheCodeIndexIsSavedAndRemoteEmbeddingsNeedConfirmation(t *testing.T) {
	path := networkConfig(t, "llm:\n  provider: openai\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	put := func(body string) *httptest.ResponseRecorder {
		return sendNetwork(t, srv, http.MethodPut, "/v1/code-index", body)
	}

	for name, body := range map[string]string{
		"nothing":        `{}`,
		"only confirm":   `{"confirm":true}`,
		"remote":         `{"embeddings_url":"https://api.openai.com/v1","embeddings_model":"m"}`,
		"not json":       `{`,
		"no model":       `{"embeddings_url":"http://localhost:11434/v1"}`,
		"negative limit": `{"max_files":-1}`,
	} {
		if w := put(body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}

	limits := `{"semantic":false,"embeddings_auto":false,"max_file_kb":64,"max_files":100,"enabled":true}`
	want := codeIndexView{Enabled: true, MaxFileKB: 64, MaxFiles: 100, Embeddings: "off", CanSave: true}
	if v := decodeCodeIndex(t, put(limits)); v != want {
		t.Errorf("limits = %+v", v)
	}
	local := `{"embeddings_url":" http://localhost:11434/v1 ","embeddings_model":" nomic-embed-text "}`
	want.EmbeddingsURL, want.EmbeddingsModel = "http://localhost:11434/v1", "nomic-embed-text"
	want.Embeddings, want.EndpointLocal = "endpoint", true
	if v := decodeCodeIndex(t, put(local)); v != want {
		t.Errorf("local endpoint needs no confirmation: %+v", v)
	}
	remote := `{"embeddings_url":"https://api.openai.com/v1","embeddings_model":"m","confirm":true}`
	want.EmbeddingsURL, want.EmbeddingsModel, want.EndpointLocal = "https://api.openai.com/v1", "m", false
	if v := decodeCodeIndex(t, put(remote)); v != want {
		t.Errorf("remote confirmed: %+v", v)
	}
	if v := decodeCodeIndex(t, put(`{"embeddings_url":""}`)); v.Embeddings != "off" {
		t.Errorf("clearing the endpoint needs no confirmation: %+v", v)
	}

	noFile := newTestServer(t, &fakeService{})
	w := sendNetwork(t, noFile, http.MethodPut, "/v1/code-index", `{"semantic":false}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "configuration file") {
		t.Errorf("no file: %d %s", w.Code, w.Body.String())
	}
}

func TestTheCodeIndexCacheIsCleared(t *testing.T) {
	cache := t.TempDir()
	path := networkConfig(t, "llm:\n  provider: openai\ncode_index:\n  cache_dir: "+cache+"\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	if err := os.WriteFile(filepath.Join(cache, "a.gob"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := del(t, srv, "/v1/code-index/cache", testToken)
	var got codeIndexCleared
	err := json.Unmarshal(w.Body.Bytes(), &got)
	if err != nil || w.Code != http.StatusOK || got != (codeIndexCleared{1, 5}) {
		t.Fatalf("clear = %d %s", w.Code, w.Body.String())
	}
	if w := del(t, srv, "/v1/code-index/cache", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("without a token: %d", w.Code)
	}
	broken := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, "llm: [\n") })
	if w := del(t, broken, "/v1/code-index/cache", testToken); w.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable file: %d", w.Code)
	}
	bad := networkConfig(t, "llm:\n  provider: openai\ncode_index:\n  cache_dir: \"[\"\n")
	badSrv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = bad })
	if w := del(t, badSrv, "/v1/code-index/cache", testToken); w.Code != http.StatusInternalServerError {
		t.Errorf("a cache_dir that cannot be listed: %d", w.Code)
	}
}

// indexedProject is a server whose code index is kept in a temporary folder, with a project that
// has one source file, and the project's index URL.
func indexedProject(t *testing.T) (*Server, string, string) {
	t.Helper()
	cache := t.TempDir()
	path := networkConfig(t, "llm:\n  provider: openai\ncode_index:\n  cache_dir: "+cache+"\n")
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = path })
	withProjects(t, srv, t.TempDir())
	id := makeProject(t, srv, "indexed")
	source := filepath.Join(srv.projectOf(id).Dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n\nfunc Rotate() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return srv, id, "/v1/projects/" + id + "/code-index"
}

func readProjectIndex(t *testing.T, w *httptest.ResponseRecorder) codeindex.Status {
	t.Helper()
	var st codeindex.Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %s: %v", w.Code, w.Body.String(), err)
	}
	return st
}

func TestTheProjectIndexIsReportedAndBuiltOnDemand(t *testing.T) {
	srv, _, url := indexedProject(t)
	st := readProjectIndex(t, get(t, srv, url, testToken))
	if st.State != codeindex.StateNone || st.Candidates == 0 || st.Percent != 0 {
		t.Errorf("before building: %+v", st)
	}
	st = readProjectIndex(t, post(t, srv, url, "", testToken))
	if st.State != codeindex.StateReady || st.Percent != 100 || st.CacheBytes == 0 {
		t.Errorf("after building: %+v", st)
	}
	if v := decodeCodeIndex(t, get(t, srv, "/v1/code-index", testToken)); v.CacheFiles != 1 || v.CacheBytes == 0 {
		t.Errorf("cache size in the settings view: %+v", v)
	}
}

func TestTheProjectIndexRefusesWhatItCannotAnswer(t *testing.T) {
	srv, id, url := indexedProject(t)
	if w := get(t, srv, "/v1/projects/nope/code-index", testToken); w.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d", w.Code)
	}
	if w := post(t, srv, url, "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("without a token: %d", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, url, nil).WithContext(ctx)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		srv.projectIndex(rec, req, method == http.MethodPost)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s with the walk cancelled: %d", method, rec.Code)
		}
	}
	for name, c := range map[string]struct {
		config string
		want   int
	}{
		"turned off": {"llm:\n  provider: openai\ncode_index:\n  enabled: false\n", http.StatusConflict},
		"unreadable": {"llm: [\n", http.StatusInternalServerError},
	} {
		other := newTestServer(t, &fakeService{}, func(o *Options) { o.ConfigPath = networkConfig(t, c.config) })
		other.projects = srv.projects
		if w := get(t, other, url, testToken); w.Code != c.want {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
}

// The same process still holds the index in memory after the cache is cleared; building must write
// it to disk again instead of answering "none" as if nothing happened.
func TestBuildingAgainAfterClearingTheCacheKeepsTheIndexOnDisk(t *testing.T) {
	srv, _, url := indexedProject(t)
	readProjectIndex(t, post(t, srv, url, "", testToken))
	if w := del(t, srv, "/v1/code-index/cache", testToken); w.Code != http.StatusOK {
		t.Fatalf("clearing: %d", w.Code)
	}
	if st := readProjectIndex(t, get(t, srv, url, testToken)); st.State != codeindex.StateNone {
		t.Fatalf("after clearing: %+v", st)
	}
	if st := readProjectIndex(t, post(t, srv, url, "", testToken)); st.State != codeindex.StateReady {
		t.Errorf("after building again: %+v", st)
	}
}

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/notes"
)

func noteServer(t *testing.T, mutators ...func(*Options)) (*Server, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "notes")
	srv := newTestServer(
		t, &fakeService{}, append([]func(*Options){func(o *Options) { o.NotesDir = dir }}, mutators...)...,
	)
	return srv, dir
}

func noteCall(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return sendNetwork(t, srv, method, path, body)
}

func notePath(p string) string { return "/v1/notes/note?path=" + url.QueryEscape(p) }

func TestNotesAreCreatedReadLinkedAndDeleted(t *testing.T) {
	srv, dir := noteServer(t)
	w := noteCall(t, srv, http.MethodGet, "/v1/notes", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"notes":[]`) {
		t.Fatalf("empty vault: %d %s", w.Code, w.Body)
	}
	w = noteCall(t, srv, http.MethodPut, notePath("Japan.md"), `{"body":"# Japan\nSee [[Peru]]","create":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	w = noteCall(t, srv, http.MethodPut, notePath("trips/Peru.md"), `{"body":"Back to [[Japan]]","create":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create in a folder: %d %s", w.Code, w.Body)
	}
	w = noteCall(t, srv, http.MethodPut, notePath("Japan.md"), `{"body":"x","create":true}`)
	if w.Code != http.StatusConflict {
		t.Errorf("create twice: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "trips", "Peru.md")); err != nil {
		t.Errorf("the note is a plain file: %v", err)
	}

	var list struct {
		Root  string
		Notes []struct{ Path, Title string }
	}
	w = noteCall(t, srv, http.MethodGet, "/v1/notes", "")
	err := json.Unmarshal(w.Body.Bytes(), &list)
	if err != nil || len(list.Notes) != 2 || list.Root != dir || list.Notes[0].Title != "Japan" {
		t.Fatalf("list = %s", w.Body)
	}

	var view struct {
		Body      string
		Links     []struct{ Target, Path string }
		Backlinks []struct{ Path, Context string }
	}
	w = noteCall(t, srv, http.MethodGet, notePath("Japan.md"), "")
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil ||
		view.Links[0].Path != "trips/Peru.md" ||
		len(view.Backlinks) != 1 ||
		view.Backlinks[0].Path != "trips/Peru.md" {
		t.Fatalf("view = %s", w.Body)
	}

	w = noteCall(t, srv, http.MethodPost, "/v1/notes/rename", `{"from":"Japan.md","to":"Nippon.md"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"updated":1`) {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	w = noteCall(t, srv, http.MethodPost, "/v1/notes/rename", `{"from":"Japan.md","to":"x.md"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("rename a missing note: %d", w.Code)
	}
	if w := noteCall(t, srv, http.MethodPost, "/v1/notes/rename", `{`); w.Code != http.StatusBadRequest {
		t.Errorf("rename not json: %d", w.Code)
	}
	if w := noteCall(t, srv, http.MethodDelete, notePath("trips/Peru.md"), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := noteCall(t, srv, http.MethodDelete, notePath("trips/Peru.md"), ""); w.Code != http.StatusNotFound {
		t.Errorf("delete twice: %d", w.Code)
	}
}

func TestNoteRefusalsHaveTheirOwnStatus(t *testing.T) {
	srv, _ := noteServer(t)
	for name, c := range map[string]struct {
		method, path, body string
		want               int
	}{
		"path outside":  {http.MethodGet, notePath("../etc/passwd.md"), "", http.StatusBadRequest},
		"not markdown":  {http.MethodGet, notePath("a.txt"), "", http.StatusBadRequest},
		"missing":       {http.MethodGet, notePath("nope.md"), "", http.StatusNotFound},
		"save not json": {http.MethodPut, notePath("a.md"), "{", http.StatusBadRequest},
		"save bad path": {http.MethodPut, notePath("../a.md"), `{"body":""}`, http.StatusBadRequest},
		"too big": {
			http.MethodPut, notePath("a.md"), `{"body":"` + strings.Repeat("x", 200<<10) + `"}`,
			http.StatusRequestEntityTooLarge,
		},
		"delete bad path": {http.MethodDelete, notePath(""), "", http.StatusBadRequest},
	} {
		if w := noteCall(t, srv, c.method, c.path, c.body); w.Code != c.want {
			t.Errorf("%s: %d %s, want %d", name, w.Code, w.Body, c.want)
		}
	}
}

func TestNoteErrorStatuses(t *testing.T) {
	for err, want := range map[error]int{
		notes.ErrTooBig:  http.StatusRequestEntityTooLarge,
		notes.ErrTooMany: http.StatusConflict,
		os.ErrPermission: http.StatusInternalServerError,
	} {
		w := httptest.NewRecorder()
		writeNoteError(w, err)
		if w.Code != want {
			t.Errorf("%v: %d, want %d", err, w.Code, want)
		}
	}
}

func TestNotesNeedAVault(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	for _, c := range [][2]string{
		{http.MethodGet, "/v1/notes"}, {http.MethodGet, notePath("a.md")}, {http.MethodPut, notePath("a.md")},
		{http.MethodDelete, notePath("a.md")}, {http.MethodPost, "/v1/notes/rename"},
		{http.MethodGet, "/v1/notes/search?q=a"}, {http.MethodGet, "/v1/notes/graph"},
	} {
		if w := noteCall(t, srv, c[0], c[1], `{}`); w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: %d", c[0], c[1], w.Code)
		}
	}
}

func TestNotesNeedTheToken(t *testing.T) {
	srv, _ := noteServer(t)
	if w := get(t, srv, "/v1/notes", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", w.Code)
	}
}

func TestNoteSearchFallsBackToPlainWordsAndUsesTheIndexWhenOn(t *testing.T) {
	// No config file: the plain search.
	srv, _ := noteServer(t)
	noteCall(t, srv, http.MethodPut, notePath("bread.md"), `{"body":"Bake the dough\nrest it"}`)
	noteCall(t, srv, http.MethodPut, notePath("other.md"), `{"body":"nothing"}`)
	var out struct{ Hits []noteHit }
	w := noteCall(t, srv, http.MethodGet, "/v1/notes/search?q=dough&limit=5", "")
	err := json.Unmarshal(w.Body.Bytes(), &out)
	if err != nil || len(out.Hits) != 1 || out.Hits[0].Path != "bread.md" || out.Hits[0].Line != 1 {
		t.Fatalf("plain = %s", w.Body)
	}
	if w := noteCall(t, srv, http.MethodGet, "/v1/notes/search?q=", ""); !strings.Contains(w.Body.String(), `"hits":[]`) {
		t.Errorf("blank = %s", w.Body)
	}

	// The code index on: the same vault, ranked by the index.
	cfg := networkConfig(t, "llm:\n  provider: openai\ncode_index:\n  enabled: true\n  semantic: false\n  embeddings_auto: false\n  cache_dir: "+t.TempDir()+"\n")
	srv, _ = noteServer(t, func(o *Options) { o.ConfigPath = cfg })
	noteCall(t, srv, http.MethodPut, notePath("bread.md"), `{"body":"\nBake the dough\nrest it"}`)
	os.WriteFile(filepath.Join(srv.opts.NotesDir, "readme.txt"), []byte("dough"), 0o644)
	out.Hits = nil
	w = noteCall(t, srv, http.MethodGet, "/v1/notes/search?q=dough", "")
	err = json.Unmarshal(w.Body.Bytes(), &out)
	if err != nil || len(out.Hits) != 1 || out.Hits[0].Path != "bread.md" || out.Hits[0].Snippet == "" {
		t.Fatalf("indexed = %s", w.Body)
	}
	// An empty vault is not indexed.
	empty, _ := noteServer(t, func(o *Options) { o.ConfigPath = cfg })
	w = noteCall(t, empty, http.MethodGet, "/v1/notes/search?q=dough", "")
	if !strings.Contains(w.Body.String(), `"hits":[]`) {
		t.Errorf("empty = %s", w.Body)
	}
	// An index that fails falls back to the plain search.
	broken := networkConfig(t, "llm: [\n")
	bsrv, _ := noteServer(t, func(o *Options) { o.ConfigPath = broken })
	noteCall(t, bsrv, http.MethodPut, notePath("bread.md"), `{"body":"dough"}`)
	w = noteCall(t, bsrv, http.MethodGet, "/v1/notes/search?q=dough", "")
	if !strings.Contains(w.Body.String(), "bread.md") {
		t.Errorf("fallback = %s", w.Body)
	}
}

func TestNoteFirstSnippetLine(t *testing.T) {
	if got := firstSnippetLine("\n  \n  hello  \nworld"); got != "hello" {
		t.Errorf("%q", got)
	}
	if got := firstSnippetLine(" \n"); got != "" {
		t.Errorf("%q", got)
	}
}

func TestNotesGraphListsNotesAndLinks(t *testing.T) {
	srv, _ := noteServer(t)
	noteCall(t, srv, http.MethodPut, notePath("a.md"), `{"body":"[[b]] [[ghost]]"}`)
	noteCall(t, srv, http.MethodPut, notePath("b.md"), `{"body":"x"}`)
	var g notes.Graph
	w := noteCall(t, srv, http.MethodGet, "/v1/notes/graph", "")
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil || len(g.Nodes) != 2 || len(g.Edges) != 1 || g.Missing != 1 {
		t.Fatalf("graph = %s", w.Body)
	}
}

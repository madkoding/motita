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

	"github.com/madkoding/motita/internal/memory"
)

func memoryServer(t *testing.T, mutators ...func(*Options)) (*Server, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "memory")
	srv := newTestServer(
		t, &fakeService{}, append([]func(*Options){func(o *Options) { o.MemoryDir = dir }}, mutators...)...,
	)
	return srv, dir
}

func memoryCall(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return sendNetwork(t, srv, method, path, body)
}

func entryPath(p string) string { return "/v1/memory/entry?path=" + url.QueryEscape(p) }

// seedEntries makes the memory of two entries that link to each other, one of them in a folder.
func seedEntries(t *testing.T) (*Server, string) {
	t.Helper()
	srv, dir := memoryServer(t)
	w := memoryCall(t, srv, http.MethodPut, entryPath("Japan.md"), `{"body":"# Japan\nSee [[Peru]]","create":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	body := "---\nauthor: Ana\ndate: 2026-03-10\n---\nBack to [[Japan]]"
	b, _ := json.Marshal(map[string]any{"body": body, "create": true})
	w = memoryCall(t, srv, http.MethodPut, entryPath("trips/Peru.md"), string(b))
	if w.Code != http.StatusOK {
		t.Fatalf("create in a folder: %d %s", w.Code, w.Body)
	}
	return srv, dir
}

func TestEntriesAreCreatedAndListed(t *testing.T) {
	empty, _ := memoryServer(t)
	w := memoryCall(t, empty, http.MethodGet, "/v1/memory", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"entries":[]`) {
		t.Fatalf("empty memory: %d %s", w.Code, w.Body)
	}
	srv, dir := seedEntries(t)
	w = memoryCall(t, srv, http.MethodPut, entryPath("Japan.md"), `{"body":"x","create":true}`)
	if w.Code != http.StatusConflict {
		t.Errorf("create twice: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "trips", "Peru.md")); err != nil {
		t.Errorf("the entry is a plain file: %v", err)
	}
	var list struct {
		Root       string
		Entries    []struct{ Path, Title string }
		Namespaces []memory.NamespaceInfo
	}
	w = memoryCall(t, srv, http.MethodGet, "/v1/memory", "")
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("list = %s", w.Body)
	}
	if len(list.Entries) != 2 || list.Root != dir || list.Entries[0].Title != "Japan" {
		t.Errorf("list = %s", w.Body)
	}
	if len(list.Namespaces) != 2 || list.Namespaces[1].Name != "trips" {
		t.Errorf("namespaces = %s", w.Body)
	}
}

func TestEntriesAreReadWithLinksBacklinksAndMeta(t *testing.T) {
	srv, _ := seedEntries(t)
	var view struct {
		Body      string
		Meta      memory.Meta
		Links     []struct{ Target, Path string }
		Backlinks []struct{ Path, Context string }
	}
	w := memoryCall(t, srv, http.MethodGet, entryPath("Japan.md"), "")
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("view = %s", w.Body)
	}
	if len(view.Links) != 1 || view.Links[0].Path != "trips/Peru.md" {
		t.Errorf("links = %s", w.Body)
	}
	if len(view.Backlinks) != 1 || view.Backlinks[0].Path != "trips/Peru.md" {
		t.Errorf("backlinks = %s", w.Body)
	}
	w = memoryCall(t, srv, http.MethodGet, entryPath("trips/Peru.md"), "")
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || view.Meta.Author != "Ana" {
		t.Fatalf("meta = %s", w.Body)
	}
	if view.Meta.Date.Format("2006-01-02") != "2026-03-10" {
		t.Errorf("date = %s", w.Body)
	}
}

func TestEntriesAreRenamedAndDeleted(t *testing.T) {
	srv, _ := seedEntries(t)
	w := memoryCall(t, srv, http.MethodPost, "/v1/memory/rename", `{"from":"Japan.md","to":"Nippon.md"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"updated":1`) {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	w = memoryCall(t, srv, http.MethodPost, "/v1/memory/rename", `{"from":"Japan.md","to":"x.md"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("rename a missing entry: %d", w.Code)
	}
	if w := memoryCall(t, srv, http.MethodPost, "/v1/memory/rename", `{`); w.Code != http.StatusBadRequest {
		t.Errorf("rename not json: %d", w.Code)
	}
	if w := memoryCall(t, srv, http.MethodDelete, entryPath("trips/Peru.md"), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := memoryCall(t, srv, http.MethodDelete, entryPath("trips/Peru.md"), ""); w.Code != http.StatusNotFound {
		t.Errorf("delete twice: %d", w.Code)
	}
}

func TestEntryRefusalsHaveTheirOwnStatus(t *testing.T) {
	srv, _ := memoryServer(t)
	for name, c := range map[string]struct {
		method, path, body string
		want               int
	}{
		"path outside":  {http.MethodGet, entryPath("../etc/passwd.md"), "", http.StatusBadRequest},
		"not markdown":  {http.MethodGet, entryPath("a.txt"), "", http.StatusBadRequest},
		"missing":       {http.MethodGet, entryPath("nope.md"), "", http.StatusNotFound},
		"save not json": {http.MethodPut, entryPath("a.md"), "{", http.StatusBadRequest},
		"save bad path": {http.MethodPut, entryPath("../a.md"), `{"body":""}`, http.StatusBadRequest},
		"too big": {
			http.MethodPut, entryPath("a.md"), `{"body":"` + strings.Repeat("x", 200<<10) + `"}`,
			http.StatusRequestEntityTooLarge,
		},
		"delete bad path": {http.MethodDelete, entryPath(""), "", http.StatusBadRequest},
	} {
		if w := memoryCall(t, srv, c.method, c.path, c.body); w.Code != c.want {
			t.Errorf("%s: %d %s, want %d", name, w.Code, w.Body, c.want)
		}
	}
}

func TestMemoryErrorStatuses(t *testing.T) {
	for err, want := range map[error]int{
		memory.ErrTooBig:  http.StatusRequestEntityTooLarge,
		memory.ErrTooMany: http.StatusConflict,
		os.ErrPermission:  http.StatusInternalServerError,
	} {
		w := httptest.NewRecorder()
		writeMemoryError(w, err)
		if w.Code != want {
			t.Errorf("%v: %d, want %d", err, w.Code, want)
		}
	}
}

func TestMemoryNeedsAFolder(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	for _, c := range [][2]string{
		{http.MethodGet, "/v1/memory"}, {http.MethodGet, entryPath("a.md")}, {http.MethodPut, entryPath("a.md")},
		{http.MethodDelete, entryPath("a.md")}, {http.MethodPost, "/v1/memory/rename"},
		{http.MethodGet, "/v1/memory/search?q=a"}, {http.MethodGet, "/v1/memory/graph"},
		{http.MethodDelete, "/v1/memory/folder?path=A"},
	} {
		if w := memoryCall(t, srv, c[0], c[1], `{}`); w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: %d", c[0], c[1], w.Code)
		}
	}
}

func TestMemoryNeedsTheToken(t *testing.T) {
	srv, _ := memoryServer(t)
	if w := get(t, srv, "/v1/memory", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", w.Code)
	}
}

func TestDeletingAFolderRemovesItsEntries(t *testing.T) {
	srv, dir := memoryServer(t)
	memoryCall(t, srv, http.MethodPut, entryPath("HR/a.md"), `{"body":"a"}`)
	memoryCall(t, srv, http.MethodPut, entryPath("HR/deep/b.md"), `{"body":"b"}`)
	memoryCall(t, srv, http.MethodPut, entryPath("Eng/c.md"), `{"body":"c"}`)
	w := memoryCall(t, srv, http.MethodDelete, "/v1/memory/folder?path=HR", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":2`) {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "HR")); err == nil {
		t.Error("the collection's folder is still there")
	}
	if _, err := os.Stat(filepath.Join(dir, "Eng", "c.md")); err != nil {
		t.Errorf("another collection lost its entry: %v", err)
	}
	if w := memoryCall(t, srv, http.MethodDelete, "/v1/memory/folder?path=..%2Fx", ""); w.Code != http.StatusBadRequest {
		t.Errorf("a bad path: %d", w.Code)
	}
	if w := memoryCall(t, srv, http.MethodDelete, "/v1/memory/folder?path=Nope", ""); w.Code != http.StatusNotFound {
		t.Errorf("a missing folder: %d", w.Code)
	}
}

type searchAnswer struct {
	Hits, Related []memory.Hit
	Edges         []memory.Edge
	Warning       string
}

// memorySearcher asks the memory of srv and decodes the answer.
func memorySearcher(t *testing.T, srv *Server) func(q string) searchAnswer {
	return func(q string) (out searchAnswer) {
		w := memoryCall(t, srv, http.MethodGet, "/v1/memory/search?"+q, "")
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body)
		}
		return out
	}
}

// seedHR fills the memory with two collections and returns its searcher.
func seedHR(t *testing.T) func(q string) searchAnswer {
	srv, _ := memoryServer(t)
	put := func(p, body string) {
		b, _ := json.Marshal(map[string]any{"body": body})
		if w := memoryCall(t, srv, http.MethodPut, entryPath(p), string(b)); w.Code != http.StatusOK {
			t.Fatalf("put %s: %d %s", p, w.Code, w.Body)
		}
	}
	put("HR/Hiring.md", "---\nauthor: Ana\ndate: 2026-03-10\n---\n# Hiring\n\n## Salary bands\n\n"+
		"Senior engineers top out at 90k. See [[Onboarding]].")
	put("HR/Onboarding.md", "---\nauthor: Luis\ndate: 2025-01-05\n---\n# Onboarding\n\n"+
		"First week: laptop and salary forms.")
	put("Eng/Salary notes.md", "# Salary notes\n\nSalary bands of the platform team.")
	return memorySearcher(t, srv)
}

func TestMemorySearchStaysInACollectionAndSignsItsAnswers(t *testing.T) {
	search := seedHR(t)
	if hits := search("q=salary+bands").Hits; len(hits) != 3 {
		t.Errorf("all = %+v", hits)
	}
	got := search("q=salary+bands&namespace=HR").Hits
	if len(got) != 2 || got[0].Namespace != "HR" || got[1].Namespace != "HR" {
		t.Errorf("namespace = %+v", got)
	}
	for _, h := range got {
		if h.Path == "HR/Hiring.md" && (h.Author != "Ana" || h.Heading != "Salary bands") {
			t.Errorf("hiring = %+v", h)
		}
	}
}

func TestMemorySearchFiltersByAuthorAndDate(t *testing.T) {
	search := seedHR(t)
	// filters can be written in the query itself, and the parameters win over them
	for q, want := range map[string]string{
		"q=salary&author=luis":                   "HR/Onboarding.md",
		"q=salary&since=2026-01-01&namespace=HR": "HR/Hiring.md",
		"q=salary&until=2025-12-31&namespace=HR": "HR/Onboarding.md",
		"q=salary+ns%3AEng":                      "Eng/Salary notes.md",
	} {
		if got := search(q).Hits; len(got) != 1 || got[0].Path != want {
			t.Errorf("%s = %+v, want %s", q, got, want)
		}
	}
	if got := search("q=salary+ns%3AEng&namespace=HR").Hits; len(got) == 0 || got[0].Namespace != "HR" {
		t.Errorf("param wins = %+v", got)
	}
}

func TestMemorySearchFollowsLinksOnRequest(t *testing.T) {
	search := seedHR(t)
	g := search("q=senior+engineers&limit=1&graph=1&namespace=HR")
	if len(g.Hits) != 1 || len(g.Related) != 1 || len(g.Edges) != 1 || g.Related[0].Path != "HR/Onboarding.md" {
		t.Errorf("graph = %+v", g)
	}
	if got := search("q=").Hits; len(got) != 0 {
		t.Errorf("blank = %+v", got)
	}
}

func TestMemoryGraphListsEntriesAndLinks(t *testing.T) {
	srv, _ := memoryServer(t)
	memoryCall(t, srv, http.MethodPut, entryPath("a.md"), `{"body":"[[b]] [[ghost]]"}`)
	memoryCall(t, srv, http.MethodPut, entryPath("b.md"), `{"body":"x"}`)
	var g memory.Graph
	w := memoryCall(t, srv, http.MethodGet, "/v1/memory/graph", "")
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil || len(g.Nodes) != 2 || len(g.Edges) != 1 || g.Missing != 1 {
		t.Fatalf("graph = %s", w.Body)
	}
}

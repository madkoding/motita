package plan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/codeindex"
	"github.com/madkoding/motita/internal/llm"
)

type fakeIndex struct {
	hits  []codeindex.Hit
	err   error
	root  string
	query string
	limit int
}

func (f *fakeIndex) Search(_ context.Context, root, q string, limit int) ([]codeindex.Hit, error) {
	f.root, f.query, f.limit = root, q, limit
	return f.hits, f.err
}

func TestSearchIndexIsOfferedOnlyWithAnIndex(t *testing.T) {
	has := func(p *Planner) bool {
		for _, tool := range p.tools() {
			if tool.Function.Name == "search_index" {
				return true
			}
		}
		return false
	}
	if has(&Planner{}) {
		t.Error("search_index offered with no index")
	}
	if !has((&Planner{}).WithIndex(&fakeIndex{})) {
		t.Error("search_index missing with an index")
	}
}

func TestToolSearchIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeIndex{hits: []codeindex.Hit{{Path: "a.go", Line: 3, Snippet: "func A() {}"}}}
	p := (&Planner{}).WithIndex(f)
	ctx := context.Background()
	call := llm.ToolCall{Function: llm.FunctionCall{
		Name: "search_index", Arguments: raw(`{"query":" rotate wal ","limit":3,"path":"` + filepath.ToSlash(dir) + `"}`),
	}}
	out := p.dispatchTool(ctx, call)
	if !strings.Contains(out, "a.go:3\nfunc A() {}") || f.query != "rotate wal" || f.limit != 3 || f.root != dir {
		t.Errorf("out=%q root=%q query=%q limit=%d", out, f.root, f.query, f.limit)
	}
	out = p.toolSearchIndex(ctx, raw(`{"query":"  "}`))
	if !strings.Contains(out, "'query' parameter is missing") {
		t.Errorf("empty query: %q", out)
	}
	out = p.toolSearchIndex(ctx, raw(`{"query":`))
	if !strings.HasPrefix(out, errParsingArguments) {
		t.Errorf("bad arguments: %q", out)
	}
	out = p.toolSearchIndex(ctx, raw(`{"query":"x","path":"`+filepath.ToSlash(filepath.Join(dir, "f.txt"))+`"}`))
	if !strings.Contains(out, "is not a directory") {
		t.Errorf("a file: %q", out)
	}
	out = p.toolSearchIndex(ctx, raw(`{"query":"x","path":"`+filepath.ToSlash(filepath.Join(dir, "nope"))+`"}`))
	if !strings.Contains(out, "is not a directory") {
		t.Errorf("a folder that does not exist: %q", out)
	}
	f.err = errors.New("boom")
	out = p.toolSearchIndex(ctx, raw(`{"query":"x","path":"`+filepath.ToSlash(dir)+`"}`))
	if out != "Error searching the index: boom" {
		t.Errorf("index error: %q", out)
	}
}

func TestToolSearchIndexReadsOnlyWhatTheSessionMayRead(t *testing.T) {
	work, other := t.TempDir(), t.TempDir()
	p := (&Planner{runner: &rootedRunner{root: work}}).WithIndex(&fakeIndex{})
	out := p.toolSearchIndex(context.Background(), raw(`{"query":"x","path":"`+filepath.ToSlash(other)+`"}`))
	if !strings.HasPrefix(out, errPrefix) {
		t.Errorf("a folder outside the session was searched: %q", out)
	}
}

func TestWithCodeIndexFollowsTheSwitch(t *testing.T) {
	if p := (&Planner{}).WithCodeIndex(false, codeindex.Options{}); p.index != nil {
		t.Error("a disabled block must not offer the tool")
	}
	o := codeindex.Options{MaxFileKB: 64, MaxFiles: 10, CacheDir: "/c", Semantic: true}
	p := (&Planner{}).WithCodeIndex(true, o)
	want := codeindex.Shared(o)
	if p.index != IndexSearcher(want) {
		t.Errorf("index = %#v", p.index)
	}
}

type warningIndex struct {
	fakeIndex
	warning string
}

func (w *warningIndex) Warning() string { return w.warning }

func TestSearchIndexPassesTheIndexWarningOn(t *testing.T) {
	dir := t.TempDir()
	w := &warningIndex{fakeIndex: fakeIndex{hits: []codeindex.Hit{{Path: "a.go", Line: 1, Snippet: "x"}}}}
	p := (&Planner{}).WithIndex(w)
	args := raw(`{"query":"x","path":"` + filepath.ToSlash(dir) + `"}`)
	if out := p.toolSearchIndex(context.Background(), args); strings.Contains(out, "embeddings") {
		t.Errorf("no warning, none shown: %q", out)
	}
	w.warning = "embeddings unavailable: down"
	if out := p.toolSearchIndex(context.Background(), args); !strings.HasSuffix(out, "\n(embeddings unavailable: down)") {
		t.Errorf("the warning is appended: %q", out)
	}
}

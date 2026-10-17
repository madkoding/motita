package plan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/xerj"
)

type fakeIndex struct {
	hits  []xerj.Hit
	err   error
	query string
	size  int
}

func (f *fakeIndex) Search(_ context.Context, q string, size int) ([]xerj.Hit, error) {
	f.query, f.size = q, size
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
	f := &fakeIndex{hits: []xerj.Hit{{Path: "a.go", Line: 3, Passage: "func A() {}"}}}
	p := (&Planner{}).WithIndex(f)
	call := llm.ToolCall{Function: llm.FunctionCall{
		Name: "search_index", Arguments: raw(`{"query":" rotate wal ","limit":3}`),
	}}
	out := p.dispatchTool(context.Background(), call)
	if !strings.Contains(out, "a.go:3\nfunc A() {}") || f.query != "rotate wal" || f.size != 3 {
		t.Errorf("out=%q query=%q size=%d", out, f.query, f.size)
	}
	out = p.toolSearchIndex(context.Background(), raw(`{"query":"  "}`))
	if !strings.Contains(out, "'query' parameter is missing") {
		t.Errorf("empty query: %q", out)
	}
	out = p.toolSearchIndex(context.Background(), raw(`{"query":`))
	if !strings.HasPrefix(out, errParsingArguments) {
		t.Errorf("bad arguments: %q", out)
	}
	f.err = errors.New("no node")
	out = p.toolSearchIndex(context.Background(), raw(`{"query":"x"}`))
	if out != "Error searching the index: no node" {
		t.Errorf("node down: %q", out)
	}
}

func TestWithXerjFollowsTheSwitch(t *testing.T) {
	if p := (&Planner{}).WithXerj(false, "http://h", "", ""); p.index != nil {
		t.Error("a disabled block must not offer the tool")
	}
	p := (&Planner{}).WithXerj(true, "http://h:9200/", "k", "ax-code")
	c, ok := p.index.(*xerj.Client)
	if !ok || c.URL != "http://h:9200" || c.APIKey != "k" || c.Index != "ax-code" {
		t.Errorf("index = %#v", p.index)
	}
}

package plan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
)

type fakeWeb struct {
	text string
	err  error
	url  string
}

func (f *fakeWeb) Fetch(_ context.Context, url string) (string, error) {
	f.url = url
	return f.text, f.err
}

func TestFetchURLIsOfferedOnlyWithAWebReader(t *testing.T) {
	has := func(p *Planner) bool {
		for _, tool := range p.tools() {
			if tool.Function.Name == "fetch_url" {
				return true
			}
		}
		return false
	}
	if has(&Planner{}) || !has((&Planner{}).WithWeb(&fakeWeb{})) {
		t.Error("fetch_url must be offered exactly when a reader is set")
	}
}

func TestToolFetchURL(t *testing.T) {
	ctx := context.Background()
	f := &fakeWeb{text: "hello page"}
	p := (&Planner{}).WithWeb(f)
	call := func(args string) string {
		return p.dispatchTool(ctx, llm.ToolCall{Function: llm.FunctionCall{Name: "fetch_url", Arguments: raw(args)}})
	}
	out := call(`{"url":" https://example.com/a "}`)
	if out != "GET https://example.com/a\nhello page" || f.url != "https://example.com/a" {
		t.Errorf("out = %q url = %q", out, f.url)
	}
	if out = call(`{"url":" "}`); !strings.Contains(out, "'url' parameter is missing") {
		t.Errorf("out = %q", out)
	}
	if out = call(`{"url":`); !strings.HasPrefix(out, errParsingArguments) {
		t.Errorf("out = %q", out)
	}
	f.text = "  "
	if out = call(`{"url":"https://x"}`); out != "(the page has no text)" {
		t.Errorf("out = %q", out)
	}
	f.err = errors.New("closed")
	if out = call(`{"url":"https://x"}`); out != "Error: closed" {
		t.Errorf("out = %q", out)
	}
}

func TestFetchURLWithoutAReaderSaysSo(t *testing.T) {
	out := (&Planner{}).dispatchTool(context.Background(),
		llm.ToolCall{Function: llm.FunctionCall{Name: "fetch_url", Arguments: raw(`{"url":"https://x"}`)}})
	if !strings.Contains(out, "not available") {
		t.Errorf("out = %q", out)
	}
}

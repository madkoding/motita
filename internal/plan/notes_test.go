package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/notes"
)

func TestNoteToolsAreOfferedOnlyWithAVault(t *testing.T) {
	count := func(p *Planner) int {
		n := 0
		for _, tool := range p.tools() {
			switch tool.Function.Name {
			case "search_notes", "read_note", "write_note", "append_note":
				n++
			}
		}
		return n
	}
	v := notes.Open(t.TempDir())
	if count(&Planner{}) != 0 || count((&Planner{}).WithNotes(v)) != 4 {
		t.Error("the note tools must be offered exactly when a vault is set")
	}
}

func TestNoteToolsReadAndWriteTheVault(t *testing.T) {
	v := notes.Open(t.TempDir())
	p := (&Planner{}).WithNotes(v)
	call := func(name, args string) string {
		return p.dispatchTool(context.Background(), llm.ToolCall{Function: llm.FunctionCall{Name: name, Arguments: raw(args)}})
	}
	if out := call("write_note", `{"note":"trips/Japan","content":"Ramen in Tokyo"}`); !strings.Contains(out, "Created [[Japan]]") {
		t.Errorf("write = %q", out)
	}
	if out := call("append_note", `{"note":"Japan","content":"Go in May"}`); !strings.Contains(out, "Added to [[Japan]]") {
		t.Errorf("append = %q", out)
	}
	if out := call("search_notes", `{"query":"ramen"}`); !strings.Contains(out, "[[Japan]] (trips/Japan.md)") {
		t.Errorf("search = %q", out)
	}
	if out := call("read_note", `{"note":"japan"}`); !strings.Contains(out, "Ramen in Tokyo") || !strings.Contains(out, "Go in May") {
		t.Errorf("read = %q", out)
	}
	if out := call("read_note", `{"note":`); !strings.HasPrefix(out, errParsingArguments) {
		t.Errorf("bad arguments = %q", out)
	}
}

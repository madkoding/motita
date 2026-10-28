package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/memory"
)

func TestMemoryToolsAreOfferedOnlyWithAMemory(t *testing.T) {
	count := func(p *Planner) int {
		n := 0
		for _, tool := range p.tools() {
			switch tool.Function.Name {
			case "search_memory", "read_memory", "write_memory", "append_memory":
				n++
			default:
				// not a note tool
			}
		}
		return n
	}
	v := memory.Open(t.TempDir())
	if count(&Planner{}) != 0 || count((&Planner{}).WithMemory(v)) != 4 {
		t.Error("the memory tools must be offered exactly when a memory is set")
	}
}

func TestMemoryToolsReadAndWriteTheMemory(t *testing.T) {
	v := memory.Open(t.TempDir())
	p := (&Planner{}).WithMemory(v)
	call := func(name, args string) string {
		fn := llm.FunctionCall{Name: name, Arguments: raw(args)}
		return p.dispatchTool(context.Background(), llm.ToolCall{Function: fn})
	}
	out := call("write_memory", `{"entry":"trips/Japan","content":"Ramen in Tokyo"}`)
	if !strings.Contains(out, "Created [[Japan]]") {
		t.Errorf("write = %q", out)
	}
	out = call("append_memory", `{"entry":"Japan","content":"Go in May"}`)
	if !strings.Contains(out, "Added to [[Japan]]") {
		t.Errorf("append = %q", out)
	}
	out = call("search_memory", `{"query":"ramen"}`)
	if !strings.Contains(out, "[[Japan]] (trips/Japan.md)") {
		t.Errorf("search = %q", out)
	}
	out = call("read_memory", `{"entry":"japan"}`)
	if !strings.Contains(out, "Ramen in Tokyo") || !strings.Contains(out, "Go in May") {
		t.Errorf("read = %q", out)
	}
	if out := call("read_memory", `{"entry":`); !strings.HasPrefix(out, errParsingArguments) {
		t.Errorf("bad arguments = %q", out)
	}
}

func TestSearchMemoryToolPassesTheCollectionAndTheGraph(t *testing.T) {
	v := memory.Open(t.TempDir())
	for path, body := range map[string]string{
		"HR/Hiring.md":        "# Hiring\n\nSalary bands, see [[Onboarding]].",
		"HR/Onboarding.md":    "# Onboarding\n\nFirst week checklist.",
		"Eng/Salary notes.md": "# Salary notes\n\nSalary bands of the platform team.",
	} {
		if _, err := v.Write(path, body, true); err != nil {
			t.Fatal(err)
		}
	}
	p := (&Planner{}).WithMemory(v)
	call := func(args string) string {
		fn := llm.FunctionCall{Name: "search_memory", Arguments: raw(args)}
		return p.dispatchTool(context.Background(), llm.ToolCall{Function: fn})
	}
	if out := call(`{"query":"salary bands","namespace":"HR"}`); !strings.Contains(out, "HR/Hiring.md") || strings.Contains(out, "Eng/") {
		t.Errorf("namespace = %q", out)
	}
	if out := call(`{"query":"salary bands","namespace":"HR","graph":true}`); !strings.Contains(out, "Related through links") {
		t.Errorf("graph = %q", out)
	}
}

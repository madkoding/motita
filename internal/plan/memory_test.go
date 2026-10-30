package plan

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/memory"
)

// The memory tools are no longer offered (recall and remember reach the memory), and a call by an
// old name without a memory says so instead of failing.
func TestMemoryToolsAreNotOfferedButStillAnswered(t *testing.T) {
	for _, tool := range (&Planner{}).WithMemory(memory.Open(t.TempDir())).tools() {
		if strings.HasSuffix(tool.Function.Name, "_memory") {
			t.Errorf("%s is still offered", tool.Function.Name)
		}
	}
	fn := llm.FunctionCall{Name: "search_memory", Arguments: raw(`{"query":"x"}`)}
	got := (&Planner{}).dispatchTool(context.Background(), llm.ToolCall{Function: fn})
	if !strings.Contains(got, "no memory") {
		t.Errorf("search_memory without a memory = %q", got)
	}
}

func TestMemoryToolsReadAndWriteTheMemory(t *testing.T) {
	v := memory.Open(t.TempDir())
	var trace []string
	p := (&Planner{}).WithMemory(v).WithTrace(func(format string, args ...any) {
		trace = append(trace, fmt.Sprintf(format, args...))
	})
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
	call("write_memory", `{"entry":"trips/Japan","content":"again"}`)
	var saves []string
	for _, line := range trace {
		if strings.HasPrefix(line, memory.SavedPrefix) {
			saves = append(saves, line)
		}
	}
	if strings.Join(saves, "|") != "saved to memory: Japan (trips/Japan.md)|saved to memory: Japan (trips/Japan.md)" {
		t.Errorf("the saves were not reported, or a refusal was: %q", trace)
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
	out := call(`{"query":"salary bands","namespace":"HR"}`)
	if !strings.Contains(out, "HR/Hiring.md") || strings.Contains(out, "Eng/") {
		t.Errorf("namespace = %q", out)
	}
	graph := call(`{"query":"salary bands","namespace":"HR","graph":true}`)
	if !strings.Contains(graph, "Related through links") {
		t.Errorf("graph = %q", graph)
	}
}

func TestThePromptTellsThePlannerToSaveToTheMemory(t *testing.T) {
	for _, want := range []string{"SAVE, do not only read", "remember it in memory", "remember"} {
		if !strings.Contains(SystemPrompt, want) {
			t.Errorf("the plan prompt must tell the model when to save to the memory; missing %q", want)
		}
	}
}

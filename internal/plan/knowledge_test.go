package plan

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/memory"
	"github.com/madkoding/motita/internal/skills"
)

// recall and remember reach the memory, the library and the conversation from one planner, and a
// save to the memory is reported like the old tools reported it.
func TestRecallAndRememberReachEveryPlace(t *testing.T) {
	mem := memory.Open(t.TempDir())
	lib := skills.New(t.TempDir())
	var trace []string
	p := (&Planner{}).WithMemory(mem).WithLibrary(lib).
		WithTrace(func(f string, a ...any) { trace = append(trace, fmt.Sprintf(f, a...)) }).
		WithRecall(func(_ context.Context, q string, live int) (string, int) {
			if live != 0 {
				t.Errorf("an explicit recall must search every archived turn, live = %d", live)
			}
			return "Turn 1: we chose the blue theme", 1
		})
	call := func(name, args string) string {
		fn := llm.FunctionCall{Name: name, Arguments: raw(args)}
		return p.dispatchTool(context.Background(), llm.ToolCall{Function: fn})
	}

	got := call("remember", `{"entry":"Theme","content":"The person wants the blue theme."}`)
	if !strings.HasPrefix(got, "Added to [[Theme]]") {
		t.Fatalf("remember = %q", got)
	}
	if !strings.Contains(strings.Join(trace, "\n"), memory.SavedPrefix+"Theme") {
		t.Errorf("the save was not reported: %v", trace)
	}
	got = call("remember", `{"in":"procedures","entry":"theme-switch",`+
		`"content":"# Theme switch\nUse when...\n## Procedure\n1. a"}`)
	if !strings.Contains(got, "Saved the skill") {
		t.Fatalf("remember a procedure = %q", got)
	}
	got = call("recall", `{"query":"blue theme switch"}`)
	for _, want := range []string{"we chose the blue theme", "[[Theme]]", "theme-switch"} {
		if !strings.Contains(got, want) {
			t.Errorf("recall lacks %q:\n%s", want, got)
		}
	}
	if got := call("recall", `{"in":"procedures","open":"theme-switch"}`); !strings.Contains(got, "# Theme switch") {
		t.Errorf("open a procedure = %q", got)
	}
	if got := call("recall", `not json`); !strings.HasPrefix(got, errParsingArguments) {
		t.Errorf("bad arguments = %q", got)
	}
}

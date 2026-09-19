package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/llm"
)

// The parts of the planner a run does not reach on its own: the stream hook, the
// tool-argument decoding, the loop floor and the two tool paths that only fail on
// bad input.

// TestWithStreamReceivesLiveOutput: the stream hook is what lets the interface show
// the answer as it is written instead of only at the end.
func TestWithStreamReceivesLiveOutput(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{{content: "streamed answer"}})
	defer srv.Close()

	var live strings.Builder
	p := New(newClient(t, srv), a).WithStream(func(s string) { live.WriteString(s) })
	out, err := p.Run(context.Background(), "question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "streamed answer" {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(live.String(), "streamed") {
		t.Errorf("the stream hook received %q, want the live text", live.String())
	}
}

// TestWriteStreamWithoutAHook: the planner runs without an interface attached (the
// one-shot -p path), and writeStream must simply do nothing then.
func TestWriteStreamWithoutAHook(t *testing.T) {
	p := &Planner{}
	if p.stream != nil {
		t.Fatal("a fresh planner must have no stream hook")
	}
	p.writeStream("nothing should happen") // must not panic
}

// TestWithLoopsFloorsAtOne: WithLoops is where the floor lives, so a configuration
// that asks for zero (or fewer) loops cannot leave the planner unable to ask the
// model anything at all.
func TestWithLoopsFloorsAtOne(t *testing.T) {
	for _, n := range []int{0, -3} {
		if got := New(&llm.Client{}, &agent.Agent{}).WithLoops(n).maxLoops; got != 1 {
			t.Errorf("WithLoops(%d) set %d, want the floor of 1", n, got)
		}
	}
	if got := New(&llm.Client{}, &agent.Agent{}).WithLoops(7).maxLoops; got != 7 {
		t.Errorf("a positive limit must be kept, got %d", got)
	}
}

// TestEmitToolCallWithNoTraceAndNoArguments: the tracer is a convenience for the
// interface, and the arguments are optional. Neither may cause a panic, because
// the planner is also used without any UI.
func TestEmitToolCallWithNoTraceAndNoArguments(t *testing.T) {
	emitToolCall(nil, "execute_command", json.RawMessage(`{"command":"ls"}`)) // must not panic

	// The tracer takes a format string and its arguments, exactly like the real
	// consumer (fmt.Fprintf), so the assertion formats it the same way.
	var seen string
	emitToolCall(func(format string, args ...any) {
		seen = fmt.Sprintf(format, args...)
	}, "list_directory", nil)
	if !strings.Contains(seen, "list_directory") {
		t.Errorf("a call with no arguments must still name the tool, got %q", seen)
	}

	// A call with parseable arguments names them, sorted so the line is stable.
	var withArgs string
	emitToolCall(func(format string, args ...any) {
		withArgs = fmt.Sprintf(format, args...)
	}, "execute_command", json.RawMessage(`{"z":"last","a":"first"}`))
	if !strings.Contains(withArgs, "a=first") || !strings.Contains(withArgs, "z=last") {
		t.Errorf("the arguments must be listed, got %q", withArgs)
	}
	if strings.Index(withArgs, "a=first") > strings.Index(withArgs, "z=last") {
		t.Errorf("the arguments must be sorted, got %q", withArgs)
	}

	// Arguments the planner does not understand are ignored rather than fatal.
	var again string
	emitToolCall(func(format string, args ...any) {
		again = fmt.Sprintf(format, args...)
	}, "read_file", json.RawMessage(`not json`))
	if !strings.Contains(again, "read_file") {
		t.Errorf("unparsable arguments must not stop the announcement, got %q", again)
	}
}

// TestToolArgumentsThatCannotBeParsed: every tool reports a bad argument list as a
// message the model can read, instead of returning an empty result it would
// mistake for "nothing found".
func TestToolArgumentsThatCannotBeParsed(t *testing.T) {
	p := &Planner{}
	bad := json.RawMessage(`{"path": 12}`) // a number where a string is expected

	if got := p.toolListDirectory(bad); !strings.Contains(got, "Error parsing the arguments") {
		t.Errorf("list_directory = %q", got)
	}
	if got := p.toolReadFile(bad); !strings.Contains(got, "Error parsing the arguments") {
		t.Errorf("read_file = %q", got)
	}
	if got := p.toolSearchInFiles(context.Background(), bad); !strings.Contains(got, "Error parsing the arguments") {
		t.Errorf("search_in_files = %q", got)
	}
	if got := p.toolExecuteCommand(context.Background(), json.RawMessage(`{"command": 12}`)); !strings.Contains(got, "Error parsing the arguments") {
		t.Errorf("execute_command = %q", got)
	}
}

// TestSearchInFilesUsesTheLiteralFlag: the literal search treats the pattern as a
// fixed string instead of a regular expression, which is what a user searching for
// a path with dots or brackets needs.
func TestSearchInFilesUsesTheLiteralFlag(t *testing.T) {
	a, ex := makeAgent(t, true)
	ex.defaultOutput = "match"
	srv := llmServer(t, nil)
	defer srv.Close()
	p := New(newClient(t, srv), a)

	got := p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"a.b","path":".","literal":"true"}`))
	if !strings.Contains(got, "grep -R -n -F") {
		t.Errorf("a literal search must use -F, got %q", got)
	}

	got = p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"a.b","path":"."}`))
	if !strings.Contains(got, "grep -R -n -E") {
		t.Errorf("the default search must use -E, got %q", got)
	}
}

// TestSearchInFilesReportsAMissingDirectory: a path that does not exist is a normal
// answer for this tool, not an error the model cannot act on.
func TestSearchInFilesReportsAMissingDirectory(t *testing.T) {
	a, ex := makeAgent(t, true)
	ex.defaultOutput = ""
	ex.script = map[string]struct {
		output string
		exit   int
		err    error
	}{}
	srv := llmServer(t, nil)
	defer srv.Close()
	p := New(newClient(t, srv), a)

	got := p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"x","path":"/does/not/exist"}`))
	if !strings.Contains(got, "exit=") {
		t.Errorf("the exit code must always be reported, got %q", got)
	}
}

// TestToolOutputAlwaysEndsWithANewline: the output is assembled for a model, and a
// block that runs into the exit marker is harder to read back. Both shapes are
// checked: output that already ends in a newline and output that does not.
func TestToolOutputAlwaysEndsWithANewline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
	}{
		{"without a trailing newline", "a line"},
		{"with a trailing newline", "a line\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, ex := makeAgent(t, true)
			ex.defaultOutput = tc.output
			srv := llmServer(t, nil)
			defer srv.Close()
			p := New(newClient(t, srv), a)

			got := p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"x","path":"."}`))
			if strings.Contains(got, "line[exit=") {
				t.Errorf("the output must be separated from the exit marker, got %q", got)
			}
			if !strings.Contains(got, "line\n[exit=0]") {
				t.Errorf("the exit marker must follow the output, got %q", got)
			}
		})
	}

	// A failing command reports the error above its output.
	a, ex := makeAgent(t, true)
	ex.defaultOutput = "partial"
	ex.script = map[string]struct {
		output string
		exit   int
		err    error
	}{}
	srvErr := llmServer(t, nil)
	defer srvErr.Close()
	p := New(newClient(t, srvErr), a)
	if got := p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"x","path":"."}`)); !strings.Contains(got, "partial") {
		t.Errorf("the output must survive, got %q", got)
	}
}

// fakeEngine is an Engine whose stream is scripted, so the planner's loop can be
// driven without a server. It is what makes the "producer closed without a done
// chunk" branch reachable.
type fakeEngine struct {
	chunks []llm.StreamChunk
	// noDone closes the channel after the chunks instead of sending StreamDone.
	noDone bool
}

func (f *fakeEngine) CompleteToolsStream(context.Context, []llm.Message, []llm.Tool) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk, len(f.chunks)+1)
	for _, c := range f.chunks {
		ch <- c
	}
	if !f.noDone {
		ch <- llm.StreamChunk{Event: llm.StreamDone}
	}
	close(ch)
	return ch
}

func (f *fakeEngine) Complete(context.Context, []llm.Message) (string, error) {
	return "forced answer", nil
}

// TestRunWithAProducerThatClosesWithoutADoneChunk: the loop accumulates whatever
// arrived and returns it, so a producer that breaks the contract cannot leave the
// planner without an answer.
func TestRunWithAProducerThatClosesWithoutADoneChunk(t *testing.T) {
	a, _ := makeAgent(t, true)
	engine := &fakeEngine{
		noDone: true,
		chunks: []llm.StreamChunk{{Event: llm.StreamText, Text: "partial answer"}},
	}
	p := New(engine, a)
	out, err := p.Run(context.Background(), "question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "partial answer" {
		t.Errorf("output = %q, want the accumulated text", out)
	}
}

// TestRunForcesAnAnswerAtTheLoopLimit: when the model keeps calling tools, the last
// iteration tells it to answer in plain text, and the closing Complete call is what
// produces the final answer the user reads.
func TestRunForcesAnAnswerAtTheLoopLimit(t *testing.T) {
	a, _ := makeAgent(t, true)
	call := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "list_directory", Arguments: json.RawMessage(`{"path":"."}`)}}
	engine := &fakeEngine{
		// Every turn asks for a tool, so the loop limit is what ends the run.
		chunks: []llm.StreamChunk{{Event: llm.StreamToolCall, Call: &call}},
	}
	p := New(engine, a).WithLoops(2)
	out, err := p.Run(context.Background(), "question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "forced answer" {
		t.Errorf("output = %q, want the forced final answer", out)
	}
}

// TestSearchInFilesDefaultsThePathAndReportsAFailure: an omitted path means the
// working directory, and a command that fails reports the error above whatever
// output it managed to produce.
func TestSearchInFilesDefaultsThePathAndReportsAFailure(t *testing.T) {
	t.Run("an omitted path searches the working directory", func(t *testing.T) {
		a, ex := makeAgent(t, true)
		ex.defaultOutput = "a match"
		srv := llmServer(t, nil)
		defer srv.Close()
		p := New(newClient(t, srv), a)

		got := p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"x"}`))
		if !strings.Contains(got, "grep -R -n -E") || !strings.Contains(got, "'.'") {
			t.Errorf("the path must default to the working directory, got %q", got)
		}
	})

	t.Run("a failing command reports the error", func(t *testing.T) {
		a, ex := makeAgent(t, true)
		ex.defaultOutput = "some output before the failure"
		ex.defaultErr = errors.New("the command was killed")
		srv := llmServer(t, nil)
		defer srv.Close()
		p := New(newClient(t, srv), a)

		got := p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"x","path":"."}`))
		if !strings.Contains(got, "Execution error: the command was killed") {
			t.Errorf("the failure must be reported, got %q", got)
		}
		if !strings.Contains(got, "some output before the failure") {
			t.Errorf("the partial output must be kept, got %q", got)
		}
	})
}

// TestRunReportsAnUnreachableEngine: an engine that cannot be reached surfaces as
// an error from the planner, so the interface reports it instead of an empty answer.
func TestRunReportsAnUnreachableEngine(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServerStatus(t, 500)
	defer srv.Close()

	p := New(newClient(t, srv), a)
	if _, err := p.Run(context.Background(), "question"); err == nil {
		t.Error("an unreachable engine must be reported")
	}
}

// TestUnknownToolIsReportedToTheModel: a hallucinated tool name is answered with a
// message naming it, so the model can correct itself on the next step.
func TestUnknownToolIsReportedToTheModel(t *testing.T) {
	p := &Planner{}
	got := p.runTool(context.Background(), llm.ToolCall{
		Function: llm.FunctionCall{Name: "does_not_exist"},
	})
	if !strings.Contains(got, "does_not_exist") {
		t.Errorf("the answer must name the unknown tool, got %q", got)
	}
}

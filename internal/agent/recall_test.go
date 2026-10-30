package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/memory"
)

// analysisServer answers every request with a readable analysis and keeps the prompts it was
// asked, so a test can see what the model was actually told.
func analysisServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the prompt: %v", err)
		}
		prompts = append(prompts, string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{
				"content": `{"understandable":true,"summary":"the payroll","needs_subtasks":false}`,
			}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &prompts
}

// TestAnalysisRecallsEarlierTurns: what the archive returns goes into the analysis prompt, and
// the user is told how much was brought, because an answer built on an old turn has to say so.
func TestAnalysisRecallsEarlierTurns(t *testing.T) {
	srv, prompts := analysisServer(t)
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	var trace []string
	e.agent.SetProgress(func(format string, args ...any) {
		trace = append(trace, fmt.Sprintf(format, args...))
	})
	var asked string
	var live int
	e.agent.SetRecall(func(_ context.Context, query string, shown int) (string, int) {
		asked = query
		live = shown
		return "EARLIER-TURN-BLOCK", 3
	})
	// Two turns in the conversation: the second is this task's own, still in progress.
	e.agent.SetTranscript([]DialogueTurn{
		{Kind: KindChat, User: "the first question", Agent: "the first answer"},
		{Kind: KindTask, User: "the task in progress", Agent: pendingText, Pending: true},
	})

	question := "and what did we decide about the payroll?"
	analysis := e.agent.analysisPhase(context.Background(), textTask(question), "", 0)
	if !analysis.Understandable {
		t.Fatalf("analysis = %+v", analysis)
	}
	if asked != question {
		t.Errorf("the recall was asked %q, want the request %q", asked, question)
	}
	// At the top level the turn in progress is already in the prompt, so it is one fewer than
	// the number of turns the conversation holds.
	if live != 1 {
		t.Errorf("live = %d, want 1", live)
	}
	if len(*prompts) != 1 || !strings.Contains((*prompts)[0], "EARLIER-TURN-BLOCK") {
		t.Errorf("the recalled block is not in the prompt:\n%s", strings.Join(*prompts, "\n---\n"))
	}
	if !strings.Contains(strings.Join(trace, "\n"), "[recalled 3 passages from earlier]") {
		t.Errorf("the recall was not traced: %v", trace)
	}
}

// TestAnalysisTellsTheRecallHowManyTurnsAreShown: the count is the turns the prompt really
// carries, capped at DialogueTurns, and one fewer at the top level, where the turn in progress
// is already in the prompt. A short conversation must not hide archived turns the prompt never
// showed.
func TestAnalysisTellsTheRecallHowManyTurnsAreShown(t *testing.T) {
	cases := []struct {
		name  string
		turns int
		depth int
		want  int
	}{
		{"nothing said yet", 0, 0, 0},
		{"the turn in progress", 2, 0, 1},
		{"a subtask of a short conversation", 2, 1, 2},
		{"a subtask of a long one", 20, 1, DialogueTurns},
		{"the top level of a long one", 20, 0, DialogueTurns - 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := analysisServer(t)
			e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
			turns := make([]DialogueTurn, tc.turns)
			for i := range turns {
				turns[i] = DialogueTurn{Kind: KindChat, User: "a question", Agent: "an answer"}
			}
			e.agent.SetTranscript(turns)

			var got int
			e.agent.SetRecall(func(_ context.Context, _ string, live int) (string, int) {
				got = live
				return "", 0
			})
			e.agent.analysisPhase(context.Background(), textTask("the task"), "", tc.depth)
			if got != tc.want {
				t.Errorf("live = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestAnalysisWithoutRecallLeavesTheBlockEmpty: a task piped in from a script has no
// conversation, and one whose archive found nothing must not invent a block or a trace line.
func TestAnalysisWithoutRecallLeavesTheBlockEmpty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		recall func(context.Context, string, int) (string, int)
	}{
		{"no hook", nil},
		{"nothing found", func(context.Context, string, int) (string, int) { return "", 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, prompts := analysisServer(t)
			e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

			var trace []string
			e.agent.SetProgress(func(format string, args ...any) {
				trace = append(trace, fmt.Sprintf(format, args...))
			})
			if tc.recall != nil {
				e.agent.SetRecall(tc.recall)
			}

			analysis := e.agent.analysisPhase(context.Background(), textTask("do the test task"), "", 0)
			if !analysis.Understandable {
				t.Fatalf("analysis = %+v", analysis)
			}
			if strings.Contains(strings.Join(trace, "\n"), "recalled") {
				t.Errorf("a recall with nothing to say was traced: %v", trace)
			}
			if len(*prompts) == 0 {
				t.Fatal("no prompt was sent")
			}
		})
	}
}

// TestAnalysisBringsWhatTheMemoryKnows: the task starts with the memory's passages in its
// prompt, next to any earlier turn, and the user is told.
func TestAnalysisBringsWhatTheMemoryKnows(t *testing.T) {
	srv, prompts := analysisServer(t)
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	mem := memory.Open(t.TempDir())
	if r := mem.WriteText("Payroll", "Payroll closes on the 25th."); strings.HasPrefix(r, "Error") {
		t.Fatal(r)
	}
	e.agent.SetMemory(mem)
	e.agent.SetRecall(func(context.Context, string, int) (string, int) { return "EARLIER-TURN-BLOCK", 1 })
	var trace []string
	e.agent.SetProgress(func(format string, args ...any) { trace = append(trace, fmt.Sprintf(format, args...)) })

	e.agent.analysisPhase(context.Background(), textTask("when does payroll close?"), "", 0)
	if p := strings.Join(*prompts, ""); !strings.Contains(p, "[[Payroll]]") || !strings.Contains(p, "EARLIER-TURN-BLOCK") {
		t.Errorf("the prompt lacks the memory or the earlier turn:\n%s", p)
	}
	if !strings.Contains(strings.Join(trace, "\n"), "[recalled 1 passages from memory]") {
		t.Errorf("trace = %v", trace)
	}
}

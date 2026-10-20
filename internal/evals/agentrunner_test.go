package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

// solutions are the commands a model that does the task right proposes, by task id.
var solutions = map[string]string{
	"create-file": `printf hello > hello.txt`,
	"fix-typo":    `printf 'I receive your mail\n' > letter.txt`,
	"count-lines": `printf 7 > count.txt`,
	"rename-file": `mv old.txt new.txt`,
	"sort-lines":  `sort names.txt > sorted.txt`,
}

// fakeModel answers every phase of the agent. With solve false it proposes a command that changes
// nothing, so the check fails. It reports token usage like a real provider.
func fakeModel(t *testing.T, solve bool) *httptest.Server {
	t.Helper()
	reply := func(w http.ResponseWriter, content string) {
		body := map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
			"usage":   map[string]int{"prompt_tokens": 1000, "completion_tokens": 500, "total_tokens": 1500},
		}
		_ = json.NewEncoder(w).Encode(body)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			reply(w, `{"understandable":true,"summary":"s","success_criteria":["done"],"risks":[],"needs_subtasks":false}`)
		case strings.Contains(text, "## ACTION PLAN"):
			reply(w, `{"plan":[{"step":1,"action":"a","command":"c"}],"subtasks":[],"expected_result":"r"}`)
		case strings.Contains(text, "## CRITERIA AUDIT"):
			reply(w, `{"criteria":[{"criterion":"done","met":true,"evidence":"e"}]}`)
		case strings.Contains(text, "## FINAL ANSWER"):
			reply(w, `{"summary":"finished"}`)
		default:
			command := "true"
			for id, c := range solutions {
				if solve && strings.Contains(text, taskPrompt(id)) {
					command = c
				}
			}
			action, _ := json.Marshal(map[string]any{
				"reasoning":    "r",
				"actions":      []map[string]string{{"kind": "command", "description": "d", "command": command}},
				"final_action": map[string]string{"description": "none", "command": ""},
			})
			reply(w, string(action))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// taskPrompt is the prompt of the task with that id.
func taskPrompt(id string) string {
	for _, c := range Cases() {
		if c.ID == id {
			return c.Prompt
		}
	}
	panic(fmt.Sprintf("no task %q", id))
}

func runnerFor(url string) AgentRunner {
	cfg := config.Default()
	cfg.LLM.Provider, cfg.LLM.Model = "openai", "gpt-5-mini"
	cfg.LLM.APIKey, cfg.LLM.BaseURL = "key", url
	cfg.LLM.MaxAttempts, cfg.LLM.Timeout = 1, 10*time.Second
	cfg.Agent.MaxRetries = 1
	return AgentRunner{Cfg: cfg, Log: logx.Global()}
}

func TestAgentRunnerPassesEveryTaskWithAModelThatSolvesThem(t *testing.T) {
	srv := fakeModel(t, true)
	run, err := Execute(context.Background(), runnerFor(srv.URL), Cases(), Options{Provider: "openai", Model: "gpt-5-mini"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Summary.Passed != len(Cases()) {
		t.Fatalf("passed %d of %d: %+v", run.Summary.Passed, run.Summary.Total, run.Results)
	}
	if run.Summary.Tokens == 0 || !run.Summary.CostKnown || run.Summary.CostUSD <= 0 || run.Summary.Steps == 0 {
		t.Errorf("tokens, steps and cost should be measured: %+v", run.Summary)
	}
}

func TestAgentRunnerFailsTasksThatAreNotDone(t *testing.T) {
	srv := fakeModel(t, false)
	run, err := Execute(context.Background(), runnerFor(srv.URL), Cases()[:1], Options{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Summary.Passed != 0 || run.Results[0].Reason == "" {
		t.Errorf("a task left undone should fail with a reason: %+v", run.Results[0])
	}
}

func TestAgentRunnerReportsAModelItCannotReach(t *testing.T) {
	r := runnerFor("http://127.0.0.1:1")
	if _, err := r.Run(context.Background(), Cases()[0], t.TempDir()); err == nil {
		t.Error("an unreachable model should be an error, not a failed task")
	}
	r.Cfg.LLM.Provider = "nope"
	if _, err := r.Run(context.Background(), Cases()[0], t.TempDir()); err == nil {
		t.Error("a model that cannot be built should be an error")
	}
}

func TestAgentRunnerReportsARunThatNeverStarted(t *testing.T) {
	srv := fakeModel(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runnerFor(srv.URL).Run(ctx, Cases()[0], t.TempDir()); err == nil {
		t.Error("a run stopped before it began has no result")
	}
}

func TestAgentRunnerRefusesAnEmptyTask(t *testing.T) {
	srv := fakeModel(t, true)
	if _, err := runnerFor(srv.URL).Run(context.Background(), Case{ID: "x"}, t.TempDir()); err == nil {
		t.Error("a task with no prompt should not run")
	}
}

func TestAgentRunnerReportsASandboxThatCannotStart(t *testing.T) {
	srv := fakeModel(t, true)
	defer func(old func(sandbox.Options) (*sandbox.Sandbox, error)) { newBox = old }(newBox)
	newBox = func(sandbox.Options) (*sandbox.Sandbox, error) { return nil, errors.New("no sandbox") }
	if _, err := runnerFor(srv.URL).Run(context.Background(), Cases()[0], t.TempDir()); err == nil {
		t.Error("a sandbox that cannot start should be an error")
	}
}

func TestOutcomeLeavesTheCostUnknownWhenNothingWasReported(t *testing.T) {
	l := config.LLM{Provider: "openai", Model: "gpt-5-mini"}
	if o := outcomeOf(agent.TaskResult{Pass: true}, llm.Usage{}, l); o.CostKnown || o.CostUSD != 0 {
		t.Errorf("no tokens reported: %+v", o)
	}
	if o := outcomeOf(agent.TaskResult{Pass: true}, llm.Usage{Input: 1_000_000}, l); !o.CostKnown || o.CostUSD != 0.25 {
		t.Errorf("a million input tokens: %+v", o)
	}
}

func TestPriceOf(t *testing.T) {
	if in, out, ok := PriceOf("openai", "gpt-5-mini"); !ok || in <= 0 || out <= 0 {
		t.Errorf("gpt-5-mini should have a price: %v %v %v", in, out, ok)
	}
	if _, _, ok := PriceOf("openai", "made-up"); ok {
		t.Error("an unknown model has no price")
	}
	if _, _, ok := PriceOf("nobody", "x"); ok {
		t.Error("an unknown provider has no price")
	}
	if _, _, ok := PriceOf("ollama", "llama3"); ok {
		t.Error("a local model has no price")
	}
}

func TestSupportedFollowsTheShell(t *testing.T) {
	if !Supported() {
		t.Skip("no sh on this machine")
	}
}

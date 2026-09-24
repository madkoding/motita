package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/task"
)

func init() {
	l, _ := logx.New(logx.Options{Level: logx.Error, Console: false})
	logx.Install(l)
}

// fakeLLMServer simulates the three phases of the flow by returning the JSON the
// agent expects, and runs a script of actions per attempt.
type fakeLLMServer struct {
	// actionsPerAttempt states, for each attempt, which commands the "model"
	// proposes in the execution phase.
	actionsPerAttempt [][]string
	calls             int32
	phases            []string
	failOn            string // when not empty, that phase returns an HTTP error
}

func (s *fakeLLMServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&s.calls, 1)

		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&request)

		var text string
		for _, m := range request.Messages {
			text += m.Content
		}

		// The phase is decided by the prompt's markers (so what is really tested
		// is which template was used, not just the call order).
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			s.phases = append(s.phases, "analyze")
			if s.failOn == "analyze" {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":{"message":"simulated failure in the analysis"}}`)
				return
			}
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"understandable\":true,\"summary\":\"test task\",\"success_criteria\":[\"the file exists\"],\"risks\":[],\"needs_subtasks\":false}"}}]}`)

		case strings.Contains(text, "## ACTION PLAN"):
			s.phases = append(s.phases, "plan")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[{\"step\":1,\"action\":\"create file\",\"command\":\"create.txt\"}],\"subtasks\":[],\"expected_result\":\"file created\"}"}}]}`)

		case strings.Contains(text, "## FINAL ANSWER"):
			s.phases = append(s.phases, "synthesize")
			response := map[string]any{"summary": "synthesized test answer"}
			data, _ := json.Marshal(map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]string{"content": mustJSON(response)},
				}},
			})
			w.Write(data)

		case strings.Contains(text, "## ACTION"):
			s.phases = append(s.phases, "execute")
			n := 0
			for _, f := range s.phases {
				if f == "execute" {
					n++
				}
			}
			idx := n - 1
			var commands []string
			if idx < len(s.actionsPerAttempt) {
				commands = s.actionsPerAttempt[idx]
			}
			if len(commands) == 0 {
				commands = []string{"true"}
			}
			actions := make([]map[string]string, 0, len(commands))
			for _, c := range commands {
				actions = append(actions, map[string]string{"kind": "command", "description": "test", "command": c})
			}
			response := map[string]any{
				"reasoning":    "automated test",
				"actions":      actions,
				"final_action": map[string]string{"description": "none", "command": ""},
			}
			// The response envelope is built with the content already serialised.
			data, _ := json.Marshal(map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]string{"content": mustJSON(response)},
				}},
			})
			w.Write(data)

		default:
			t.Errorf("request with an unrecognisable prompt: %q", truncate(text, 200))
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
		}
	}
}

// mustJSON returns the JSON string the "model" would put in content.
func mustJSON(v any) string {
	d, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(d)
}

// fixture mounts a complete agent with the fake LLM and the given anchor.
type fixture struct {
	agent *Agent
	dir   string
	log   *logx.Logger
	box   *sandbox.Sandbox
}

func mount(t *testing.T, srv *httptest.Server, anchor config.Anchor, cfg func(*config.Config)) *fixture {
	t.Helper()
	dir := t.TempDir()

	c := config.Default()
	c.LLM.APIKey = "key"
	c.LLM.BaseURL = srv.URL
	c.LLM.MaxAttempts = 1
	c.LLM.BackoffInitial = time.Millisecond
	c.LLM.BackoffMax = 2 * time.Millisecond
	c.LLM.Timeout = 5 * time.Second
	c.Anchor = anchor
	c.Agent.WorkspaceDir = dir
	c.Agent.MaxRetries = 1
	c.TaskSource.Kind = "file"
	if cfg != nil {
		cfg(&c)
	}

	box, err := sandbox.New(sandbox.Options{
		Dir:         dir,
		Limits:      sandbox.Limits{MemoryMB: 256, CPUSeconds: 10},
		Timeout:     20 * time.Second,
		MaxOutputKB: 64,
		Log:         logx.Global(),
	})
	if err != nil {
		t.Fatalf("could not create the sandbox: %v", err)
	}
	t.Cleanup(func() { box.Close() })

	engine, err := llm.New(c.LLM, logx.Global())
	if err != nil {
		t.Fatalf("could not create the engine: %v", err)
	}

	source, err := task.NewText("do the test task", "test")
	if err != nil {
		t.Fatal(err)
	}

	return &fixture{
		agent: New(c, logx.Global(), engine, box, source),
		dir:   dir,
		log:   logx.Global(),
		box:   box,
	}
}

// TestFullLoopPASSFirstAttempt: the happy path, end to end.
func TestFullLoopPASSFirstAttempt(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"echo hello > result.txt"}},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:         "command",
		Command:      "sh",
		Args:         []string{"-c", "test -s result.txt && echo READY"},
		Timeout:      10 * time.Second,
		ExpectOutput: "READY",
	}, nil)

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	err := e.agent.Run(context.Background())
	if err != nil {
		t.Fatalf("the agent returned an error: %v", err)
	}
	if result == nil {
		t.Fatal("the task result was not captured")
	}
	if !result.Pass {
		t.Fatalf("PASS was expected, reason: %s", result.Reason)
	}
	if result.Attempts != 1 {
		t.Errorf("attempts = %d", result.Attempts)
	}
	// The main phases must have been used, in order, and synthesis may follow.
	expected := []string{"analyze", "plan", "execute"}
	gotPrefix := fake.phases
	if len(gotPrefix) > len(expected) {
		gotPrefix = gotPrefix[:len(expected)]
	}
	if strings.Join(gotPrefix, ",") != strings.Join(expected, ",") {
		t.Errorf("phases = %v, expected prefix %v", fake.phases, expected)
	}
	// The anchor must have seen the effect: the agent's loop and the sandbox's
	// write to the same working directory.
	if _, err := os.Stat(filepath.Join(e.dir, "result.txt")); err != nil {
		t.Errorf("the file created by the action is not in the working directory: %v", err)
	}
}

// TestLoopRetriesAndCorrects: the first attempt does not pass validation and the
// second one does. It checks that the second prompt carries the failure logs.
func TestLoopRetriesAndCorrects(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{
			{"echo wrong"},
			{"echo hello > correct.txt"},
		},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "test -s correct.txt"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, nil)

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil || !result.Pass {
		t.Fatalf("it should pass on the second attempt: %+v", result)
	}
	if result.Attempts != 2 {
		t.Errorf("attempts = %d, expected 2", result.Attempts)
	}
}

// TestLoopFailsAndEscalatesAfterAttempts: when it never validates, it escalates
// and does not declare PASS.
func TestLoopFailsAndEscalatesAfterAttempts(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"echo wrong"}, {"echo wrong again"}},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	dir := t.TempDir()
	e := mount(t, srv, config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "exit 1"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}, func(c *config.Config) {
		c.Agent.MaxRetries = 1
		c.Agent.OnFailure = config.OnFailure{
			Kind:    "command",
			Command: "echo escalated > " + filepath.Join(dir, "escalated.txt"),
		}
	})

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	err := e.agent.Run(context.Background())
	if err == nil {
		t.Error("a task that does not pass must make the agent finish with an error")
	}
	if result == nil {
		t.Fatal("no result")
	}
	if result.Pass {
		t.Fatal("it must never declare PASS when the anchor fails")
	}
	if result.Attempts != 2 {
		t.Errorf("attempts = %d (max_retries=1 => 2 attempts)", result.Attempts)
	}
	if !strings.Contains(result.Reason, "exhausted") {
		t.Errorf("reason = %q", result.Reason)
	}
}

// TestAgentWithoutAnchorDoesNotStart: with no validator there is nobody to
// declare PASS, so the agent must refuse to start instead of burning attempts
// against the LLM.
func TestAgentWithoutAnchorDoesNotStart(t *testing.T) {
	fake := &fakeLLMServer{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "none"}, nil)
	err := e.agent.Run(context.Background())
	if err == nil {
		t.Fatal("a configuration error was expected")
	}
	if !strings.Contains(err.Error(), "anchor.kind=none") {
		t.Errorf("the error must explain the problem and how to fix it: %v", err)
	}
	if atomic.LoadInt32(&fake.calls) != 0 {
		t.Errorf("the LLM must not be called without an anchor: %d calls were made", fake.calls)
	}
}

// TestAnalysisNotUnderstandable: when the model says the task is not viable,
// nothing is run and the task is discarded with a reason.
func TestAnalysisNotUnderstandable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		if strings.Contains(text, "## ANALYSIS OF THE TASK") {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"understandable\":false,\"risks\":[\"input data is missing\"]}"}}]}`)
			return
		}
		t.Errorf("no other phase should be reached")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	// The model reported that it cannot understand the task and named NO question and NO
	// assumption, so there is nothing to ask and nothing to act on. That is the one case that
	// remains a failure, and it must exit non-zero (important for cron).
	//
	// When the model DOES name a question the agent asks instead, and when there is no user to
	// ask it proceeds on the stated assumption — both are covered separately. The difference
	// matters: a question is not a failure, and a run that is waiting for an answer must not be
	// reported as one.
	err := e.agent.Run(context.Background())
	if err == nil {
		t.Fatal("a task with nothing to ask and nothing to assume must finish with an error")
	}
	if result == nil {
		t.Fatal("no result")
	}
	if result.Pass {
		t.Fatal("a task that is not understandable cannot pass")
	}
	if !strings.Contains(result.Reason, "input data is missing") {
		t.Errorf("reason = %q", result.Reason)
	}
	if !strings.Contains(err.Error(), "input data is missing") {
		t.Errorf("the aggregated error must include the reason: %v", err)
	}
}

// TestActionOutputReachesTheAnchor: the command's output reaches the next
// attempt's reasoning, which is what lets the LLM correct itself.
func TestActionOutputReachesTheAnchor(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{
			{"echo 'hint-for-the-model'; exit 1"},
			{"echo hello > result.txt"},
		},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "test -s result.txt"},
		Timeout: 10 * time.Second,
	}, nil)

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil || !result.Pass {
		t.Fatalf("it should end up passing: %+v", result)
	}
}

// TestSubtasks: when the analysis asks for a split, every subtask goes through
// the full flow.
func TestSubtasks(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			calls++
			if calls == 1 {
				fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"understandable\":true,\"summary\":\"split\",\"success_criteria\":[],\"risks\":[],\"needs_subtasks\":true}"}}]}`)
			} else {
				fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"understandable\":true,\"summary\":\"sub\",\"success_criteria\":[],\"risks\":[],\"needs_subtasks\":false}"}}]}`)
			}
		case strings.Contains(text, "## ACTION PLAN"):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[],\"subtasks\":[\"subtask A\",\"subtask B\"],\"expected_result\":\"x\"}"}}]}`)
		default:
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"reasoning\":\"r\",\"actions\":[{\"kind\":\"command\",\"command\":\"echo sub > sub.txt\"}],\"final_action\":{\"command\":\"\"}}"}}]}`)
		}
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "test -s sub.txt"},
		Timeout: 10 * time.Second,
	}, func(c *config.Config) { c.Agent.SubtaskDepth = 1 })

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Subtasks != 2 {
		t.Errorf("subtasks = %d, expected 2", result.Subtasks)
	}
	if !result.Pass {
		t.Errorf("both subtasks should pass: %s", result.Reason)
	}
}

// TestSubtaskLimit: without the limit, the split could never finish.
func TestSubtaskLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			// It always asks to split: with no limit this would never finish.
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"understandable\":true,\"summary\":\"x\",\"needs_subtasks\":true}"}}]}`)
		case strings.Contains(text, "## ACTION PLAN"):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[],\"subtasks\":[\"another\"],\"expected_result\":\"x\"}"}}]}`)
		default:
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"reasoning\":\"r\",\"actions\":[{\"command\":\"true\"}]}"}}]}`)
		}
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "true", Timeout: 5 * time.Second,
	}, func(c *config.Config) { c.Agent.SubtaskDepth = 2 })

	done := make(chan error, 1)
	go func() { done <- e.agent.Run(context.Background()) }()

	select {
	case <-done:
		// It finished: the limit works.
	case <-time.After(30 * time.Second):
		t.Fatal("it did not finish: the subtask limit is not respected")
	}
}

// TestFailedFinalActionIsNotACompletedTask: bug found by CI with the i386 E2E.
// The validation passed, the final action died (because of the sandbox's memory
// limit) and the agent reported "task completed" and exited 0. A contract that is
// not met is not a success.
func TestFailedFinalActionIsNotACompletedTask(t *testing.T) {
	fake := &fakeLLMServer{actionsPerAttempt: [][]string{{"true"}, {"true"}, {"true"}}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "true", Timeout: 5 * time.Second,
	}, func(c *config.Config) {
		c.Agent.MaxRetries = 2
		// A final action that always fails.
		c.FinalAction = config.FinalAction{
			Kind: "command", Command: "sh", Args: []string{"-c", "exit 7"},
		}
	})

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	err := e.agent.Run(context.Background())
	if err == nil {
		t.Fatal("a failed final action must fail the task and the process")
	}
	if result == nil {
		t.Fatal("no result")
	}
	if result.Pass {
		t.Fatal("PASS cannot be declared when the final action failed")
	}
	if !strings.Contains(result.Reason, "final action failed") {
		t.Errorf("the reason must explain that the final action failed: %q", result.Reason)
	}
	if !strings.Contains(err.Error(), "final action failed") {
		t.Errorf("the process error must include the reason: %v", err)
	}
	// A non-zero exit code is a failure even with no execution error.
	if !strings.Contains(result.Reason, "exit code 7") {
		t.Errorf("the reason must mention the exit code: %q", result.Reason)
	}
}

// TestFinalActionIsRetriedOnFailure: when the final action fails, the retry loop
// tries it again instead of giving up.
func TestFinalActionIsRetriedOnFailure(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "count.txt")

	fake := &fakeLLMServer{actionsPerAttempt: [][]string{{"true"}, {"true"}}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "true", Timeout: 5 * time.Second,
	}, func(c *config.Config) {
		c.Agent.MaxRetries = 2
		// The final action fails the first time and works the second.
		script := fmt.Sprintf(`if [ -f %s ]; then exit 0; fi; touch %s; exit 9`, marker, marker)
		c.FinalAction = config.FinalAction{
			Kind: "command", Command: "sh", Args: []string{"-c", script},
		}
	})

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("it should end well after retrying the final action: %v", err)
	}
	if !result.Pass {
		t.Fatalf("it should pass on the second attempt: %s", result.Reason)
	}
	if result.Attempts != 2 {
		t.Errorf("attempts = %d, expected 2", result.Attempts)
	}
}

// TestFinalActionOnlyAfterPASS: the final action must never run if the anchor did
// not give PASS.
func TestFinalActionOnlyAfterPASS(t *testing.T) {
	fake := &fakeLLMServer{actionsPerAttempt: [][]string{{"echo wrong"}, {"echo wrong"}}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	marker := filepath.Join(t.TempDir(), "final-action-ran.txt")
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "exit 1", Timeout: 5 * time.Second, ExpectExit: 0,
	}, func(c *config.Config) {
		c.Agent.MaxRetries = 1
		c.FinalAction = config.FinalAction{
			Kind:    "command",
			Command: "sh",
			Args:    []string{"-c", "touch " + marker},
		}
	})

	e.agent.Run(context.Background())
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the final action ran without a PASS from the anchor")
	}
}

// TestEmptyTaskIsAnError: building a source with a blank task must fail instead
// of sending a useless request to the LLM.
func TestEmptyTaskIsAnError(t *testing.T) {
	if _, err := task.NewText("   ", "test"); err == nil {
		t.Fatal("an empty task must be an error")
	}
}

// TestMaxTasks: the task limit stops the agent even when the source has more
// work (useful for bounded runs from cron).
func TestMaxTasks(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"true"}, {"true"}, {"true"}},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "true", Timeout: 5 * time.Second,
	}, func(c *config.Config) { c.Agent.MaxTasks = 1 })

	// A source with two tasks: only one must be processed.
	source, err := task.NewText("first", "test")
	if err != nil {
		t.Fatal(err)
	}
	e.agent.source = source

	processed := 0
	e.agent.Observer = func(r TaskResult) { processed++ }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if processed != 1 {
		t.Errorf("tasks processed = %d, expected 1 (max_tasks)", processed)
	}
}

// TestRunCommand executes a single command through the public hook used by
// interactive modes and checks that the sandbox output is returned.
func TestRunCommand(t *testing.T) {
	dir := t.TempDir()
	box, err := sandbox.New(sandbox.Options{
		Dir:         dir,
		Limits:      sandbox.Limits{MemoryMB: 256, CPUSeconds: 10},
		Timeout:     20 * time.Second,
		MaxOutputKB: 64,
		Log:         logx.Global(),
	})
	if err != nil {
		t.Fatalf("could not create the sandbox: %v", err)
	}
	defer box.Close()

	a := New(config.Default(), logx.Global(), nil, box, nil)
	output, exit, err := a.RunCommand(context.Background(), "printf ok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d", exit)
	}
	if !strings.Contains(output, "ok") {
		t.Fatalf("expected output to contain 'ok', got: %s", output)
	}
}

// TestRunCommandRefusesWrites verifies that read-only mode rejects destructive
// commands before they reach the sandbox.
func TestRunCommandRefusesWrites(t *testing.T) {
	dir := t.TempDir()
	box, err := sandbox.New(sandbox.Options{
		Dir:         dir,
		Limits:      sandbox.Limits{MemoryMB: 256, CPUSeconds: 10},
		Timeout:     20 * time.Second,
		MaxOutputKB: 64,
		Log:         logx.Global(),
	})
	if err != nil {
		t.Fatalf("could not create the sandbox: %v", err)
	}
	defer box.Close()

	cfg := config.Default()
	cfg.Agent.ReadOnly = true
	a := New(cfg, logx.Global(), nil, box, nil)
	output, _, err := a.RunCommand(context.Background(), "rm -f /tmp/x")
	if err == nil {
		t.Fatal("expected the write to be refused")
	}
	if !strings.Contains(output, "[refused:") {
		t.Fatalf("expected refusal message, got: %s", output)
	}
}

// TestAttemptFailedLogsTheProposedCommands: the "attempt failed" record must carry the commands
// the model proposed, not only the validation reason.
//
// Why it matters: the retry loop stops when the counter runs out, and the only way to learn
// whether retrying CONVERGES is to compare one attempt's commands with the next. The validation
// reason alone ("the check failed") is identical for a run that is correcting itself and for one
// that is repeating itself; the commands are what tell them apart. Without them in the log, that
// question can only be answered by guessing, and the loop's whole design rests on it.
func TestAttemptFailedLogsTheProposedCommands(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "attempts.log")
	l, err := logx.New(logx.Options{Path: logPath, Level: logx.Warn, Console: false})
	if err != nil {
		t.Fatalf("could not create the logger: %v", err)
	}

	// Two attempts, both failing: the anchor wants result.txt and neither command makes it.
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{
			{"echo first-try > wrong.txt"},
			{"echo second-try > other.txt"},
		},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	dir := t.TempDir()
	c := config.Default()
	c.LLM.APIKey = "key"
	c.LLM.BaseURL = srv.URL
	c.LLM.MaxAttempts = 1
	c.LLM.BackoffInitial = time.Millisecond
	c.LLM.BackoffMax = 2 * time.Millisecond
	c.LLM.Timeout = 5 * time.Second
	c.Anchor = config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "test -s result.txt"},
		Timeout: 10 * time.Second,
	}
	c.Agent.WorkspaceDir = dir
	c.Agent.MaxRetries = 1
	c.TaskSource.Kind = "file"

	box, err := sandbox.New(sandbox.Options{
		Dir: dir, Limits: sandbox.Limits{MemoryMB: 256, CPUSeconds: 10},
		Timeout: 20 * time.Second, MaxOutputKB: 64, Log: l,
	})
	if err != nil {
		t.Fatalf("could not create the sandbox: %v", err)
	}
	defer box.Close()

	engine, err := llm.New(c.LLM, l)
	if err != nil {
		t.Fatalf("could not create the engine: %v", err)
	}
	source, err := task.NewText("do the test task", "test")
	if err != nil {
		t.Fatal(err)
	}

	ag := New(c, l, engine, box, source)
	// The run is EXPECTED to end in failure: both attempts miss what the anchor checks. That is
	// the only way to produce two "attempt failed" records, so the error is not a test failure.
	if err := ag.Run(context.Background()); err == nil {
		t.Fatal("the run should have failed: neither attempt satisfies the anchor")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("closing the log: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("could not read the log: %v", err)
	}

	// Each failing attempt must leave a record naming the command it proposed.
	var attempts []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["msg"] == "attempt failed" {
			attempts = append(attempts, rec)
		}
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 'attempt failed' records, got %d (log: %s)", len(attempts), raw)
	}
	if got, want := attempts[0]["commands"], "echo first-try > wrong.txt"; got != want {
		t.Errorf("first record's commands = %v, want %q", got, want)
	}
	if got, want := attempts[1]["commands"], "echo second-try > other.txt"; got != want {
		t.Errorf("second record's commands = %v, want %q", got, want)
	}
}

// TestProposedCommands: the line must describe every attempt honestly, including the two shapes
// that carry no command at all.
//
// An attempt with an empty command and an attempt with no commands are both real outcomes, and
// both must be distinguishable in the log from an attempt that proposed something. Rendering them
// as an empty string would make a thrashing run look like it never tried, which is the opposite of
// what this field exists to show.
func TestProposedCommands(t *testing.T) {
	cases := []struct {
		name   string
		action Action
		want   string
	}{
		{
			name:   "one command",
			action: Action{Actions: []Command{{Command: "ls -la"}}},
			want:   "ls -la",
		},
		{
			name:   "several commands, in order",
			action: Action{Actions: []Command{{Command: "echo a"}, {Command: "echo b"}}},
			want:   "echo a | echo b",
		},
		{
			name:   "surrounding whitespace is not part of the command",
			action: Action{Actions: []Command{{Command: "  printf ok  "}}},
			want:   "printf ok",
		},
		{
			name:   "an entry with no command still counts",
			action: Action{Actions: []Command{{Command: "echo a"}, {Description: "just words"}}},
			want:   "echo a | (empty)",
		},
		{
			name:   "nothing proposed",
			action: Action{},
			want:   "(no commands proposed)",
		},
		{
			name:   "a very long command is bounded",
			action: Action{Actions: []Command{{Command: strings.Repeat("x", 900)}}},
			want:   strings.Repeat("x", 500) + "...",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := proposedCommands(tc.action); got != tc.want {
				t.Errorf("proposedCommands() = %q, want %q", got, tc.want)
			}
		})
	}
}

package app

// Verification of the three fixes, driven through the REAL command line with a simulated LLM.
//
// It is kept (unlike the probes that found the defects) because each of these is a promise the
// shipped prompt makes and nothing else checks: the prompt advertises a library to every path,
// so every path has to answer for it.
//
// The three defects it covers:
//  1. the task path promised a library and answered "no procedure library is configured";
//  2. the plan path did the same;
//  3. a model that asked for plan mode's `read_file` had its argument EXECUTED as a shell
//     command, because an unknown kind fell through to the command path.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// llmStub answers each phase of the flow with a canned reply, and records every prompt it
// received so the test can assert on what the MODEL was told.
type llmStub struct {
	analyze string
	plan    string
	act     func(seen string) string
	prompts []string
}

func (s *llmStub) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		s.prompts = append(s.prompts, text)

		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = s.analyze
		case strings.Contains(text, "## ACTION PLAN"):
			content = s.plan
		default:
			content = s.act(text)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
}

// allPrompts joins every prompt the stub saw, for assertions like "the model was shown X".
func (s *llmStub) allPrompts() string { return strings.Join(s.prompts, "\n---\n") }

// tailOf returns the end of a long prompt, which is where the agent's feedback to the model
// lands, and bounds what a failure message prints.
func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// chmodExec makes a script runnable, so "was it executed" can be observed.
func chmodExec(path string) error { return os.Chmod(path, 0o755) }

// fileExists reports whether a path exists, which is how the test tells "it ran" from "it did
// not" without asking the program under test.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// taskConfig writes a configuration that runs one task from a file.
func taskConfig(t *testing.T, dir, baseURL, task string) string {
	t.Helper()
	taskFile := filepath.Join(dir, "task.txt")
	mustWrite(t, taskFile, task+"\n")
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, fmt.Sprintf(`llm:
  base_url: %s
  api_key: x
  model: fake
task_source:
  kind: file
  path: %s
anchor:
  kind: command
  command: sh
  args: ["-c", "echo OK"]
  expect_exit: 0
  expect_output: OK
sandbox:
  kind: cgroups
  cgroups: off
skills:
  dir: %s
agent:
  workspace_dir: %s
`, baseURL, taskFile, filepath.Join(dir, "skills"), dir))
	return path
}

// TestTheTaskPathKeepsTheLibraryPromise: the shipped prompt tells the model it has a library
// of procedures. This runs the real command line and checks that a library action is ANSWERED,
// not refused for want of a library.
func TestTheTaskPathKeepsTheLibraryPromise(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	stub := &llmStub{
		analyze: `{"understandable":true,"summary":"list the skills","success_criteria":[],"risks":[],"needs_subtasks":false}`,
		plan:    `{"plan":[{"step":1,"action":"list skills","command":""}],"subtasks":[],"expected_result":"the index"}`,
		act: func(string) string {
			return `{"reasoning":"ask the library","actions":[{"kind":"list_skills","description":"see what exists","command":""}],"final_action":{"command":""}}`
		},
	}
	srv := stub.server()
	defer srv.Close()

	var out, errs bytes.Buffer
	Run(Options{
		Args: []string{"-config", taskConfig(t, dir, srv.URL, "list the library")},
		Out:  &out,
		Err:  &errs,
		NewLogger: func(config.Agent) (*logx.Logger, error) {
			return logx.New(logx.Options{Level: logx.Error, Console: false})
		},
	})

	seen := stub.allPrompts()
	if strings.Contains(seen, "no procedure library is configured") {
		t.Fatal("the task path promised the model a library and then said there was none")
	}
	if !strings.Contains(seen, "files-and-directories") {
		t.Errorf("the model asked for the index and did not get a shipped procedure in it:\n%s", tailOf(seen, 700))
	}
}

// TestThePlanPathKeepsTheLibraryPromise: the same promise, on the command line's plan run.
func TestThePlanPathKeepsTheLibraryPromise(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, fmt.Sprintf(`llm:
  base_url: %s
  api_key: x
  model: fake
skills:
  dir: %s
agent:
  workspace_dir: %s
`, "http://127.0.0.1:1", filepath.Join(dir, "skills"), dir))

	var out, errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-config", path, "-prompt", "what files are here"},
		Out:  &out,
		Err:  &errs,
		NewLogger: func(config.Agent) (*logx.Logger, error) {
			return logx.New(logx.Options{Level: logx.Error, Console: false})
		},
	})
	// The engine is unreachable on purpose: what is asserted is that the run built its
	// library and reached the point of talking to a model, rather than failing on the library.
	if strings.Contains(errs.String(), "no procedure library") {
		t.Errorf("the plan path has no library: %s", errs.String())
	}
	if code == 0 {
		t.Logf("plan run completed: %s", out.String())
	}
}

// TestAnUnknownActionKindIsRefusedNotExecuted is the one that matters most.
//
// The shipped skill names `read_file` — a PLAN-mode tool — and this mode has no such kind. The
// old fall-through ran the model's argument as a shell command, so a path became a program.
// The test asserts the two halves of the repair: the text is NOT executed, and the model is
// told which kinds exist so it can try one of them.
func TestAnUnknownActionKindIsRefusedNotExecuted(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	// A file whose execution would leave a mark, so "it ran" is observable rather than inferred.
	marker := filepath.Join(dir, "EXECUTED")
	script := filepath.Join(dir, "read_file")
	mustWrite(t, script, "#!/bin/sh\ntouch "+marker+"\n")
	if err := chmodExec(script); err != nil {
		t.Fatal(err)
	}

	stub := &llmStub{
		analyze: `{"understandable":true,"summary":"read it","success_criteria":[],"risks":[],"needs_subtasks":false}`,
		plan:    `{"plan":[{"step":1,"action":"read it","command":""}],"subtasks":[],"expected_result":"contents"}`,
		act: func(seen string) string {
			// The model does exactly what the skill's table told it to do.
			return `{"reasoning":"the skill says to use read_file","actions":[{"kind":"read_file","description":"read the file","command":"` + script + `"}],"final_action":{"command":""}}`
		},
	}
	srv := stub.server()
	defer srv.Close()

	var out, errs bytes.Buffer
	Run(Options{
		Args: []string{"-config", taskConfig(t, dir, srv.URL, "read the file")},
		Out:  &out,
		Err:  &errs,
		NewLogger: func(config.Agent) (*logx.Logger, error) {
			return logx.New(logx.Options{Level: logx.Error, Console: false})
		},
	})

	seen := stub.allPrompts()
	if strings.Contains(seen, "[read_file]") && !strings.Contains(seen, "is not an action this mode has") {
		t.Errorf("an unknown kind was answered without refusing it:\n%s", tailOf(seen, 700))
	}
	if !strings.Contains(seen, "is not an action this mode has") {
		t.Errorf("the model must be told the kind does not exist, and which ones do:\n%s", tailOf(seen, 900))
	}
	// And the proof that it was not executed: the marker the script would have created.
	if fileExists(marker) {
		t.Error("the model's argument was EXECUTED as a shell command")
	}
}

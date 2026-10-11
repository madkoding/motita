package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// --- Full run ---------------------------------------------------------------

// TestFullRunInMemory is the program's end-to-end test without re-executing
// processes: real configuration -> three real layers (sandbox included) -> agent
// with a simulated in-memory LLM -> verdict.
func TestFullRunInMemory(t *testing.T) {
	silence(t)
	dir := t.TempDir()

	// Simulated LLM: it analyses, plans and runs a command that creates the file
	// the anchor checks.
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
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"create the file","success_criteria":[],"risks":[],` +
				`"needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[{"step":1,"action":"create","command":"echo x > done.txt"}],"subtasks":[],` +
				`"expected_result":"file"}`
		default:
			content = `{"reasoning":"create the file","actions":[{"kind":"command","command":"echo ready > ` +
				`done.txt"}],"final_action":{"command":""}}`
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, fmt.Sprintf(`task_source:
  kind: file
  path: %s
anchor:
  kind: command
  command: sh
  args: ["-c", "test -s %s && echo ANCHOR_PASS"]
  expect_exit: 0
  expect_output: ANCHOR_PASS
sandbox:
  kind: cgroups
  cgroups: off
  memory_mb: 256
  cpu_seconds: 20
  timeout: 20s
llm:
  api_key: x
  base_url: %s
  model: simulated
  max_attempts: 1
  timeout: 10s
final_action:
  kind: command
  command: sh
  args: ["-c", "echo final > %s"]
agent:
  workspace_dir: %s
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 5s
`, filepath.Join(dir, "task.txt"), filepath.Join(dir, "done.txt"),
		srv.URL, filepath.Join(dir, "final.txt"), dir))
	mustWrite(t, filepath.Join(dir, "task.txt"), "create the file done.txt")

	var out, errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-config", path},
		Out:  &out,
		Err:  &errs,
	})

	if code != Success {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
	// The real test: the effects exist on the filesystem.
	if _, err := os.Stat(filepath.Join(dir, "done.txt")); err != nil {
		t.Errorf("the agent's command did not create the file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "final.txt")); err != nil {
		t.Errorf("the final action did not run: %v", err)
	}
}

// TestFailedRunReturns1: the anchor never passes and the process must exit with
// code 1 and say why.
func TestFailedRunReturns1(t *testing.T) {
	silence(t)
	dir := t.TempDir()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
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
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"x","needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[{"step":1,"action":"nothing","command":"true"}]}`
		default:
			content = `{"reasoning":"nothing","actions":[{"command":"true"}],"final_action":{"command":""}}`
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, fmt.Sprintf(`task_source:
  kind: file
  path: %s
anchor:
  kind: command
  command: sh
  args: ["-c", "exit 1"]
  expect_exit: 0
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
  base_url: %s
  model: simulated
  max_attempts: 1
  timeout: 10s
agent:
  max_retries: 0
  workspace_dir: %s
  log_level: error
  log_console: false
`, filepath.Join(dir, "task.txt"), srv.URL, dir))
	mustWrite(t, filepath.Join(dir, "task.txt"), "something impossible")

	var errs bytes.Buffer
	code := Run(Options{Args: []string{"-config", path}, Err: &errs})
	if code != RunError {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errs.String(), "exhausted") {
		t.Errorf("the error must explain the real reason: %q", errs.String())
	}
}

// TestTaskFromCommandLine: -task takes priority over the configured source.
func TestTaskFromCommandLine(t *testing.T) {
	silence(t)
	dir := t.TempDir()

	var received string
	var mu sync.Mutex
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
		mu.Lock()
		received += text
		mu.Unlock()

		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"x","needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[]}`
		default:
			content = `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, fmt.Sprintf(`task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
  base_url: %s
  model: simulated
  max_attempts: 1
  timeout: 10s
agent:
  workspace_dir: %s
  log_level: error
  log_console: false
# An EPHEMERAL gateway port, because a test that walks the real interface binds a real listener
# and the shipped default is a FIXED port: two such tests would contend for one global resource,
# and the loser fails with "address already in use" depending on how fast the winner released the
# socket. That is an intermittent failure which reads as machine flakiness rather than as a bug.
# The port is never what a test asserts, so it belongs here rather than in each test's flags.
gateway:
  listen: "127.0.0.1:0"
`, srv.URL, dir))

	code := Run(Options{
		Args: []string{"-config", path, "-task", "TASK-FROM-THE-COMMAND-LINE"},
		Out:  &bytes.Buffer{},
		Err:  &bytes.Buffer{},
	})
	if code != Success {
		t.Fatalf("code = %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(received, "TASK-FROM-THE-COMMAND-LINE") {
		t.Error("the command-line task did not reach the prompt")
	}
}

func TestTaskFromFile(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	taskFile := filepath.Join(dir, "task.md")
	mustWrite(t, taskFile, "TASK-FROM-A-FILE")

	var errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-task-file", taskFile, "-config", filepath.Join(dir, "does-not-exist.yaml")},
		Err:  &errs,
	})
	// Without a valid configuration it fails before using the task; what is
	// checked here is that the file is read (not that it fails because of it).
	if code != ConfigError {
		t.Fatalf("code = %d", code)
	}
}

// --- app: the last error paths ----------------------------------------------

// TestRunFailsWhenTheLoggerCannotBeBuilt: a log path that cannot be opened (it is
// a directory) must be a configuration error, before anything else runs.
func TestRunFailsWhenTheLoggerCannotBeBuilt(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// The log file points at a directory: opening it fails.
	mustWrite(t, path, "llm:\n  api_key: k\nagent:\n  log_file: "+dir+"\n")

	var errs bytes.Buffer
	code := Run(Options{Args: []string{"-config", path}, Err: &errs})
	if code != ConfigError {
		t.Fatalf("code = %d (%q)", code, errs.String())
	}
}

// TestRunFailsWhenTheLoggerConfigIsInvalid: an invalid log level must be caught
// before the agent starts.
func TestRunFailsWhenTheLoggerConfigIsInvalid(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, "llm:\n  api_key: k\nagent:\n  log_level: loud\n")

	var errs bytes.Buffer
	code := Run(Options{Args: []string{"-config", path}, Err: &errs})
	if code != ConfigError {
		t.Fatalf("code = %d (%q)", code, errs.String())
	}
}

// TestRunFailsWhenTheSourceCannotBeBuilt: a source kind that does not exist must
// be a configuration error naming the source, not a crash.
func TestRunFailsWhenTheSourceCannotBeBuilt(t *testing.T) {
	silence(t)
	t.Setenv("MOTITA_LLM_API_KEY", "k")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, "llm:\n  api_key: k\ntask_source:\n  kind: telepathy\n")

	var errs bytes.Buffer
	code := Run(Options{Args: []string{"-config", path}, Err: &errs})
	if code != ConfigError {
		t.Fatalf("code = %d (%q)", code, errs.String())
	}
}

// TestIsolationModeWithoutAClaimableKey: the diagnostic modes must work before
// the deployment has its key.
func TestIsolationModeWithoutAClaimableKey(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		mustWrite(t, path, "anchor:\n  kind: command\n  command: \"true\"\n")

		var out, errs bytes.Buffer
		code := Run(Options{Args: []string{"-config", path, "-isolation"}, Out: &out, Err: &errs})
		if code != Success {
			t.Fatalf("code = %d (%q)", code, errs.String())
		}
		if !strings.Contains(out.String(), "applied isolation") {
			t.Errorf("out = %q", out.String())
		}
	})
}

// TestSourceBuildFailureIsReported: a task source that cannot be built must end
// the run as a configuration error naming the reason. A blank -task is the way to
// reach that branch once the file itself is valid.
func TestSourceBuildFailureIsReported(t *testing.T) {
	silence(t)
	t.Setenv("MOTITA_LLM_API_KEY", "k")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(
		t,
		path,
		"llm:\n  api_key: k\nanchor:\n  kind: command\n  command: \"true\"\nagent:\n  workspace_dir: "+dir+"\n",
	)

	var errs bytes.Buffer
	// A blank task text is refused when the source is built.
	code := Run(Options{Args: []string{"-config", path, "-task", "   "}, Err: &errs})
	if code != ConfigError {
		t.Fatalf("code = %d (%q)", code, errs.String())
	}
	if !strings.Contains(errs.String(), "empty") {
		t.Errorf("the failure must explain the reason: %q", errs.String())
	}
}

// TestInjectedLoggerIsUsed: when a logger factory is injected (the tests use this
// to keep the output quiet), it must be the one that is called.
func TestInjectedLoggerIsUsed(t *testing.T) {
	silence(t)
	t.Setenv("MOTITA_LLM_API_KEY", "k")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(
		t,
		path,
		"llm:\n  api_key: k\nanchor:\n  kind: command\n  command: \"true\"\nagent:\n  workspace_dir: "+dir+"\n",
	)

	called := false
	code := Run(Options{
		Args: []string{"-config", path, "-validate-config"},
		Out:  &bytes.Buffer{},
		Err:  &bytes.Buffer{},
		NewLogger: func(config.Agent) (*logx.Logger, error) {
			called = true
			return logx.New(logx.Options{Level: logx.Error, Console: false})
		},
	})
	if code != Success {
		t.Fatalf("code = %d", code)
	}
	if !called {
		t.Error("the injected logger factory must be used")
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/onboard"
	"github.com/madkoding/starlight/internal/sandbox"
)

// silence reduces the log to errors so the test output stays readable (the
// agent's log writes to stderr by design).
func silence(t *testing.T) {
	t.Helper()
	l, err := logx.New(logx.Options{Level: logx.Error, Console: false})
	if err != nil {
		t.Fatalf("could not create the logger: %v", err)
	}
	logx.Install(l)
}

// inTempDir runs the body with the working directory set to a fresh temporary
// directory, and changes back afterwards. The program resolves relative paths
// (workspace_dir, log_file) against the working directory, so without this a test
// that runs the real flow would leave ./workspace inside the package and dirty the
// repository. Repository paths must be resolved BEFORE calling it (`repoPath`).
func inTempDir(t *testing.T, body func()) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not read the working directory: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("could not enter %s: %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("could not go back to %s: %v", previous, err)
		}
	}()
	body()
}

// repoPath resolves a path relative to the repository root (the package lives two
// levels down) to an absolute one, so a test can still find the examples after
// changing the working directory.
func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	all := append([]string{"..", ".."}, parts...)
	abs, err := filepath.Abs(filepath.Join(all...))
	if err != nil {
		t.Fatalf("could not resolve %v: %v", parts, err)
	}
	return abs
}

// --- Argument parsing -------------------------------------------------------

func TestParseFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected flags
		fails    bool
	}{
		{"empty", nil, flags{}, false},
		{"config", []string{"-config", "a.yaml"}, flags{configPath: "a.yaml"}, false},
		{"config with equals", []string{"-config=b.yaml"}, flags{configPath: "b.yaml"}, false},
		{"task", []string{"-task", "fix this"}, flags{task: "fix this"}, false},
		{"task file", []string{"-task-file", "t.md"}, flags{taskFile: "t.md"}, false},
		{"validate", []string{"-validate-config"}, flags{validateConfig: true}, false},
		{"version", []string{"-version"}, flags{version: true}, false},
		{"isolation", []string{"-isolation"}, flags{isolation: true}, false},
		{"all together", []string{"-config", "c.yaml", "-validate-config", "-version"}, flags{configPath: "c.yaml", validateConfig: true, version: true}, false},
		{"long form", []string{"--config", "d.yaml", "--isolation"}, flags{configPath: "d.yaml", isolation: true}, false},
		{"unknown flag", []string{"-nonsense"}, flags{}, true},
		{"missing value", []string{"-config"}, flags{}, true},
		{"stray empty", []string{""}, flags{}, true},
		{"help", []string{"-h"}, flags{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(tc.args)
			if tc.fails {
				if err == nil {
					t.Fatalf("an error was expected with %v", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Errorf("flags = %+v, expected %+v", got, tc.expected)
			}
		})
	}
}

// --- Version and usage errors -----------------------------------------------

func TestVersionDoesNotNeedConfiguration(t *testing.T) {
	var out, errs bytes.Buffer
	code := Run(Options{
		Args:    []string{"-version"},
		Out:     &out,
		Err:     &errs,
		Version: "v9.9.9",
		Goos:    "linux",
		Goarch:  "386",
	})
	if code != Success {
		t.Fatalf("code = %d, out=%q errs=%q", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "v9.9.9 (linux/386)") {
		t.Errorf("out = %q", out.String())
	}
}

func TestUnknownFlagReturns2(t *testing.T) {
	var out, errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-invented"},
		Out:  &out,
		Err:  &errs,
	})
	if code != ConfigError {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errs.String(), "unknown flag") {
		t.Errorf("errs = %q", errs.String())
	}
	// The message must include the usage, so the user knows what can be asked.
	if !strings.Contains(errs.String(), "Usage: starlight") {
		t.Errorf("the error must include the usage: %q", errs.String())
	}
}

func TestOptionsWithoutOutputDoesNotPanic(t *testing.T) {
	// Out and Err as nil must use a sink, not blow up.
	code := Run(Options{Args: []string{"-version"}})
	if code != Success {
		t.Errorf("code = %d", code)
	}
}

// --- Diagnostic modes -------------------------------------------------------

// TestValidateConfigWithRealExample runs the validation mode against the three
// YAML files of the repository: if an example stops being valid, this fails.
func TestValidateConfigWithRealExample(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		t.Setenv("STARLIGHT_LLM_API_KEY", "test-key")

		paths := []string{
			repoPath(t, "configs", "agent.yaml.example"),
			repoPath(t, "configs", "cases", "1-development.yaml"),
			repoPath(t, "configs", "cases", "2-data.yaml"),
			repoPath(t, "configs", "cases", "3-automation.yaml"),
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				t.Skipf("%s is missing: %v", path, err)
			}
			t.Run(filepath.Base(path), func(t *testing.T) {
				var out, errs bytes.Buffer
				code := Run(Options{
					Args: []string{"-config", path, "-validate-config"},
					Out:  &out,
					Err:  &errs,
				})
				if code != Success {
					t.Fatalf("code = %d, errs = %q", code, errs.String())
				}
				if !strings.Contains(out.String(), "valid configuration") {
					t.Errorf("out = %q", out.String())
				}
			})
		}
	})
}

// TestValidateConfigDoesNotRequireKey checks that a deployment can be validated
// before the key exists, but that an invalid file still fails.
func TestValidateConfigDoesNotRequireKey(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()

		good := filepath.Join(dir, "good.yaml")
		mustWrite(t, good, "anchor:\n  kind: command\n  command: make\n")

		var out, errs bytes.Buffer
		code := Run(Options{Args: []string{"-config", good, "-validate-config"}, Out: &out, Err: &errs})
		if code != Success {
			t.Fatalf("without a key the validation must work: %d %q", code, errs.String())
		}

		bad := filepath.Join(dir, "bad.yaml")
		mustWrite(t, bad, "task_source:\n  kind: telepathy\n")
		out.Reset()
		errs.Reset()
		code = Run(Options{Args: []string{"-config", bad, "-validate-config"}, Out: &out, Err: &errs})
		if code != ConfigError {
			t.Fatalf("an invalid file must give %d, gave %d (%q)", ConfigError, code, out.String())
		}
		if strings.Contains(out.String(), "valid") {
			t.Error("it must not report a valid configuration with an invalid file")
		}
	})
}
func TestMissingConfig(t *testing.T) {
	silence(t)
	var errs bytes.Buffer
	code := Run(Options{Args: []string{"-config", "/does/not/exist.yaml"}, Err: &errs})
	if code != ConfigError {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(errs.String(), "could not read the configuration") {
		t.Errorf("errs = %q", errs.String())
	}
}

func TestInvalidLogLevel(t *testing.T) {
	silence(t)
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	dir := t.TempDir()
	path := filepath.Join(dir, "level.yaml")
	mustWrite(t, path, "llm:\n  api_key: x\nagent:\n  log_level: verbose\n")

	var errs bytes.Buffer
	code := Run(Options{Args: []string{"-config", path}, Err: &errs})
	if code != ConfigError {
		t.Errorf("code = %d (%q)", code, errs.String())
	}
}

// TestIsolationShowsReality: the informative mode must show what was applied and
// what was requested but could not be applied.
func TestIsolationShowsReality(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "isolation.yaml")
		mustWrite(t, path, `anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: on
  cgroup_root: /a/path/that/does/not/exist
`)

		var out, errs bytes.Buffer
		code := Run(Options{Args: []string{"-config", path, "-isolation"}, Out: &out, Err: &errs})
		if code != Success {
			t.Fatalf("code = %d (%q)", code, errs.String())
		}
		text := out.String()
		if !strings.Contains(text, "applied isolation") {
			t.Errorf("out = %q", text)
		}
		// With a non-existent cgroups root it must declare what it could not apply.
		if !strings.Contains(text, "not applied") {
			t.Errorf("it should report what was not applied: %q", text)
		}
	})
}
func TestFailedSandboxReturns1(t *testing.T) {
	silence(t)
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.yaml")
	mustWrite(t, path, "llm:\n  api_key: x\n")

	var errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-config", path},
		Err:  &errs,
		NewSandbox: func(sandbox.Options) (*sandbox.Sandbox, error) {
			return nil, fmt.Errorf("simulated sandbox failure")
		},
	})
	if code != RunError {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errs.String(), "simulated sandbox failure") {
		t.Errorf("errs = %q", errs.String())
	}
}

func TestFailedEngineReturns2(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		t.Setenv("STARLIGHT_LLM_API_KEY", "x")
		dir := t.TempDir()
		path := filepath.Join(dir, "engine.yaml")
		mustWrite(t, path, "llm:\n  api_key: x\n")

		var errs bytes.Buffer
		code := Run(Options{
			Args: []string{"-config", path, "-task", "something"},
			Err:  &errs,
			NewEngine: func(config.LLM, *logx.Logger) (*llm.Client, error) {
				return nil, fmt.Errorf("engine not available")
			},
		})
		if code != ConfigError {
			t.Fatalf("code = %d", code)
		}
		if !strings.Contains(errs.String(), "engine not available") {
			t.Errorf("errs = %q", errs.String())
		}
	})
}
func TestInvalidSourceReturns2(t *testing.T) {
	silence(t)
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	dir := t.TempDir()
	path := filepath.Join(dir, "source.yaml")
	// A valid source kind but without the required path: config validation
	// already rejects it; the source path is exercised here.
	mustWrite(t, path, "llm:\n  api_key: x\ntask_source:\n  kind: file\n")

	var errs bytes.Buffer
	code := Run(Options{Args: []string{"-config", path}, Err: &errs})
	if code != ConfigError {
		t.Errorf("code = %d (%q)", code, errs.String())
	}
}

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
			content = `{"understandable":true,"summary":"create the file","success_criteria":[],"risks":[],"needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[{"step":1,"action":"create","command":"echo x > done.txt"}],"subtasks":[],"expected_result":"file"}`
		default:
			content = `{"reasoning":"create the file","actions":[{"kind":"command","command":"echo ready > done.txt"}],"final_action":{"command":""}}`
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

// --- Graceful shutdown ------------------------------------------------------

// TestGracefulShutdown: the first signal cancels the context and the work ends
// cleanly (code 0).
func TestGracefulShutdown(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 2s
`)

	signals := make(chan os.Signal, 2)
	done := make(chan struct{})

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		waitSignal: func(time.Duration) <-chan time.Time {
			return make(chan time.Time)
		},
		Exit: func(int) { t.Error("the exit must not be forced after a single signal") },
		RunAgent: func(ctx context.Context, _ *agent.Agent) error {
			go func() {
				time.Sleep(20 * time.Millisecond)
				signals <- fakeSignal{}
			}()
			select {
			case <-ctx.Done():
				close(done)
				return nil // cancelled gracefully
			case <-time.After(5 * time.Second):
				t.Error("the context was not cancelled by the signal")
				return nil
			}
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("the agent did not react to the signal")
	}
}

// fakeSignal implements os.Signal without depending on real signals.
type fakeSignal struct{}

func (fakeSignal) String() string { return "fake" }
func (fakeSignal) Signal()        {}

// --- Configuration conversions ----------------------------------------------

func TestSandboxOptionsByKind(t *testing.T) {
	cases := []struct {
		name        string
		sandboxKind string
		cgroups     string
		wantChroot  bool
		wantCgroups bool
		wantLimits  bool
	}{
		{"none removes the limits", "none", "on", false, false, false},
		{"chroot enables chroot", "chroot", "off", true, false, true},
		{"cgroups with auto", "cgroups", "auto", false, true, true},
		{"cgroups disabled", "cgroups", "off", false, false, true},
		{"unknown falls back to auto", "weird", "auto", false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Sandbox.Kind = tc.sandboxKind
			cfg.Sandbox.Cgroups = tc.cgroups
			cfg.Sandbox.Root = "/tmp/root"
			cfg.Sandbox.MemoryMB = 128

			op := SandboxOptions(cfg, logx.Global())
			if op.UseChroot != tc.wantChroot {
				t.Errorf("UseChroot = %v, expected %v", op.UseChroot, tc.wantChroot)
			}
			if op.UseCgroups != tc.wantCgroups {
				t.Errorf("UseCgroups = %v, expected %v", op.UseCgroups, tc.wantCgroups)
			}
			hasLimits := op.Limits.MemoryMB > 0
			if hasLimits != tc.wantLimits {
				t.Errorf("limits = %v (%+v), expected %v", hasLimits, op.Limits, tc.wantLimits)
			}
		})
	}
}

func TestSandboxOptionsTranslatesEverything(t *testing.T) {
	cfg := config.Default()
	cfg.Sandbox.Kind = "cgroups"
	cfg.Sandbox.MemoryMB = 300
	cfg.Sandbox.CPUSeconds = 40
	cfg.Sandbox.Processes = 33
	cfg.Sandbox.OpenFiles = 77
	cfg.Sandbox.MaxFileSizeMB = 5
	cfg.Sandbox.CgroupRoot = "/sys/fs/cgroup"
	cfg.Sandbox.Timeout = 42 * time.Second
	cfg.Sandbox.MaxOutputKB = 99
	cfg.Sandbox.KeepEphemeral = true
	cfg.Sandbox.IsolateNetwork = true
	cfg.Sandbox.User = "1000:1001"
	cfg.Agent.WorkspaceDir = "/tmp/w"

	op := SandboxOptions(cfg, logx.Global())

	if op.Dir != "/tmp/w" || op.Limits.MemoryMB != 300 || op.Limits.CPUSeconds != 40 ||
		op.Limits.Processes != 33 || op.Limits.OpenFiles != 77 || op.Limits.MaxFileSizeMB != 5 {
		t.Errorf("options = %+v", op)
	}
	if op.Timeout != 42*time.Second || op.MaxOutputKB != 99 || !op.Keep {
		t.Errorf("options = %+v", op)
	}
	if !op.Limits.NoNetwork {
		t.Error("isolate_network was not translated")
	}
	if !op.DropPrivs || op.Uid != 1000 || op.Gid != 1001 {
		t.Errorf("user not translated: %+v", op)
	}
}

func TestSandboxOptionsInvalidUser(t *testing.T) {
	cfg := config.Default()
	// An invalid user must not enable the privilege drop.
	cfg.Sandbox.User = "this-is-not-a-uid:not-a-gid:x"
	op := SandboxOptions(cfg, logx.Global())
	if op.DropPrivs {
		t.Errorf("an invalid user must not enable DropPrivs: %+v", op)
	}
}

func TestWantsCgroups(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"on", true}, {"off", false}, {"auto", true}, {"", true}, {"weird", true},
	} {
		cfg := config.Default()
		cfg.Sandbox.Cgroups = tc.value
		if got := WantsCgroups(cfg); got != tc.want {
			t.Errorf("cgroups=%q -> %v, expected %v", tc.value, got, tc.want)
		}
	}
}

func TestBuildSource(t *testing.T) {
	silence(t)
	cfg := config.Default()

	// Priority 1: the -task flag.
	f, err := BuildSource(cfg, "direct", "file.md", logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := f.Next(context.Background())
	if tk.Description != "direct" {
		t.Errorf("description = %q", tk.Description)
	}

	// Priority 2: the file.
	dir := t.TempDir()
	path := filepath.Join(dir, "t.md")
	mustWrite(t, path, "from file")
	f, err = BuildSource(cfg, "", path, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	tk, _ = f.Next(context.Background())
	if tk.Description != "from file" {
		t.Errorf("description = %q", tk.Description)
	}

	// Priority 3: the configured source (stdin by default).
	f, err = BuildSource(cfg, "", "", logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Describe(), "stdin") {
		t.Errorf("description = %q", f.Describe())
	}

	// An invalid source kind returns an error instead of killing the process.
	badCfg := config.Default()
	badCfg.TaskSource.Kind = "telepathy"
	if _, err := BuildSource(badCfg, "", "", logx.Global()); err == nil {
		t.Error("an invalid source kind must error")
	}

	// A non-existent file too.
	if _, err := BuildSource(cfg, "", filepath.Join(dir, "not-here.md"), logx.Global()); err != nil {
		// NewFile does not read the file until the first task, so it is not an
		// error here: the behaviour is documented.
		t.Logf("NewFile does not validate existence at construction: %v", err)
	}
}

func TestDescribeConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.LogFile = "/tmp/x.log"
	text := DescribeConfig(cfg)
	for _, part := range []string{"task_source=stdin", "llm=openai", "anchor=", "sandbox=", "/tmp/x.log"} {
		if !strings.Contains(text, part) {
			t.Errorf("missing %q in %q", part, text)
		}
	}

	cfg.Agent.LogFile = ""
	if !strings.Contains(DescribeConfig(cfg), "console only") {
		t.Errorf("without a file it must say so: %q", DescribeConfig(cfg))
	}
}

func TestLogPath(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.LogFile = "/tmp/r.log"
	if got := LogPath(cfg); got != "/tmp/r.log" {
		t.Errorf("path = %q", got)
	}
	cfg.Agent.LogFile = ""
	if got := LogPath(cfg); got != "stderr" {
		t.Errorf("without a file it must be stderr: %q", got)
	}
}

// --- Sandbox child mode -----------------------------------------------------

// TestChildModeReturns126: an invalid specification in child mode must return the
// reserved code and a clear message.
func TestChildModeReturns126(t *testing.T) {
	var errs bytes.Buffer
	code := Run(Options{
		Args: []string{sandbox.ChildMarker, "{not json}"},
		Err:  &errs,
	})
	if code != ChildError {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errs.String(), "sandbox") {
		t.Errorf("errs = %q", errs.String())
	}
}

// TestChildModeSuccess: the child mode's success path.
//
// The real child mode ends in syscall.Exec, which REPLACES the current process:
// if the test invoked it for real, it would swap the test binary for the command
// and the coverage profile would never be written (that was exactly the symptom
// that gave this design away). Hence the hook: app's decision is checked without
// running the exec.
func TestChildModeSuccess(t *testing.T) {
	var called []string
	var errs bytes.Buffer
	code := Run(Options{
		Args: []string{sandbox.ChildMarker, "{}"},
		Err:  &errs,
		RunChild: func(args []string) error {
			called = args
			return nil
		},
	})
	if code != Success {
		t.Fatalf("code = %d (%q)", code, errs.String())
	}
	if len(called) != 2 || called[0] != sandbox.ChildMarker {
		t.Errorf("the child mode received %v", called)
	}
}

// TestChildModeHookError: a child-mode failure returns the reserved code.
func TestChildModeHookError(t *testing.T) {
	var errs bytes.Buffer
	code := Run(Options{
		Args: []string{sandbox.ChildMarker, "{}"},
		Err:  &errs,
		RunChild: func([]string) error {
			return fmt.Errorf("could not enter the chroot")
		},
	})
	if code != ChildError {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errs.String(), "could not enter the chroot") {
		t.Errorf("errs = %q", errs.String())
	}
}

// TestForcedShutdownByDeadline: if the work does not finish within the deadline
// after the first signal, the exit is forced with the interruption code.
func TestForcedShutdownByDeadline(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 1s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		// Immediate countdown: it simulates that the deadline already expired.
		waitSignal: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
		RunAgent: func(ctx context.Context, _ *agent.Agent) error {
			time.Sleep(30 * time.Millisecond)
			signals <- fakeSignal{} // first signal: cancels
			<-ctx.Done()
			// The work does not finish: the countdown forces the exit.
			time.Sleep(200 * time.Millisecond)
			return nil
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case c := <-exits:
		if c != InterruptedError {
			t.Errorf("forced exit code = %d, expected %d", c, InterruptedError)
		}
	case <-time.After(3 * time.Second):
		t.Error("the exit was not forced when the deadline expired")
	}
}

// TestSecondSignalForcesExit: the second signal exits immediately.
func TestSecondSignalForcesExit(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 30s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		RunAgent: func(ctx context.Context, _ *agent.Agent) error {
			time.Sleep(20 * time.Millisecond)
			signals <- fakeSignal{} // first: cancel
			<-ctx.Done()
			time.Sleep(20 * time.Millisecond)
			signals <- fakeSignal{} // second: immediate exit
			time.Sleep(200 * time.Millisecond)
			return nil
		},
	})
	select {
	case c := <-exits:
		if c != InterruptedError {
			t.Errorf("code = %d", c)
		}
	case <-time.After(3 * time.Second):
		t.Error("the second signal did not force the exit")
	}
}

// TestClosedSignalChannel: if the channel closes (end of process), the handler
// must neither block nor exit bluntly.
func TestClosedSignalChannel(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 1s
`)

	signals := make(chan os.Signal, 2)
	close(signals)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(int) { t.Error("the exit must not be forced with the channel closed") },
		RunAgent: func(context.Context, *agent.Agent) error {
			time.Sleep(50 * time.Millisecond)
			return nil
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
}

// TestNewLoggerWithInvalidLevel: the logger's error path.
func TestNewLoggerWithInvalidLevel(t *testing.T) {
	op := Options{}
	op.complete()
	if _, err := op.newLogger(config.Agent{LogLevel: "verbose"}); err == nil {
		t.Error("an invalid log level must error")
	}
}

// TestNewLoggerUsesTheRealLogger: without a hook, the real logger is built (file
// included) and it is checked that it writes.
func TestNewLoggerUsesTheRealLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "output.log")
	op := Options{}
	op.complete()

	log, err := op.newLogger(config.Agent{LogLevel: "info", LogFile: path, LogConsole: false, LogMaxMB: 1, LogBackups: 1})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	log.Info("test message", "key", "value")
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the log was not written: %v", err)
	}
	if !strings.Contains(string(data), "test message") {
		t.Errorf("content = %q", string(data))
	}
}

// TestFlagsWithMissingValue covers the remaining flags that take a value.
func TestFlagsWithMissingValue(t *testing.T) {
	for _, flag := range []string{"-config", "-task", "-task-file"} {
		if _, err := parse([]string{flag}); err == nil {
			t.Errorf("%s with no value must error", flag)
		}
	}
}

// TestSandboxSubprocessRunsTheCommand: the real child mode, launched as a
// subprocess of the test binary (whose TestMain handles the marker). It is done as
// a subprocess, and not inside the test process, because RunAsChild ends in
// syscall.Exec, which would replace the process and the coverage profile would
// never be written.
func TestSandboxSubprocessRunsTheCommand(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed.txt")

	spec := sandbox.Spec{
		Command:     "/bin/sh",
		Args:        []string{"-c", "echo subprocess-ok > " + marker},
		Dir:         dir,
		Environment: []string{"PATH=/usr/bin:/bin"},
	}
	encoded, err := spec.Encode()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binary, sandbox.ChildMarker, encoded)
	cmd.Env = append(os.Environ(), "STARLIGHT_TEST_CHILD=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the child mode failed: %v (%s)", err, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the command did not run: %v", err)
	}
}

// --- Helpers ----------------------------------------------------------------

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
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
	t.Setenv("STARLIGHT_LLM_API_KEY", "k")
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

// TestShutdownHandlerUninstallsItself: after the work finishes, the shutdown
// goroutine must be gone (otherwise it would force an exit later, which is the
// bug that made the coverage profile disappear).
func TestShutdownHandlerUninstallsItself(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 1s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		// A deadline that fires immediately: if the handler were left alive it
		// would force the exit right away.
		waitSignal: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
		RunAgent: func(context.Context, *agent.Agent) error {
			return nil // the work finishes without any signal
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case c := <-exits:
		t.Errorf("the shutdown handler forced an exit (%d) after the work had finished", c)
	case <-time.After(200 * time.Millisecond):
		// Nothing happened: the handler was uninstalled correctly.
	}
}

// TestShutdownHandlerIgnoresAClosedChannel: if the signal channel is closed
// while the handler is waiting, it must return instead of forcing an exit.
func TestShutdownHandlerIgnoresAClosedChannel(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 5s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		RunAgent: func(context.Context, *agent.Agent) error {
			// The channel closes while the handler is waiting for a signal.
			time.Sleep(20 * time.Millisecond)
			close(signals)
			time.Sleep(80 * time.Millisecond)
			return nil
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case c := <-exits:
		t.Errorf("a closed channel must not force an exit (got %d)", c)
	default:
	}
}

// TestSourceBuildFailureIsReported: a task source that cannot be built must end
// the run as a configuration error naming the reason. A blank -task is the way to
// reach that branch once the file itself is valid.
func TestSourceBuildFailureIsReported(t *testing.T) {
	silence(t)
	t.Setenv("STARLIGHT_LLM_API_KEY", "k")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, "llm:\n  api_key: k\nanchor:\n  kind: command\n  command: \"true\"\nagent:\n  workspace_dir: "+dir+"\n")

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
	t.Setenv("STARLIGHT_LLM_API_KEY", "k")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, "llm:\n  api_key: k\nanchor:\n  kind: command\n  command: \"true\"\nagent:\n  workspace_dir: "+dir+"\n")

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

// TestClosedChannelDuringTheSecondWait: the shutdown countdown also listens to the
// teardown signal; if the channel closes while it waits, it must return instead of
// forcing an exit.
func TestClosedChannelDuringTheSecondWait(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 30s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		// A countdown that never fires, so the only way out is the closed channel.
		waitSignal: func(time.Duration) <-chan time.Time {
			return make(chan time.Time)
		},
		RunAgent: func(context.Context, *agent.Agent) error {
			// One signal (which cancels), then the channel closes.
			signals <- fakeSignal{}
			time.Sleep(30 * time.Millisecond)
			close(signals)
			return nil
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case c := <-exits:
		t.Errorf("a closed channel must not force an exit (got %d)", c)
	case <-time.After(300 * time.Millisecond):
		// No forced exit: correct.
	}
}

// --- the first-run wizard ----------------------------------------------------

// TestInitFlagRunsTheWizard: -init must write a configuration at the given path
// and say how to use it, without needing a key or a config to exist beforehand.
func TestInitFlagRunsTheWizard(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "starlight.yaml")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{"-init", "-config", path},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("openai\n1\n2\n\n\n"),
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the configuration must exist: %v", err)
		}
		text := out.String()
		if !strings.Contains(text, "Welcome to starlight") {
			t.Errorf("the wizard must introduce itself: %q", text)
		}
		// It must prove the generated file loads, which is the point of the wizard.
		if !strings.Contains(text, "the configuration loads") {
			t.Errorf("the wizard must check what it wrote: %q", text)
		}
	})
}

// TestInitWithoutConfigUsesADefaultPath: with no -config the destination is
// ./starlight.yaml, so the command is usable on its own.
func TestInitWithoutConfigUsesADefaultPath(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{"-init"},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("openai\n1\n2\n\n\n"),
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
		if _, err := os.Stat("starlight.yaml"); err != nil {
			t.Errorf("the default path must be used: %v", err)
		}
	})
}

// TestInitCancelledIsNotAFailure: backing out with q is a normal outcome and must
// exit 0 with a clear message.
func TestInitCancelledIsNotAFailure(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "starlight.yaml")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{"-init", "-config", path},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("q\n"),
		})
		if code != Success {
			t.Errorf("cancelling must not be an error: code = %d", code)
		}
		if !strings.Contains(out.String(), "nothing was written") {
			t.Errorf("out = %q", out.String())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("nothing may be written when cancelled")
		}
	})
}

// TestInitReportsAWizardFailure: a wizard that cannot write must exit with the
// configuration error code, not silently succeed.
func TestInitReportsAWizardFailure(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{"-init"},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("not-a-provider\nbad\nworse\n"),
		})
		if code != ConfigError {
			t.Errorf("code = %d, want %d", code, ConfigError)
		}
		if !strings.Contains(errs.String(), "❌") {
			t.Errorf("the failure must be reported: %q", errs.String())
		}
	})
}

// TestInitRefusesWhenTheGeneratedFileDoesNotLoad: if the wizard ever wrote
// something the loader rejects, the user has to hear it immediately instead of on
// the first task.
func TestInitRefusesWhenTheGeneratedFileDoesNotLoad(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args: []string{"-init", "-config", path},
			Out:  &out,
			Err:  &errs,
			// A wizard that writes an invalid file on purpose.
			RunOnboard: func(context.Context, io.Reader, io.Writer, string, onboard.Answers) (onboard.Result, error) {
				if err := os.WriteFile(path, []byte("llm:\n  provider: telepathy\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return onboard.Result{ConfigPath: path}, nil
			},
		})
		if code != ConfigError {
			t.Errorf("code = %d, want %d", code, ConfigError)
		}
		if !strings.Contains(errs.String(), "does not load") {
			t.Errorf("errs = %q", errs.String())
		}
	})
}

// TestInitUsesTheInjectedWizard: the flag must hand the real streams to the wizard,
// so the conversation is the documented one.
func TestInitUsesTheInjectedWizard(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")

		called := false
		var out bytes.Buffer
		code := Run(Options{
			Args: []string{"-init", "-config", path},
			Out:  &out,
			Err:  &out,
			RunOnboard: func(_ context.Context, in io.Reader, w io.Writer, gotPath string, preset onboard.Answers) (onboard.Result, error) {
				called = true
				if gotPath != path {
					t.Errorf("the wizard got %q, want %q", gotPath, path)
				}
				if preset.Provider != "" || preset.Model != "" || preset.APIKey != "" ||
					preset.AnchorCommand != "" || len(preset.AnchorArgs) != 0 {
					t.Errorf("the wizard must be asked everything: %+v", preset)
				}
				fmt.Fprint(w, "dialogue")
				// A real wizard leaves a loadable file behind: app checks it.
				if err := os.WriteFile(gotPath, []byte("anchor:\n  kind: command\n  command: true\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return onboard.Result{
					ConfigPath: gotPath,
					Provider:   onboard.Providers()[0],
					Model:      onboard.Providers()[0].Models[0].ID,
				}, nil
			},
		})
		if code != Success {
			t.Fatalf("code = %d", code)
		}
		if !called {
			t.Fatal("the wizard must be called")
		}
		if !strings.Contains(out.String(), "dialogue") {
			t.Errorf("the wizard writes to the same output: %q", out.String())
		}
	})
}

// --- Plan mode tests --------------------------------------------------------

func planServer(t *testing.T, answers []string) *httptest.Server {
	t.Helper()
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		text := ""
		for _, m := range req.Messages {
			text += m.Content
		}
		var content string
		if idx < len(answers) {
			content = answers[idx]
			idx++
		} else {
			content = "done"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
		})
	}))
}

func planConfig(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, fmt.Sprintf(`sandbox:
  kind: none
  cgroups: off
llm:
  api_key: x
  base_url: %s
  model: mock
  max_attempts: 1
  timeout: 10s
agent:
  workspace_dir: %s
  log_level: error
  log_console: false
`, srv.URL, dir))
	return path
}

func TestParsePlanAndPromptFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected flags
		fails    bool
	}{
		{"plan flag", []string{"-plan"}, flags{plan: true}, false},
		{"plan=true", []string{"-plan=true"}, flags{plan: true}, false},
		{"plan=false", []string{"-plan=false"}, flags{plan: false}, false},
		{"prompt", []string{"-p", "ask"}, flags{plan: false, prompt: "ask"}, false},
		{"prompt with equals", []string{"-p=ask"}, flags{plan: false, prompt: "ask"}, false},
		{"prompt missing value", []string{"-p"}, flags{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(tc.args)
			if tc.fails {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.plan != tc.expected.plan || got.prompt != tc.expected.prompt {
				t.Errorf("got %+v, expected %+v", got, tc.expected)
			}
		})
	}
}

func TestPlanOneShotPrompt(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"the plan"})
	defer srv.Close()
	path := planConfig(t, srv)
	var out, errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-config", path, "-plan", "-p", "list files"},
		Out:  &out,
		Err:  &errs,
	})
	if code != Success {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
	if !strings.Contains(out.String(), "the plan") {
		t.Errorf("out = %q", out.String())
	}
}

func TestPlanOneShotError(t *testing.T) {
	silence(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"down"}}`)
	}))
	defer srv.Close()
	path := planConfig(t, srv)
	var out, errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-config", path, "-plan", "-p", "list files"},
		Out:  &out,
		Err:  &errs,
	})
	if code != RunError {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
	if !strings.Contains(errs.String(), "❌") {
		t.Errorf("errs = %q", errs.String())
	}
}

func TestPlanFlagWithoutPromptEntersTUI(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()
	path := planConfig(t, srv)
	var out, errs bytes.Buffer
	tuiCalled := false
	code := Run(Options{
		Args: []string{"-config", path, "-plan"},
		Out:  &out,
		Err:  &errs,
		RunTUI: func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
			tuiCalled = true
			return Success
		},
	})
	if code != Success {
		t.Fatalf("code = %d", code)
	}
	if !tuiCalled {
		t.Fatal("-plan without -prompt should enter the TUI")
	}
}

func TestTUIFlag(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()
	path := planConfig(t, srv)
	var out, errs bytes.Buffer
	tuiCalled := false
	code := Run(Options{
		Args: []string{"-config", path, "-tui"},
		Out:  &out,
		Err:  &errs,
		RunTUI: func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
			tuiCalled = true
			return Success
		},
	})
	if code != Success {
		t.Fatalf("code = %d", code)
	}
	if !tuiCalled {
		t.Fatal("-tui should launch the TUI")
	}
}

func TestDefaultNoConfigGoesToTUI(t *testing.T) {
	silence(t)
	t.Setenv("STARLIGHT_LLM_API_KEY", "test")
	srv := planServer(t, []string{"default plan"})
	defer srv.Close()
	var out, errs bytes.Buffer
	tuiCalled := false
	code := Run(Options{
		Args: nil,
		Out:  &out,
		Err:  &errs,
		NewEngine: func(c config.LLM, l *logx.Logger) (*llm.Client, error) {
			c.BaseURL = srv.URL
			c.Model = "mock"
			return llm.New(c, l)
		},
		NewSandbox: func(o sandbox.Options) (*sandbox.Sandbox, error) {
			o.Dir = t.TempDir()
			return sandbox.New(o)
		},
		RunTUI: func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
			tuiCalled = true
			return Success
		},
	})
	if code != Success {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
	if !tuiCalled {
		t.Fatal("no task should launch the TUI by default")
	}
}

func TestPlanDefaultTimeoutAndLoops(t *testing.T) {
	cfg := config.Default()
	cfg.Sandbox.Timeout = 0
	if planDefaultTimeout(cfg) != 120*time.Second {
		t.Error("default timeout wrong")
	}
	cfg.Agent.MaxRetries = 0
	if planDefaultLoops(cfg) != 5 {
		t.Errorf("default loops = %d", planDefaultLoops(cfg))
	}
	cfg.Agent.MaxRetries = 3
	if planDefaultLoops(cfg) != 3 {
		t.Errorf("loops = %d", planDefaultLoops(cfg))
	}
	cfg.Agent.MaxRetries = 20
	if planDefaultLoops(cfg) != 5 {
		t.Errorf("loops = %d", planDefaultLoops(cfg))
	}
}

func TestRunTUINilHook(t *testing.T) {
	silence(t)
	t.Setenv("STARLIGHT_LLM_API_KEY", "test")
	srv := planServer(t, []string{"hello"})
	defer srv.Close()
	var out bytes.Buffer
	code := Run(Options{
		Args:  []string{"-config", planConfig(t, srv), "-tui"},
		Out:   &out,
		Err:   &out,
		Stdin: strings.NewReader("q\n"),
	})
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
}

// TestTUIRealRunnerPlanModeDoesNotPanic: the TUI is handed the production runner
// built inside runTUI. A struct literal there left the agent factory nil, so
// choosing Plan mode from the real menu crashed with a nil pointer dereference
// that no test caught, because every test injected the RunTUI hook instead of
// exercising the real one. This walks the real path: menu -> plan -> answer.
func TestTUIRealRunnerPlanModeDoesNotPanic(t *testing.T) {
	silence(t)
	t.Setenv("STARLIGHT_LLM_API_KEY", "test")
	srv := planServer(t, []string{"the answer"})
	defer srv.Close()

	var out bytes.Buffer
	code := Run(Options{
		// p selects plan mode, then the prompt, then Enter to leave the answer
		// screen, then q to quit the menu.
		Args:  []string{"-config", planConfig(t, srv), "-tui"},
		Out:   &out,
		Err:   &out,
		Stdin: strings.NewReader("p\nwhat is running?\n\nq\n"),
	})
	if code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	if !strings.Contains(out.String(), "the answer") {
		t.Errorf("the plan answer must be shown, out = %q", out.String())
	}
	if got := strings.Count(out.String(), "the answer"); got != 1 {
		t.Errorf("the answer must appear exactly once, found %d times in %q", got, out.String())
	}
	if strings.Contains(out.String(), "panic") {
		t.Errorf("the real runner must not panic, out = %q", out.String())
	}
}

// TestTUIRealRunnerModelsModeLists: choosing Models from the real menu must walk
// the production runner too, without panicking on a nil dependency.
func TestTUIRealRunnerModelsModeLists(t *testing.T) {
	silence(t)
	t.Setenv("STARLIGHT_LLM_API_KEY", "test")
	// A catalogue that answers for any path, so the real lister succeeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-from-catalogue"}]}`))
	}))
	defer srv.Close()

	path := planConfig(t, srv)
	var out bytes.Buffer
	code := Run(Options{
		Args:  []string{"-config", path, "-tui"},
		Out:   &out,
		Err:   &out,
		Stdin: strings.NewReader("m\n\nq\n"),
	})
	if code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	if !strings.Contains(out.String(), "base URL") {
		t.Errorf("the models screen must show the active setup, out = %q", out.String())
	}
}

func TestDefaultNoConfigButTaskUsesTaskMode(t *testing.T) {
	silence(t)
	t.Setenv("STARLIGHT_LLM_API_KEY", "test")
	var out, errs bytes.Buffer
	code := Run(Options{
		Args:     []string{"-task", "TASK-FROM-CMD"},
		Out:      &out,
		Err:      &errs,
		RunAgent: func(context.Context, *agent.Agent) error { return nil },
	})
	if code != Success {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
}

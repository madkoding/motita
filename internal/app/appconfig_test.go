package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

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

	limitsMatch := op.Limits.MemoryMB == 300 && op.Limits.CPUSeconds == 40 &&
		op.Limits.Processes == 33 && op.Limits.OpenFiles == 77
	if op.Dir != "/tmp/w" || !limitsMatch || op.Limits.MaxFileSizeMB != 5 {
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
	// A confined command must not read motita's logins, its stored keys, nor the gateway's own
	// state (its token, the push key and the sessions): SandboxOptions hides exactly SecretFiles.
	if strings.Join(op.HiddenPaths, " ") != strings.Join(config.SecretFiles(""), " ") {
		t.Errorf("motita's secrets are not hidden from the sandbox: %v", op.HiddenPaths)
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

// TestClaudeCodeMCPMode: claude starts the program as its MCP server, and that
// command line is served, not parsed as flags.
func TestClaudeCodeMCPMode(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(manifest, []byte(`[{"name":"read_file"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	code := Run(Options{
		Args:  []string{llm.ClaudeCodeMCPCommand, manifest},
		Stdin: strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"),
		Out:   &out,
		Err:   &errs,
	})
	if code != Success || !strings.Contains(out.String(), `"name":"read_file"`) {
		t.Fatalf("code = %d, out = %q, errs = %q", code, out.String(), errs.String())
	}

	code = Run(Options{Args: []string{llm.ClaudeCodeMCPCommand, filepath.Join(t.TempDir(), "missing.json")}, Err: &errs})
	if code != RunError || !strings.Contains(errs.String(), "claude-code-mcp") {
		t.Errorf("code = %d, errs = %q", code, errs.String())
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
	cmd.Env = append(os.Environ(), "MOTITA_TEST_CHILD=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the child mode failed: %v (%s)", err, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the command did not run: %v", err)
	}
}

package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
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
		{
			"all together",
			[]string{"-config", "c.yaml", "-validate-config", "-version"},
			flags{configPath: "c.yaml", validateConfig: true, version: true},
			false,
		},
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
	if !strings.Contains(errs.String(), "Usage: motita") {
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
		t.Setenv("MOTITA_LLM_API_KEY", "test-key")

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
	t.Setenv("MOTITA_LLM_API_KEY", "x")
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
	t.Setenv("MOTITA_LLM_API_KEY", "x")
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

// The configuration a run was loaded from holds its key, wherever it lives, so the sandbox hides
// it and the key file beside it from confined commands.
func TestTheLoadedConfigurationIsHiddenFromTheSandbox(t *testing.T) {
	silence(t)
	t.Setenv("MOTITA_LLM_API_KEY", "x")
	path := filepath.Join(t.TempDir(), "custom.yaml")
	mustWrite(t, path, "llm:\n  api_key: x\n")

	var hidden []string
	Run(Options{
		Args: []string{"-config", path},
		Err:  io.Discard,
		NewSandbox: func(o sandbox.Options) (*sandbox.Sandbox, error) {
			hidden = o.HiddenPaths
			return nil, fmt.Errorf("stop here")
		},
	})
	joined := strings.Join(hidden, "\n")
	for _, want := range []string{path, strings.TrimSuffix(path, ".yaml") + ".env", config.AuthDir()} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s is not hidden: %v", want, hidden)
		}
	}
}

func TestFailedEngineReturns2(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		t.Setenv("MOTITA_LLM_API_KEY", "x")
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
	t.Setenv("MOTITA_LLM_API_KEY", "x")
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

// TestFlagsWithMissingValue covers the remaining flags that take a value.
func TestFlagsWithMissingValue(t *testing.T) {
	for _, flag := range []string{"-config", "-task", "-task-file"} {
		if _, err := parse([]string{flag}); err == nil {
			t.Errorf("%s with no value must error", flag)
		}
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

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain lets the test binary act as the program itself when the marker is set:
// main() reads os.Args, installs the signal handlers and exits with the code from
// the app layer, none of which can be observed from inside the test process.
func TestMain(m *testing.M) {
	if os.Getenv("STARLIGHT_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// runAgent runs the program in a subprocess and returns its stdout, stderr and
// exit code.
func runAgent(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "STARLIGHT_TEST_MAIN=1")
	cmd.Env = append(cmd.Env, env...)

	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs

	code := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("could not run the program: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errs.String(), code
}

// TestVersionExitsZero: -version must print the platform and succeed without any
// configuration, which is what a deployment script checks first.
func TestVersionExitsZero(t *testing.T) {
	out, errs, code := runAgent(t, nil, "-version")
	if code != 0 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(out, "starlight") {
		t.Errorf("out = %q", out)
	}
	// The version is injected at build time with -ldflags; in a plain `go test`
	// it is the development placeholder.
	if !strings.Contains(out, "dev") && !strings.Contains(out, "v") {
		t.Errorf("the version must be shown: %q", out)
	}
	if !strings.Contains(out, "/") {
		t.Errorf("the platform must be shown: %q", out)
	}
}

// TestUnknownFlagExitsTwo: an unrecognised flag is a configuration error (exit
// code 2), which is deliberately different from a task failure.
func TestUnknownFlagExitsTwo(t *testing.T) {
	_, errs, code := runAgent(t, nil, "-no-such-flag")
	if code != 2 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(errs, "unknown flag") {
		t.Errorf("errs = %q", errs)
	}
}

// TestValidateConfigExitsZero: a valid configuration must be accepted and
// reported, without a key being present anywhere.
func TestValidateConfigExitsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	content := "anchor:\n  kind: command\n  command: \"true\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errs, code := runAgent(t, nil, "-config", path, "-validate-config")
	if code != 0 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(out, "valid configuration") {
		t.Errorf("out = %q", out)
	}
}

// TestInvalidConfigExitsTwo: a file that is not valid must exit 2 instead of
// falling back to the defaults and reporting success.
func TestInvalidConfigExitsTwo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("task_source:\n  kind: telepathy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errs, code := runAgent(t, nil, "-config", path, "-validate-config")
	if code != 2 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(errs, "❌") {
		t.Errorf("the failure must be marked on stderr: %q", errs)
	}
}

// TestIsolationModeExitsZero: the isolation report must work without a key, since
// it is what an operator runs when a deployment misbehaves.
func TestIsolationModeExitsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte("anchor:\n  kind: command\n  command: \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errs, code := runAgent(t, nil, "-config", path, "-isolation")
	if code != 0 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(out, "isolation") {
		t.Errorf("out = %q", out)
	}
}

// TestChildModeExitsWithTheReservedCode: invoked with the sandbox's marker, the
// process must behave as the isolation child. An unreadable spec must give the
// reserved code 126, which is what the parent looks for.
func TestChildModeExitsWithTheReservedCode(t *testing.T) {
	_, errs, code := runAgent(t, nil, "__sandbox_exec", "{not json}")
	if code != 126 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(errs, "sandbox") {
		t.Errorf("the message must identify the sandbox: %q", errs)
	}
}

// TestMissingConfigExitsTwo: a file that does not exist is a configuration error,
// not a crash.
func TestMissingConfigExitsTwo(t *testing.T) {
	_, errs, code := runAgent(t, nil, "-config", "/does/not/exist.yaml", "-validate-config")
	if code != 2 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(errs, "could not read") {
		t.Errorf("the error must explain the problem: %q", errs)
	}
}

// TestAgentWithoutAnchorRefusesToRun: without a validator the run must fail
// instead of pretending a task succeeded. The message must say how to fix it.
func TestAgentWithoutAnchorRefusesToRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	content := "llm:\n  api_key: test\nanchor:\n  kind: none\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errs, code := runAgent(t, nil, "-config", path, "-task", "do something")
	if code != 1 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(errs, "anchor.kind=none") {
		t.Errorf("the error must explain the missing anchor: %q", errs)
	}
}

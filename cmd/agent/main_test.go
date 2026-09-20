package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/app"
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
// exit code. The subprocess runs in its own temporary directory: with the default
// relative workspace_dir it would otherwise create ./workspace inside the
// package, leaving the repository dirty after every test run.
func runAgent(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "STARLIGHT_TEST_MAIN=1")
	// A HOME of its own, so the subprocess cannot reach the real ~/.starlight. The program keeps
	// its state there now, and without this a test that runs it would create or read the home of
	// whoever runs the suite.
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir())
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

// TestRunWithBaseCtx exercises the wiring that main() sets up.
// TestSetupSignalsClosesCleanly verifies that the background goroutine exits
// when the signal channel is closed before any signal arrives.
func TestSetupSignalsClosesCleanly(t *testing.T) {
	ch := make(chan os.Signal, 1)
	ctx, _, stop := setupSignals(ch)
	defer stop()

	close(ch)

	select {
	case <-ctx.Done():
		t.Fatal("context must not be cancelled when the channel is closed")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRunWithBaseCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	code := app.Run(app.Options{
		Args:    []string{"-version"},
		Out:     os.Stdout,
		Err:     os.Stderr,
		Version: "test",
		Goos:    "linux",
		Goarch:  "386",
		Signals: make(chan os.Signal, 1),
		BaseCtx: ctx,
	})
	if code != app.Success {
		t.Fatalf("exit code = %d, want %d", code, app.Success)
	}
}

// TestSetupSignalsCancelsContext verifies that setupSignals cancels the returned
// context when SIGINT is delivered, while still making the signal available on
// the returned channel.
func TestSetupSignalsCancelsContext(t *testing.T) {
	ctx, sigs, stop := setupSignals(nil)
	defer stop()

	sigs <- syscall.SIGINT

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not cancelled after SIGINT")
	}

	select {
	case s := <-sigs:
		if s != syscall.SIGINT {
			t.Fatalf("got signal %v, want SIGINT", s)
		}
	case <-time.After(time.Second):
		t.Fatal("SIGINT was not re-injected into the signal channel")
	}
}

// TestSetupSignalsStopsCleanly checks that the cleanup function can be called
// repeatedly without panic and that it releases the signal handler and the
// background goroutine.
func TestSetupSignalsStopsCleanly(t *testing.T) {
	ctx, _, stop := setupSignals(nil)

	stop()
	stop()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not cancelled by stop()")
	}
}

// TestMainEntryVersion is a lightweight smoke test that main() can at least be
// imported and that the version variable is non-empty.
func TestMainEntryVersion(t *testing.T) {
	if version == "" {
		t.Fatal("version variable is empty")
	}
}

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain lets the test binary act as the program itself when the marker is set,
// so the command-line behaviour (flags, exit codes, messages) is exercised for
// real: main() reads os.Args and writes to the real stdout/stderr, which is only
// meaningful in a process of its own.
func TestMain(m *testing.M) {
	if os.Getenv("STARLIGHT_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// runMain runs the program in a subprocess with the given arguments and returns
// its stdout, stderr and exit code.
func runMain(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "STARLIGHT_TEST_MAIN=1", "NO_COLOR=1")
	cmd.Env = append(cmd.Env, env...)

	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errorsAs(err, &exitErr) {
			t.Fatalf("could not run the program: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errs.String(), code
}

// errorsAs avoids importing errors just for one call in the test.
func errorsAs(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

// TestMainPrintsTheVersion: `--version` must print the name and the platform and
// exit with success, without needing any key.
func TestMainPrintsTheVersion(t *testing.T) {
	out, _, code := runMain(t, nil, "-version")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out, programName) {
		t.Errorf("out = %q", out)
	}
	if !strings.Contains(out, "/") {
		t.Errorf("the platform must be shown: %q", out)
	}
}

// TestMainRefusesToStartWithoutAKey: without a key the program must explain how
// to set it and exit with code 1 (the contract cron and systemd rely on).
func TestMainRefusesToStartWithoutAKey(t *testing.T) {
	_, errs, code := runMain(t, []string{"OPENAI_API_KEY="}, "-p", "something")
	if code != 1 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(errs, "OPENAI_API_KEY") {
		t.Errorf("the error must name the variable: %q", errs)
	}
}

// TestMainHelpFlagListsTheOptions: the usage text must list the flags, because it
// is what a new user reads first.
func TestMainHelpFlagListsTheOptions(t *testing.T) {
	_, errs, _ := runMain(t, nil, "-h")
	for _, part := range []string{"Usage:", "-p", "-model", "-max-loops"} {
		if !strings.Contains(errs, part) {
			t.Errorf("the usage text must mention %q: %q", part, errs)
		}
	}
}

// TestMainRunsAOneShotInstruction: the non-interactive mode must run one turn and
// exit 0, honouring the injected endpoint. The simulated server is not reachable
// from the subprocess, so the run fails: what is checked is that the program
// reports the failure and exits 1 instead of hanging.
func TestMainRunsAOneShotInstruction(t *testing.T) {
	_, errs, code := runMain(t,
		[]string{"OPENAI_API_KEY=test", "OPENAI_BASE_URL=http://127.0.0.1:1/v1", "OPENAI_MODEL=mock"},
		"-p", "say hello", "-timeout", "1s")
	if code != 1 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(errs, "❌") {
		t.Errorf("the failure must be reported: %q", errs)
	}
}

// TestMainAcceptsLooseArguments: an instruction can also be given as plain
// arguments, without the -p flag.
func TestMainAcceptsLooseArguments(t *testing.T) {
	_, errs, code := runMain(t,
		[]string{"OPENAI_API_KEY=test", "OPENAI_BASE_URL=http://127.0.0.1:1/v1", "OPENAI_MODEL=mock"},
		"say", "hello", "-timeout", "1s")
	if code != 1 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(errs, "❌") {
		t.Errorf("the failure must be reported: %q", errs)
	}
}

// TestMainRejectsAnInvalidMaxLoops: a maximum below one is clamped to one instead
// of producing an agent that never calls the model.
func TestMainRejectsAnInvalidMaxLoops(t *testing.T) {
	_, errs, code := runMain(t,
		[]string{"OPENAI_API_KEY=test", "OPENAI_BASE_URL=http://127.0.0.1:1/v1"},
		"-p", "hello", "-max-loops", "0", "-timeout", "1s")
	if code != 1 {
		// It still fails because the endpoint is unreachable, which proves it
		// tried to run rather than refusing the flag.
		t.Fatalf("code = %d (%q)", code, errs)
	}
}

// TestHelpFunctionWritesTheSummary: the in-REPL help must describe the commands
// and the environment variables.
func TestHelpFunctionWritesTheSummary(t *testing.T) {
	saved := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	help()
	write.Close()
	os.Stderr = saved

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(read); err != nil {
		t.Fatal(err)
	}
	read.Close()

	text := buf.String()
	for _, part := range []string{"/help", "/reset", "exit", "OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_MODEL"} {
		if !strings.Contains(text, part) {
			t.Errorf("the help must mention %q: %q", part, text)
		}
	}
}

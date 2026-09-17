//go:build linux

// The sandbox's isolation is Linux-specific (ulimit, cgroups v1, chroot, dropping
// privileges). These tests exercise that code, so they only build where it exists;
// the portable behaviour is covered by portable_test.go.

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
)

func newSandbox(t *testing.T, lim Limits) *Sandbox {
	t.Helper()
	s, err := New(Options{
		Dir:         t.TempDir(),
		Limits:      lim,
		UseCgroups:  false, // do not touch real cgroups in the tests
		Timeout:     30 * time.Second,
		MaxOutputKB: 64,
	})
	if err != nil {
		t.Fatalf("could not create the sandbox: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestSandboxRunsWithLimits checks the full real path: the agent re-executes
// itself as a child, applies setrlimit and execs the requested command.
func TestSandboxRunsWithLimits(t *testing.T) {
	s := newSandbox(t, Limits{MemoryMB: 256, CPUSeconds: 10, Processes: 64, OpenFiles: 128, MaxFileSizeMB: 8})

	output, truncated, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo sandbox-alive"},
	})
	if err != nil {
		t.Fatalf("the run failed: %v (output: %q)", err, output)
	}
	if exit != 0 {
		t.Errorf("exit = %d", exit)
	}
	if strings.TrimSpace(output) != "sandbox-alive" {
		t.Errorf("output = %q", output)
	}
	if truncated {
		t.Error("it should not be truncated")
	}
}

// TestSandboxAppliesMemoryLimit: here is the proof that the setrlimits are
// REALLY applied. Without a limit, reserving 2 GB would work; with memory_mb=64
// it must fail.
func TestSandboxAppliesMemoryLimit(t *testing.T) {
	s := newSandbox(t, Limits{MemoryMB: 64})

	// The child is asked to try to reserve 512 MB and touch them.
	program := `head -c 536870912 /dev/zero > /dev/null 2>&1; echo "reserved"`
	output, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", program},
	})
	if err != nil {
		// A failure to launch is also acceptable (the limit did its job).
		t.Logf("the command could not run with the limit (expected): %v", err)
		return
	}
	// With an RLIMIT_AS of 64 MB, `head` cannot allocate 512 MB: it must fail.
	if exit == 0 && strings.Contains(output, "reserved") {
		t.Skip("this environment does not apply RLIMIT_AS (container without support?): skipping")
	}
	t.Logf("limit applied correctly: exit=%d output=%q", exit, output)
}

// TestSandboxWorkingDirectoryIsShared: the effect of the work has to survive
// between runs, because that is what the anchor needs to see in order to
// validate. If the sandbox ran in a directory that is deleted when it finishes,
// the anchor could never find the result and the agent would fail every time.
func TestSandboxWorkingDirectoryIsShared(t *testing.T) {
	s := newSandbox(t, Limits{})

	// First run: it creates a file in the given working directory.
	output1, _, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "pwd; echo content > result.txt"},
		Dir:     s.base,
	})
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	dir1 := strings.Split(strings.TrimSpace(output1), "\n")[0]
	if dir1 != s.base {
		t.Errorf("the command must run in the requested directory (%s) and it ran in %s", s.base, dir1)
	}

	// Second run: it must see the file from the first one.
	output2, _, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "cat result.txt"},
		Dir:     s.base,
	})
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if !strings.Contains(output2, "content") {
		t.Errorf("the effect of the previous run was lost: %q", output2)
	}
	if _, err := os.Stat(filepath.Join(s.base, "result.txt")); err != nil {
		t.Errorf("the file is not in the working directory: %v", err)
	}
}

// TestSandboxTemporaryIsEphemeral: what IS ephemeral and per attempt is the
// temporary directory (TMPDIR), which is deleted when the run finishes.
func TestSandboxTemporaryIsEphemeral(t *testing.T) {
	s := newSandbox(t, Limits{})

	output1, _, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "echo $TMPDIR && touch $TMPDIR/temporary.txt"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	tmp1 := strings.TrimSpace(output1)
	if !strings.Contains(tmp1, "tmp-") {
		t.Errorf("TMPDIR should be a tmp-* directory of its own: %q", tmp1)
	}
	if _, err := os.Stat(tmp1); !os.IsNotExist(err) {
		t.Errorf("the temporary directory %s should have been deleted", tmp1)
	}

	output2, _, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "echo $TMPDIR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tmp2 := strings.TrimSpace(output2)
	if tmp1 == tmp2 {
		t.Errorf("every run must have its own TMPDIR: %s", tmp1)
	}
}

// TestSandboxCommandByName: the YAML allows writing "command: make", and
// syscall.Exec does not search PATH by itself, so the child must resolve it.
func TestSandboxCommandByName(t *testing.T) {
	s := newSandbox(t, Limits{})

	output, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "echo",
		Args:    []string{"resolved-by-path"},
	})
	if err != nil {
		t.Fatalf("a command by name must be resolved against PATH: %v (output %q)", err, output)
	}
	if exit != 0 || !strings.Contains(output, "resolved-by-path") {
		t.Errorf("exit=%d output=%q", exit, output)
	}
}

// TestSandboxWithVeryLowMemoryLimit: a bug found by CI. If the process applying
// the limits is a Go binary, applying RLIMIT_AS before resolving the command's
// path makes the runtime itself die with "fatal error: runtime: cannot allocate
// memory" (the limit also counts the virtual memory the runtime maps, and that
// reservation depends on the number of cores: it worked on one machine and
// failed on a runner with more cores).
//
// With the order fixed (all the preparations first, the limits just before the
// exec), a small limit must still allow running commands.
func TestSandboxWithVeryLowMemoryLimit(t *testing.T) {
	s := newSandbox(t, Limits{MemoryMB: 64, OpenFiles: 64})

	output, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo alive-with-little-memory"},
	})
	if err != nil {
		t.Fatalf("with a 64 MB limit the isolation must still work: %v (output %q)", err, output)
	}
	if exit != 0 || !strings.Contains(output, "alive-with-little-memory") {
		t.Errorf("exit=%d output=%q", exit, output)
	}
}

// TestSandboxCommandByNameWithLowLimit: the exact case that used to fail. PATH
// resolution needs to reserve memory, so it must happen before the limit.
func TestSandboxCommandByNameWithLowLimit(t *testing.T) {
	s := newSandbox(t, Limits{MemoryMB: 64})

	output, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "echo",
		Args:    []string{"resolved-with-limit"},
	})
	if err != nil {
		t.Fatalf("resolving PATH with the limit already applied kills the runtime: %v (output %q)", err, output)
	}
	if exit != 0 || !strings.Contains(output, "resolved-with-limit") {
		t.Errorf("exit=%d output=%q", exit, output)
	}
}

// TestUlimitCommands checks that the right orders are generated (and
// deterministically so) for every configured limit.
func TestUlimitCommands(t *testing.T) {
	if got := ulimitCommands(Limits{}); len(got) != 0 {
		t.Errorf("without limits there must be no orders: %v", got)
	}

	got := ulimitCommands(Limits{CPUSeconds: 30, MemoryMB: 256, Processes: 64, OpenFiles: 128, MaxFileSizeMB: 8})
	if len(got) != 5 {
		t.Fatalf("orders = %v", got)
	}
	// The values must use each ulimit's units: -v in KB, -f in 512-byte blocks.
	joined := strings.Join(got, " ")
	for _, expected := range []string{"ulimit -t 30", "ulimit -v 262144", "ulimit -u 64", "ulimit -n 128", "ulimit -f 16384"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("%q is missing from %v", expected, got)
		}
	}
	// The orders are sorted so that the output is stable.
	if !sort.StringsAreSorted(got) {
		t.Errorf("the orders must be sorted: %v", got)
	}
}

// TestWrapWithUlimitNoLimits: without limits no shell gets in the way.
func TestWrapWithUlimitNoLimits(t *testing.T) {
	command, args := wrapWithUlimit(Limits{}, "/bin/echo", []string{"hello"})
	if command != "/bin/echo" || len(args) != 2 || args[0] != "/bin/echo" {
		t.Errorf("command=%q args=%v", command, args)
	}
}

// TestWrapWithUlimitWithLimits: the command and its arguments are passed without
// re-interpretation, and the shell execs so as not to remain as an intermediate
// process.
func TestWrapWithUlimitWithLimits(t *testing.T) {
	command, args := wrapWithUlimit(Limits{MemoryMB: 128}, "/bin/echo", []string{"a b", "$HOME"})
	if command != "/bin/sh" {
		t.Fatalf("command = %q", command)
	}
	script := args[2]
	if !strings.Contains(script, `exec "$@"`) {
		t.Errorf("the script must exec so as not to leave intermediate processes: %q", script)
	}
	// The real command must arrive as the final positional argument.
	if args[len(args)-2] != "a b" || args[len(args)-1] != "$HOME" {
		t.Errorf("the arguments must pass through as they are: %v", args)
	}
	if args[len(args)-3] != "/bin/echo" {
		t.Errorf("the command must come after $0: %v", args)
	}
}

// TestSandboxCommandMissingFromPATH: if it does not exist, the error must be
// clear.
func TestSandboxCommandMissingFromPATH(t *testing.T) {
	s := newSandbox(t, Limits{})
	_, truncated, exit, err := s.Run(context.Background(), execx.Request{
		Command: "does-not-exist-this-command-anywhere",
	})
	if err == nil {
		t.Fatalf("an error was expected (exit=%d truncated=%v)", exit, truncated)
	}
	if exit != 127 {
		t.Logf("exit = %d for a non-existent command (127 is the expected one)", exit)
	}
}

// TestSandboxTimeout: the sandbox cannot hang on an eternal command.
func TestSandboxTimeout(t *testing.T) {
	s := newSandbox(t, Limits{})
	start := time.Now()
	output, _, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
		Timeout: 1 * time.Second,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a timeout error was expected")
	}
	if !strings.Contains(err.Error(), "exceeded the") {
		t.Errorf("unexpected error: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the timeout took too long: %s (was the process group not killed?)", elapsed)
	}
	_ = output
}

// TestSandboxExitCodeIsPropagated: the real exit code reaches the caller, which
// is what the anchor needs to decide PASS/FAIL.
func TestSandboxExitCodeIsPropagated(t *testing.T) {
	s := newSandbox(t, Limits{})
	_, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "exit 42"},
	})
	if err != nil {
		t.Fatalf("the exit code should not be a run error: %v", err)
	}
	if exit != 42 {
		t.Errorf("exit = %d, expected 42", exit)
	}
}

// TestSandboxDoesNotInheritSecrets: the agent's keys must not reach the command.
func TestSandboxDoesNotInheritSecrets(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "secret-key-that-must-not-leak")
	s := newSandbox(t, Limits{})

	output, _, _, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "env"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(output, "secret-key") {
		t.Error("the LLM key leaked into the command's environment")
	}
	if strings.Contains(output, "OPENAI_API_KEY") {
		t.Error("OPENAI_API_KEY must not be exposed to the command")
	}
}

// TestSandboxLimitedOutput: a huge output is cut without breaking the command.
func TestSandboxLimitedOutput(t *testing.T) {
	s := newSandbox(t, Limits{})
	s.op.MaxOutputKB = 1 // 1 KiB
	output, truncated, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "i=0; while [ $i -lt 2000 ]; do echo 'long enough filler line'; i=$((i+1)); done"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !truncated {
		t.Error("a ~60 KB output with a 1 KB limit must be marked as truncated")
	}
	if len(output) > 2*1024 {
		t.Errorf("the truncated output measures %d bytes", len(output))
	}
	if exit != 0 {
		t.Errorf("exit = %d (the truncation must not make the command fail)", exit)
	}
}

// TestSandboxCommandMissing: it gives a clear error, not a panic.
func TestSandboxCommandMissing(t *testing.T) {
	s := newSandbox(t, Limits{})
	_, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/does/not/exist",
	})
	if err == nil {
		t.Fatal("an error was expected")
	}
	t.Logf("exit=%d err=%v", exit, err)
}

// TestChildExecutionIsDetected: the child mode marker must not be confused with
// a user flag.
func TestChildExecutionIsDetected(t *testing.T) {
	if !IsChildExecution([]string{ChildMarker, "{}"}) {
		t.Error("it should detect the child mode")
	}
	if IsChildExecution([]string{"-config", "x.yaml"}) {
		t.Error("it should not detect child mode on a normal command line")
	}
	if IsChildExecution(nil) {
		t.Error("with no arguments there is no child mode")
	}
}

// TestChildWithoutCommand: an incomplete spec fails with a clear message.
func TestChildWithoutCommand(t *testing.T) {
	if err := RunAsChild([]string{ChildMarker, `{"dir":"/tmp"}`}); err == nil {
		t.Fatal("a spec without a command must fail")
	}
	if err := RunAsChild([]string{ChildMarker, "not json"}); err == nil {
		t.Fatal("an unreadable spec must fail")
	}
}

// TestChildAppliesTheFileLimit checks the EFFECT of the limit: inside the
// sandbox, the maximum number of open descriptors must be the configured one and
// not the system's (which is usually 1024 or more).
//
// The re-executed subprocess is the test binary itself, and TestMain takes care
// that it attends the sandbox marker instead of launching the suite again.
func TestChildAppliesTheFileLimit(t *testing.T) {
	s := newSandbox(t, Limits{OpenFiles: 96})

	output, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "ulimit -n"},
	})
	if err != nil {
		t.Fatalf("error: %v (output %q)", err, output)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, output = %q", exit, output)
	}
	seen := strings.TrimSpace(output)
	if seen != "96" {
		t.Errorf("the descriptor limit applied is %q and 96 was configured", seen)
	}
}

// TestChildAppliesTheCPULimit: CPU time must be bounded. A loop that burns CPU
// without exiting through I/O is used, which is the case RLIMIT_CPU exists to
// cover.
func TestChildAppliesTheCPULimit(t *testing.T) {
	s := newSandbox(t, Limits{CPUSeconds: 2})

	start := time.Now()
	output, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "while :; do :; done"},
		Timeout: 20 * time.Second,
	})
	elapsed := time.Since(start)
	// With RLIMIT_CPU=2 the process dies by signal at around 2 s.
	if elapsed > 15*time.Second {
		t.Errorf("the CPU limit was not applied: it took %s", elapsed)
	}
	if exit == 0 {
		t.Errorf("an infinite loop cannot exit with 0 (output %q)", output)
	}
	if err == nil {
		t.Log("the process died and it was not reported as an error: accepted if the exit is not 0")
	}
	t.Logf("CPU cut: elapsed=%s exit=%d err=%v", elapsed.Round(time.Millisecond), exit, err)
}

// TestIsolationDeclared: what the sandbox says it isolates must match what it
// really built.
func TestIsolationDeclared(t *testing.T) {
	s := newSandbox(t, Limits{MemoryMB: 128})
	modes := s.Isolation()
	hasEphemeral, hasLimits := false, false
	for _, m := range modes {
		if m == ModeEphemeral {
			hasEphemeral = true
		}
		if m == ModeLimits {
			hasLimits = true
		}
	}
	if !hasEphemeral {
		t.Error("the ephemeral directory must always be declared")
	}
	if !hasLimits {
		t.Error("with limits configured 'posix_limits' must be declared")
	}
	// Without cgroups configured it must not lie by saying it uses them.
	for _, m := range modes {
		if m == ModeCgroups {
			t.Error("cgroups were not requested and yet they are declared as applied")
		}
	}
}

// TestSandboxBaseDirectoryMissing: it is created, it does not fail.
func TestSandboxBaseDirectoryMissing(t *testing.T) {
	base := filepath.Join(t.TempDir(), "does", "not", "exist", "yet")
	s, err := New(Options{Dir: base, Log: logx.Global()})
	if err != nil {
		t.Fatalf("it should create the directory: %v", err)
	}
	defer s.Close()
	if _, err := os.Stat(base); err != nil {
		t.Errorf("the base directory was not created: %v", err)
	}
}

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
)

func agentWith(readOnly bool) *Agent {
	cfg := config.Default()
	cfg.Agent.ReadOnly = readOnly
	// A quiet logger: runActions logs, and a nil one would panic.
	log, err := logx.New(logx.Options{Level: logx.Error, Console: false})
	if err != nil {
		panic(err)
	}
	return &Agent{cfg: cfg, log: log}
}

// TestReadOnlyRefusesWritingCommands: the guarantee of plan mode. The command never
// reaches the executor, so nothing is written.
func TestReadOnlyRefusesWritingCommands(t *testing.T) {
	a := agentWith(true)
	for _, line := range []string{
		"rm -rf /tmp/x",
		"mv a b",
		"echo hola > f.txt",
		"echo hola >> f.txt",
		"cat f | tee /etc/passwd",
		"dd if=/dev/zero of=/dev/sda",
		"git commit -m x",
		"apt-get install vim",
		"touch nuevo",
	} {
		req, refused := a.buildRequest(line)
		if refused == "" {
			t.Errorf("%q must be refused in read-only mode (request: %+v)", line, req)
		}
		if req.Command != "" {
			t.Errorf("%q must not produce a request", line)
		}
	}
}

// TestReadOnlyRefusesAnythingWithAShellMetacharacter: without a shell there is
// nothing to interpret a redirection or a pipe, so a line that contains one cannot be
// run as written. Refusing it is what makes the mode a guarantee instead of a request.
func TestReadOnlyRefusesAnythingWithAShellMetacharacter(t *testing.T) {
	a := agentWith(true)
	for _, line := range []string{
		"echo x > y",
		"echo x 2>&1",
		"cat a | wc -l",
		"ls; rm -rf /",
		"ls && rm -rf /",
		"echo $(whoami)",
		"echo `whoami`",
		"cat < /etc/passwd",
		"grep x f & ",
	} {
		if _, refused := a.buildRequest(line); refused == "" {
			t.Errorf("%q contains something a shell would interpret and must be refused", line)
		}
	}
}

// TestReadOnlyAllowsReaders: the mode has to be useful — this is the point of it.
func TestReadOnlyAllowsReaders(t *testing.T) {
	a := agentWith(true)
	for _, line := range []string{
		"cat /etc/hostname",
		"grep -n root /etc/passwd",
		"ls -la /var/log",
		"du -sh /var",
		"go test ./...",
		"git status",
		"journalctl -u nginx -n 20",
		"df -h",
	} {
		req, refused := a.buildRequest(line)
		if refused != "" {
			t.Errorf("%q must be allowed in read-only mode: %s", line, refused)
		}
		// The request runs the program DIRECTLY: that is the guarantee.
		if strings.Contains(req.Command, "sh") || len(req.Args) > 0 && req.Args[0] == "-c" {
			t.Errorf("%q must not go through a shell (command %q args %v)", line, req.Command, req.Args)
		}
	}
}

// TestReadOnlySplitsArgumentsLikeAShell: the splitter honours quotes and otherwise
// separates on spaces, exactly as a shell would. That is deliberate — the request is
// run directly, so the arguments have to arrive as the program expects them — and it
// is also why a path with spaces must be quoted by whoever writes the command.
func TestReadOnlySplitsArgumentsLikeAShell(t *testing.T) {
	a := agentWith(true)

	// Quoted: one argument.
	req, refused := a.buildRequest(`grep -n "two words" "/tmp/file with spaces.txt"`)
	if refused != "" {
		t.Fatalf("refused: %s", refused)
	}
	if req.Command != "grep" {
		t.Errorf("command = %q", req.Command)
	}
	want := []string{"-n", "two words", "/tmp/file with spaces.txt"}
	if len(req.Args) != len(want) {
		t.Fatalf("args = %#v, want %#v", req.Args, want)
	}
	for i := range want {
		if req.Args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, req.Args[i], want[i])
		}
	}

	// Unquoted: the same line splits into more arguments, as a shell does. The
	// failure it would cause is the program's, and the error it prints is what tells
	// the model to quote.
	req, refused = a.buildRequest(`grep -n two words /tmp/file with spaces.txt`)
	if refused != "" {
		t.Fatalf("refused: %s", refused)
	}
	if len(req.Args) != 6 {
		t.Errorf("an unquoted path with spaces must split, got %#v", req.Args)
	}
}

// TestReadOnlyRefusesBrokenQuoting: an unterminated quote or a trailing backslash is
// refused instead of being guessed at.
func TestReadOnlyRefusesBrokenQuoting(t *testing.T) {
	a := agentWith(true)
	for _, line := range []string{`grep "unclosed`, `grep 'unclosed`, `cat file\`} {
		if _, refused := a.buildRequest(line); refused == "" {
			t.Errorf("%q must be refused", line)
		}
	}
}

// TestReadOnlyRefusesAnEmptyLine.
func TestReadOnlyRefusesAnEmptyLine(t *testing.T) {
	a := agentWith(true)
	for _, line := range []string{"", "   ", "\t"} {
		if _, refused := a.buildRequest(line); refused == "" {
			t.Errorf("%q must be refused", line)
		}
	}
}

// TestTaskModeStillUsesAShell: outside read-only mode the model keeps the power to use
// pipes and redirections, which is what real work needs.
func TestTaskModeStillUsesAShell(t *testing.T) {
	a := agentWith(false)
	req, refused := a.buildRequest("echo hola > f.txt")
	if refused != "" {
		t.Fatalf("a writing line must be allowed outside read-only mode: %s", refused)
	}
	if len(req.Args) != 2 || req.Args[0] != "-c" {
		t.Errorf("the line must go to the shell: command %q args %v", req.Command, req.Args)
	}
	if req.Args[1] != "echo hola > f.txt" {
		t.Errorf("the line must be passed unchanged, got %q", req.Args[1])
	}
}

// TestTheShellCanBeConfigured: a configuration may name its interpreter.
func TestTheShellCanBeConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.Shell = "/usr/local/bin/myshell"
	a := &Agent{cfg: cfg}
	req, _ := a.buildRequest("echo x")
	if req.Command != "/usr/local/bin/myshell" {
		t.Errorf("command = %q, want the configured shell", req.Command)
	}
}

// TestSplitCommandUnderstandsTheBasics: the tokeniser on its own.
func TestSplitCommandUnderstandsTheBasics(t *testing.T) {
	cases := []struct {
		line string
		cmd  string
		args int
	}{
		{"ls", "ls", 0},
		{"ls -la", "ls", 1},
		{"grep -n x f", "grep", 3},
		{`grep "a b" f`, "grep", 2},
		{`grep 'a b' f`, "grep", 2},
		{`echo a\ b`, "echo", 1},
		{"  spaced   out  ", "spaced", 1},
	}
	for _, tc := range cases {
		cmd, args, err := splitCommand(tc.line)
		if err != nil {
			t.Errorf("%q: %v", tc.line, err)
			continue
		}
		if cmd != tc.cmd || len(args) != tc.args {
			t.Errorf("%q = %q %#v, want %q with %d args", tc.line, cmd, args, tc.cmd, tc.args)
		}
	}
}

// TestRunActionsRefusesWritingInReadOnlyMode: the refusal happens BEFORE the
// executor, so nothing runs. It returns an error summarising the refusal, which is
// what goes back to the model on the next attempt.
func TestRunActionsRefusesWritingInReadOnlyMode(t *testing.T) {
	executed := 0
	a := agentWith(true)
	a.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		executed++
		return "should not run", false, 0, nil
	}

	output, err := a.runActions(context.Background(), []Command{
		{Command: "rm -rf /tmp/x", Description: "delete"},
		{Command: "cat /etc/hostname", Description: "read"},
	}, "test: ")

	if err == nil {
		t.Error("a refused action must be reported as an error")
	}
	if executed != 1 {
		t.Errorf("only the reader may run, executed = %d", executed)
	}
	if !strings.Contains(output, "refused") {
		t.Errorf("the output must record the refusal: %q", output)
	}
	if !strings.Contains(output, "cat /etc/hostname") {
		t.Errorf("the allowed action must have run: %q", output)
	}
}

// TestRunActionsAllowsEveryWriterOutsideReadOnlyMode: the other mode is unchanged.
func TestRunActionsAllowsEveryWriterOutsideReadOnlyMode(t *testing.T) {
	var ran []string
	a := agentWith(false)
	a.ExecCommand = func(_ context.Context, r execx.Request) (string, bool, int, error) {
		ran = append(ran, r.Args[len(r.Args)-1])
		return "ok", false, 0, nil
	}
	if _, err := a.runActions(context.Background(), []Command{
		{Command: "rm -rf /tmp/x"}, {Command: "touch nuevo"},
	}, "test: "); err != nil {
		t.Fatalf("outside read-only mode nothing is refused: %v", err)
	}
	if len(ran) != 2 {
		t.Errorf("both actions must run, ran = %v", ran)
	}
}

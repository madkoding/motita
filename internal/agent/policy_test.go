package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
)

// agentWith builds an agent for the policy tests. The workspace is a temporary directory
// so the "inside the workspace" rule is measured against something real and never against
// whatever directory the test happens to run in.
func agentWith(t *testing.T, readOnly bool) *Agent {
	t.Helper()
	cfg := config.Default()
	cfg.Agent.ReadOnly = readOnly
	cfg.Agent.WorkspaceDir = t.TempDir()
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
	a := agentWith(t, true)
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
		p := a.planRequest(line)
		if p.Verdict.String() != "deny" {
			t.Errorf("%q must be denied in read-only mode (got %s)", line, p.Verdict)
		}
		if p.Request.Command != "" {
			t.Errorf("%q must not produce a request", line)
		}
	}
}

// TestReadOnlyRefusesAnythingWithAShellMetacharacter: without a shell there is
// nothing to interpret a redirection or a pipe, so a line that contains one cannot be
// run as written. Refusing it is what makes the mode a guarantee instead of a request.
func TestReadOnlyRefusesAnythingWithAShellMetacharacter(t *testing.T) {
	a := agentWith(t, true)
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
		if p := a.planRequest(line); p.Verdict.String() != "deny" {
			t.Errorf("%q contains something a shell would interpret and must be denied", line)
		}
	}
}

// TestReadOnlyAllowsReaders: the mode has to be useful — this is the point of it.
func TestReadOnlyAllowsReaders(t *testing.T) {
	a := agentWith(t, true)
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
		p := a.planRequest(line)
		if p.Verdict.String() != "allow" {
			t.Errorf("%q must be allowed in read-only mode: %s", line, p.Reason)
		}
		// The request runs the program DIRECTLY: that is the guarantee.
		if strings.Contains(p.Request.Command, "sh") || len(p.Request.Args) > 0 && p.Request.Args[0] == "-c" {
			t.Errorf("%q must not go through a shell (command %q args %v)", line, p.Request.Command, p.Request.Args)
		}
	}
}

// TestReadOnlySplitsArgumentsLikeAShell: the splitter honours quotes and otherwise
// separates on spaces, exactly as a shell would. That is deliberate — the request is
// run directly, so the arguments have to arrive as the program expects them — and it
// is also why a path with spaces must be quoted by whoever writes the command.
func TestReadOnlySplitsArgumentsLikeAShell(t *testing.T) {
	a := agentWith(t, true)

	// Quoted: one argument.
	p := a.planRequest(`grep -n "two words" "/tmp/file with spaces.txt"`)
	if p.Verdict.String() != "allow" {
		t.Fatalf("denied: %s", p.Reason)
	}
	if p.Request.Command != "grep" {
		t.Errorf("command = %q", p.Request.Command)
	}
	want := []string{"-n", "two words", "/tmp/file with spaces.txt"}
	if len(p.Request.Args) != len(want) {
		t.Fatalf("args = %#v, want %#v", p.Request.Args, want)
	}
	for i := range want {
		if p.Request.Args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, p.Request.Args[i], want[i])
		}
	}

	// Unquoted: the same line splits into more arguments, as a shell does. The
	// failure it would cause is the program's, and the error it prints is what tells
	// the model to quote.
	p = a.planRequest(`grep -n two words /tmp/file with spaces.txt`)
	if p.Verdict.String() != "allow" {
		t.Fatalf("denied: %s", p.Reason)
	}
	if len(p.Request.Args) != 6 {
		t.Errorf("an unquoted path with spaces must split, got %#v", p.Request.Args)
	}
}

// TestReadOnlyRefusesBrokenQuoting: an unterminated quote or a trailing backslash is
// refused instead of being guessed at.
func TestReadOnlyRefusesBrokenQuoting(t *testing.T) {
	a := agentWith(t, true)
	for _, line := range []string{`grep "unclosed`, `grep 'unclosed`, `cat file\`} {
		if p := a.planRequest(line); p.Verdict.String() != "deny" {
			t.Errorf("%q must be refused", line)
		}
	}
}

// TestReadOnlyRefusesAnEmptyLine.
func TestReadOnlyRefusesAnEmptyLine(t *testing.T) {
	a := agentWith(t, true)
	for _, line := range []string{"", "   ", "\t"} {
		if p := a.planRequest(line); p.Verdict.String() != "deny" {
			t.Errorf("%q must be refused", line)
		}
	}
}

// TestTaskModeStillUsesAShell: outside read-only mode the model keeps the power to use
// pipes and redirections, which is what real work needs. What changed is that the line is
// now read before it is run: a line that writes INSIDE the workspace is allowed and still
// goes to the shell.
func TestTaskModeStillUsesAShell(t *testing.T) {
	a := agentWith(t, false)
	p := a.planRequest("echo hola > f.txt")
	if p.Verdict.String() != "allow" {
		t.Fatalf("a line writing inside the workspace must be allowed: %s (rule %s)", p.Reason, p.Rule)
	}
	if len(p.Request.Args) != 2 || p.Request.Args[0] != "-c" {
		t.Errorf("the line must go to the shell: command %q args %v", p.Request.Command, p.Request.Args)
	}
	if p.Request.Args[1] != "echo hola > f.txt" {
		t.Errorf("the line must be passed unchanged, got %q", p.Request.Args[1])
	}
}

// TestTheShellCanBeConfigured: a configuration may name its interpreter.
func TestTheShellCanBeConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.Shell = "/usr/local/bin/myshell"
	cfg.Agent.WorkspaceDir = t.TempDir()
	a := &Agent{cfg: cfg}
	p := a.planRequest("echo x")
	if p.Request.Command != "/usr/local/bin/myshell" {
		t.Errorf("command = %q, want the configured shell", p.Request.Command)
	}
}

// TestReadOnlyRunsTheProgramWithoutAShell: the guarantee of plan mode is not only that a
// writer is refused, but that an allowed line runs with NO interpreter. A quoted argument
// survives, and the request carries the program and its arguments rather than a shell line.
//
// The tokeniser itself is the shared one (`readonly.SplitCommand`), so its own edge cases are
// tested beside it; what is checked here is that the agent uses it and builds a direct request
// from it.
func TestReadOnlyRunsTheProgramWithoutAShell(t *testing.T) {
	a := agentWith(t, true)

	p := a.planRequest(`grep -n "dos palabras" f.txt`)
	if p.Verdict.String() != "allow" {
		t.Fatalf("a reader must be allowed in read-only mode, got %s: %s", p.Verdict, p.Reason)
	}
	if p.Request.Command != "grep" {
		t.Errorf("the program must be run directly, got %q", p.Request.Command)
	}
	if len(p.Request.Args) != 3 || p.Request.Args[1] != "dos palabras" {
		t.Errorf("the quoted argument must survive whole, got %#v", p.Request.Args)
	}
	// No interpreter: the shell is what would honour a redirection, and read-only mode does
	// not give it one.
	if strings.Contains(p.Request.Command, "sh") {
		t.Errorf("read-only mode must not run the line through a shell, got %q", p.Request.Command)
	}
}

// TestReadOnlyRefusesAnUnreadableLine: a line the tokeniser cannot read is refused with the
// tokeniser's own reason, so the model is told which character stopped it.
func TestReadOnlyRefusesAnUnreadableLine(t *testing.T) {
	a := agentWith(t, true)
	for _, line := range []string{`grep "sin cerrar`, `ls \`, `cat f |`, "", "   "} {
		p := a.planRequest(line)
		if p.Verdict.String() != "deny" {
			t.Errorf("%q cannot be run as written and must be denied, got %s", line, p.Verdict)
		}
		if p.Request.Command != "" {
			t.Errorf("%q must not produce a request, got %q", line, p.Request.Command)
		}
	}
}

// TestRunActionsRefusesWritingInReadOnlyMode: the refusal happens BEFORE the
// executor, so nothing runs. It returns an error summarising the refusal, which is
// what goes back to the model on the next attempt.
func TestRunActionsRefusesWritingInReadOnlyMode(t *testing.T) {
	executed := 0
	a := agentWith(t, true)
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

// TestTaskModeAsksBeforeWritingOutsideTheWorkspace: the behaviour this whole mechanism
// exists for. The command is NOT refused — it is put in front of the user, and with an
// approver that says yes it runs exactly as it would have.
func TestTaskModeAsksBeforeWritingOutsideTheWorkspace(t *testing.T) {
	var ran []string
	a := agentWith(t, false)
	a.ExecCommand = func(_ context.Context, r execx.Request) (string, bool, int, error) {
		ran = append(ran, r.Args[len(r.Args)-1])
		return "ok", false, 0, nil
	}
	var asked []string
	a.SetApprover(func(_ context.Context, req ApprovalRequest) (bool, error) {
		asked = append(asked, req.Command)
		return true, nil
	})

	outside := a.cfg.Agent.WorkspaceDir + "/../fuera"
	if _, err := a.runActions(context.Background(), []Command{
		{Command: "rm -rf " + outside},
	}, "test: "); err != nil {
		t.Fatalf("an approved action must run: %v", err)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "rm -rf") {
		t.Errorf("the user must be asked about the exact line, got %#v", asked)
	}
	if len(ran) != 1 {
		t.Errorf("the approved action must have run, ran = %#v", ran)
	}
}

// TestTaskModeDoesNotRunWhenTheUserDeclines: a no is a no. The command must not reach
// the executor, and the output has to say it was declined rather than refused, because
// those two send the model in different directions.
func TestTaskModeDoesNotRunWhenTheUserDeclines(t *testing.T) {
	executed := 0
	a := agentWith(t, false)
	a.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		executed++
		return "should not run", false, 0, nil
	}
	a.SetApprover(func(context.Context, ApprovalRequest) (bool, error) { return false, nil })

	output, err := a.runActions(context.Background(), []Command{
		{Command: "rm -rf " + a.cfg.Agent.WorkspaceDir + "/../fuera"},
	}, "test: ")
	if err == nil {
		t.Error("a declined action must be reported as an error")
	}
	if executed != 0 {
		t.Errorf("a declined action must not run, executed = %d", executed)
	}
	if !strings.Contains(output, "declined") {
		t.Errorf("the output must record the refusal to approve: %q", output)
	}
}

// TestTaskModeRefusesWhenThereIsNobodyToAsk: a script or a job has no user, so a command
// that needs approval has to be refused — running it would be deciding, on the user's
// behalf, the one thing the policy deliberately does not decide. The error has to say
// which situation it is, or the operator cannot tell it from a policy refusal.
func TestTaskModeRefusesWhenThereIsNobodyToAsk(t *testing.T) {
	executed := 0
	a := agentWith(t, false) // no approver
	a.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) {
		executed++
		return "should not run", false, 0, nil
	}

	output, err := a.runActions(context.Background(), []Command{
		{Command: "rm -rf " + a.cfg.Agent.WorkspaceDir + "/../fuera"},
	}, "test: ")
	if err == nil {
		t.Error("an unapprovable action must be reported as an error")
	}
	if executed != 0 {
		t.Errorf("an unapprovable action must not run, executed = %d", executed)
	}
	if !strings.Contains(output, "nobody to ask") {
		t.Errorf("the output must name the real problem: %q", output)
	}
}

// --- RunCommand: the three answers, through the public entry point of the interactive modes ---

// commandAgent is an agent in task mode (writing is the point) with a stubbed executor, which
// is what lets a test see whether a line reached the executor without running anything.
func commandAgent(t *testing.T, ran *[]string) *Agent {
	t.Helper()
	a := agentWith(t, false)
	a.ExecCommand = func(_ context.Context, r execx.Request) (string, bool, int, error) {
		if ran != nil {
			*ran = append(*ran, r.Args[len(r.Args)-1])
		}
		return "ok", false, 0, nil
	}
	return a
}

// TestRunCommandRefusesAMandatoryFloorCommand: the floor is answered before the approver, and
// an approver that says yes does not move it. This is the property the whole design rests on:
// a mandatory refusal is not a question.
func TestRunCommandRefusesAMandatoryFloorCommand(t *testing.T) {
	var ran []string
	a := commandAgent(t, &ran)
	asked := 0
	a.SetApprover(func(context.Context, ApprovalRequest) (bool, error) {
		asked++
		return true, nil
	})

	for _, line := range []string{"rm -rf /", "dd if=/dev/zero of=/dev/sda", "mkfs.ext4 /dev/sda"} {
		output, exit, err := a.RunCommand(context.Background(), line)
		if err == nil {
			t.Errorf("%q is on the mandatory floor and must be refused", line)
		}
		if exit == 0 {
			t.Errorf("%q must not report success", line)
		}
		if !strings.Contains(output, "[refused:") {
			t.Errorf("%q must record the refusal: %q", line, output)
		}
	}
	if asked != 0 {
		t.Errorf("the floor must not reach the user as a question, asked = %d", asked)
	}
	if len(ran) != 0 {
		t.Errorf("nothing on the floor may run, ran = %#v", ran)
	}
}

// TestRunCommandAsksAndRunsWhenApproved: a consequential action is not refused, it is put in
// front of the user — and with a yes it runs exactly as written.
func TestRunCommandAsksAndRunsWhenApproved(t *testing.T) {
	var ran []string
	a := commandAgent(t, &ran)
	var asked []ApprovalRequest
	a.SetApprover(func(_ context.Context, req ApprovalRequest) (bool, error) {
		asked = append(asked, req)
		return true, nil
	})

	line := "rm -rf " + a.cfg.Agent.WorkspaceDir + "/../fuera"
	if _, _, err := a.RunCommand(context.Background(), line); err != nil {
		t.Fatalf("an approved command must run: %v", err)
	}
	if len(asked) != 1 {
		t.Fatalf("the user must be asked exactly once, asked = %d", len(asked))
	}
	// The user approves the TEXT, so the text is what the question carries, along with why
	// and which rule produced the question.
	if asked[0].Command != line {
		t.Errorf("the question must carry the exact line, got %q", asked[0].Command)
	}
	if asked[0].Reason == "" || asked[0].Rule == "" {
		t.Errorf("the question must say why and by which rule: %#v", asked[0])
	}
	if len(ran) != 1 || !strings.Contains(ran[0], "fuera") {
		t.Errorf("the approved line must run, ran = %#v", ran)
	}
}

// TestRunCommandDoesNotRunWhenDeclined: a no is a no, and the output says "declined" rather
// than "refused" because those two send the model in different directions.
func TestRunCommandDoesNotRunWhenDeclined(t *testing.T) {
	var ran []string
	a := commandAgent(t, &ran)
	a.SetApprover(func(context.Context, ApprovalRequest) (bool, error) { return false, nil })

	output, exit, err := a.RunCommand(context.Background(), "rm -rf "+a.cfg.Agent.WorkspaceDir+"/../fuera")
	if err == nil {
		t.Error("a declined command must be reported as an error")
	}
	if exit == 0 {
		t.Error("a declined command must not report success")
	}
	if !strings.Contains(output, "declined") {
		t.Errorf("the output must distinguish a decline from a refusal: %q", output)
	}
	if len(ran) != 0 {
		t.Errorf("a declined command must not run, ran = %#v", ran)
	}
}

// TestRunCommandReportsAnApprovalThatCouldNotBeObtained: an approver that fails (the channel
// died, the user's terminal is gone) is a different answer from a decline, and the error must
// keep the cause.
func TestRunCommandReportsAnApprovalThatCouldNotBeObtained(t *testing.T) {
	var ran []string
	a := commandAgent(t, &ran)
	a.SetApprover(func(context.Context, ApprovalRequest) (bool, error) {
		return false, errors.New("the terminal went away")
	})

	output, _, err := a.RunCommand(context.Background(), "rm -rf "+a.cfg.Agent.WorkspaceDir+"/../fuera")
	if err == nil {
		t.Fatal("an approval that could not be obtained must be an error")
	}
	if !strings.Contains(err.Error(), "the terminal went away") {
		t.Errorf("the cause must survive the wrapping: %v", err)
	}
	if !strings.Contains(output, "[not approved:") {
		t.Errorf("the output must record it: %q", output)
	}
	if len(ran) != 0 {
		t.Errorf("nothing may run, ran = %#v", ran)
	}
}

// TestRunCommandWithoutAnApproverRefuses: a run with nobody at the other end cannot ask, and
// running anyway would be deciding on the user's behalf the one thing the policy does not
// decide.
func TestRunCommandWithoutAnApproverRefuses(t *testing.T) {
	var ran []string
	a := commandAgent(t, &ran) // no approver installed

	output, _, err := a.RunCommand(context.Background(), "rm -rf "+a.cfg.Agent.WorkspaceDir+"/../fuera")
	if err == nil {
		t.Fatal("with nobody to ask the command must be refused")
	}
	if !strings.Contains(output, "nobody to ask") {
		t.Errorf("the output must name the real problem: %q", output)
	}
	if len(ran) != 0 {
		t.Errorf("nothing may run, ran = %#v", ran)
	}
}

// TestRunCommandRunsOrdinaryWorkWithoutAsking: the mechanism must not turn work into an
// interrogation. A write inside the workspace, and a reader, run silently.
func TestRunCommandRunsOrdinaryWorkWithoutAsking(t *testing.T) {
	var ran []string
	a := commandAgent(t, &ran)
	asked := 0
	a.SetApprover(func(context.Context, ApprovalRequest) (bool, error) {
		asked++
		return true, nil
	})

	for _, line := range []string{"ls -la", "echo hola > dentro.txt", "go test ./..."} {
		if _, _, err := a.RunCommand(context.Background(), line); err != nil {
			t.Errorf("%q is ordinary work and must run: %v", line, err)
		}
	}
	if asked != 0 {
		t.Errorf("ordinary work must not be questioned, asked = %d", asked)
	}
	if len(ran) != 3 {
		t.Errorf("every line must have reached the executor, ran = %#v", ran)
	}
}

// --- runConfigured: the operator's own commands ---

// TestRunConfiguredRefusesTheMandatoryFloor: an operator's configured command is trusted, but
// trust does not extend to the floor. An operator does not get to `mkfs` their own filesystem
// either, and the configured line is analysed exactly like a model's.
func TestRunConfiguredRefusesTheMandatoryFloor(t *testing.T) {
	var ran []string
	a := commandAgent(t, &ran)
	a.SetApprover(func(context.Context, ApprovalRequest) (bool, error) { return true, nil })

	output, exit, err := a.runConfigured(context.Background(),
		execx.Request{Command: shellFor(a.cfg), Args: []string{"-c", "rm -rf /"}}, "rm -rf /")
	if err == nil {
		t.Fatal("a configured command on the floor must be refused")
	}
	if exit == 0 {
		t.Error("it must not report success")
	}
	if !strings.Contains(err.Error(), "refused by the policy") {
		t.Errorf("the error must say the policy refused it: %v", err)
	}
	if output != "" {
		t.Errorf("a refused command produces no output, got %q", output)
	}
	if len(ran) != 0 {
		t.Errorf("nothing may run, ran = %#v", ran)
	}
}

// TestRunConfiguredRunsAConsequentialLineWithoutAsking: the operator WROTE this line, so the
// answer that would be a question for a model is not a question here. A prompt with no user at
// the keyboard would break the very feature meant to protect it.
func TestRunConfiguredRunsAConsequentialLineWithoutAsking(t *testing.T) {
	var ran []string
	a := commandAgent(t, &ran)
	asked := 0
	a.SetApprover(func(context.Context, ApprovalRequest) (bool, error) {
		asked++
		return false, nil // a "no" that must never be consulted
	})

	line := "rm -rf " + a.cfg.Agent.WorkspaceDir + "/../fuera"
	output, exit, err := a.runConfigured(context.Background(),
		execx.Request{Command: shellFor(a.cfg), Args: []string{"-c", line}}, line)
	if err != nil {
		t.Fatalf("the operator's own consequential line must run: %v", err)
	}
	if exit != 0 {
		t.Errorf("exit = %d, want 0", exit)
	}
	if output != "ok" {
		t.Errorf("output = %q", output)
	}
	if asked != 0 {
		t.Errorf("the operator's own line must not be turned into a question, asked = %d", asked)
	}
	if len(ran) != 1 {
		t.Errorf("it must have run once, ran = %#v", ran)
	}
	// The request is used as written: an interpreter with its own flags keeps working.
	if !strings.Contains(ran[0], line) {
		t.Errorf("the configured line must reach the executor unchanged, ran = %#v", ran)
	}
}

// --- policyDir: the directory "inside the workspace" is measured against ---

// TestPolicyDirIsAbsolute: the directory is resolved in the PARENT, while the working
// directory the configuration was written for is still in effect. A relative path handed to a
// child would be resolved twice, in two processes, and measure the wrong directory.
func TestPolicyDirIsAbsolute(t *testing.T) {
	a := agentWith(t, false)

	// A configured directory is made absolute.
	a.cfg.Agent.WorkspaceDir = "sub/dir"
	got := a.policyDir()
	if !filepath.IsAbs(got) {
		t.Errorf("a configured workspace must be resolved to an absolute path, got %q", got)
	}
	if !strings.HasSuffix(got, filepath.Join("sub", "dir")) {
		t.Errorf("the configured path must be preserved, got %q", got)
	}

	// And so is the default, which is the working directory.
	for _, empty := range []string{"", "  ", "."} {
		a.cfg.Agent.WorkspaceDir = empty
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if got := a.policyDir(); got != wd {
			t.Errorf("with %q the workspace is the working directory: got %q, want %q", empty, got, wd)
		}
	}
}

// TestPolicyDirWhenThereIsNoUsableWorkingDirectory: the two fallbacks, reached by deleting the
// directory the process is standing in so that resolving "." has nowhere to resolve to.
//
// It is a real situation rather than a contrived one — a long run whose working directory is
// removed under it, which is exactly what a `t.TempDir()` of a finished job looks like — and
// the answer must be a usable relative path instead of an empty string. An empty workspace
// would make every target look outside of it, turning the policy into a refusal of everything.
func TestPolicyDirWhenThereIsNoUsableWorkingDirectory(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// The restore is deferred BEFORE anything moves, so a failure below cannot leave the rest
	// of the package running in a directory that no longer exists.
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("could not return to %q: %v", previous, err)
		}
	}()

	doomed := t.TempDir()
	if err := os.Chdir(doomed); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(doomed); err != nil {
		t.Skipf("this filesystem does not let its working directory be removed: %v", err)
	}
	// From here the process stands in a directory that is gone, and `os.Getwd` fails.

	a := agentWith(t, false)
	a.cfg.Agent.WorkspaceDir = ""
	if got := a.policyDir(); got != "." {
		t.Errorf("with no usable working directory the answer is a relative path, got %q", got)
	}

	// And the same for a configured path that cannot be made absolute.
	a.cfg.Agent.WorkspaceDir = "sub/dir"
	if got := a.policyDir(); got != "sub/dir" {
		t.Errorf("an unresolvable configured path is returned as written, got %q", got)
	}
}

// TestTaskModeRunsInsideTheWorkspaceWithoutAsking: the policy must not turn ordinary
// work into an interrogation. A write inside the directory the user pointed the agent at
// runs silently.
func TestTaskModeRunsInsideTheWorkspaceWithoutAsking(t *testing.T) {
	var ran []string
	a := agentWith(t, false)
	a.ExecCommand = func(_ context.Context, r execx.Request) (string, bool, int, error) {
		ran = append(ran, r.Args[len(r.Args)-1])
		return "ok", false, 0, nil
	}
	a.SetApprover(func(_ context.Context, req ApprovalRequest) (bool, error) {
		t.Errorf("ordinary work inside the workspace must not be questioned: %s", req.Command)
		return false, nil
	})

	for _, line := range []string{"touch nuevo.txt", "rm -rf ./build", "mkdir -p ./a/b"} {
		if _, err := a.runActions(context.Background(), []Command{{Command: line}}, "test: "); err != nil {
			t.Errorf("%q must run: %v", line, err)
		}
	}
	if len(ran) != 3 {
		t.Errorf("all three must have run, ran = %#v", ran)
	}
}

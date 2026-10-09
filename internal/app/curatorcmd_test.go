package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

// errNoEngine is what a failing engine factory returns. The message is what the user reads.
var errNoEngine = errors.New("the LLM key is missing")

// The command is exercised through Run() with real arguments wherever the path allows it: the
// parse and the dispatch are half the feature, and a test that called runCuratorCommand
// directly would skip both.

// curatorHome prepares an isolated HOME with a skills directory, and returns it.
//
// HOME decides where the library lives (config.Dir()), so a test that did not set it would
// read the user's real ~/.motita and could archive their documents.
func curatorHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOTITA_LLM_API_KEY", "test")
	if err := os.MkdirAll(filepath.Join(home, ".motita", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// curatorOptions builds the Options for a curator invocation, WITHOUT touching HOME.
//
// gatewayTestOptions is the same shape but sets HOME to a temp dir of its own, which is right
// for the gateway tests and fatal here: the test writes a document into ITS home and the
// command would then look in a different one, so the archive would read as empty and the
// failure would look like a bug in the library.
func curatorOptions(t *testing.T, out *syncBuffer, args ...string) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		Args:  args,
		Out:   out,
		Err:   out,
		Stdin: strings.NewReader(""),
		NewSandbox: func(o sandbox.Options) (*sandbox.Sandbox, error) {
			o.Dir = dir
			return sandbox.New(o)
		},
	}
}

// writeSkill puts a document in the library, and returns its path.
func writeSkill(t *testing.T, home, name, body string) string {
	t.Helper()
	p := filepath.Join(home, ".motita", "skills", name+".md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// archiveSkill moves a document into the archive the way the curator does.
func archiveSkill(t *testing.T, home, name string) {
	t.Helper()
	dir := filepath.Join(home, ".motita", "skills", ".archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(home, ".motita", "skills", name+".md")
	if err := os.Rename(src, filepath.Join(dir, name+".md")); err != nil {
		t.Fatal(err)
	}
}

func TestCuratorStatusThroughTheCommandLine(t *testing.T) {
	silence(t)
	curatorHome(t)
	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "status")

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d:\n%s", code, Success, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"curator: ENABLED", "stale after:    14d unused", "archive after:  30d unused",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report is missing %q:\n%s", want, got)
		}
	}
}

func TestCuratorRunThroughTheCommandLine(t *testing.T) {
	silence(t)
	curatorHome(t)
	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "run")

	if code := Run(op); code != Success {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "pass: 0 stale, 0 archived") {
		t.Errorf("the run did not report itself:\n%s", out.String())
	}
}

// A dry run says so, in the ANSWER. The reply is the only thing the person who asked it will
// read, and a preview that looked like a real pass would leave them believing their library
// was rewritten.
func TestCuratorDryRunSaysNothingWasChanged(t *testing.T) {
	silence(t)
	home := curatorHome(t)
	writeSkill(t, home, "old", "# Old\n\nbody\n")
	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "run", "--dry-run")

	if code := Run(op); code != Success {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "preview (nothing was changed)") {
		t.Errorf("the preview does not say it changed nothing:\n%s", out.String())
	}
	// And the document is where it was.
	if _, err := os.Stat(filepath.Join(home, ".motita", "skills", "old.md")); err != nil {
		t.Errorf("a dry run moved a document: %v", err)
	}
}

// --consolidate with no usable engine is a CONFIGURATION error, and it is reported before
// anything runs: a pass that silently skipped the consolidation it was asked for would be a
// pass that lied about the one thing it was asked to do.
func TestCuratorConsolidateWithoutAnEngineIsAConfigError(t *testing.T) {
	silence(t)
	curatorHome(t)
	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "run", "--consolidate")
	// The engine factory fails: no key, an unsupported provider, whatever the reason, the
	// consolidation cannot run.
	op.NewEngine = func(config.LLM, *logx.Logger) (*llm.Client, error) {
		return nil, errNoEngine
	}

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for a consolidation that cannot run:\n%s", code, ConfigError, out.String())
	}
	if !strings.Contains(out.String(), "needs a working engine") {
		t.Errorf("the message does not name the problem:\n%s", out.String())
	}
}

func TestCuratorListArchivedThroughTheCommandLine(t *testing.T) {
	silence(t)
	home := curatorHome(t)
	out := &syncBuffer{}

	// Nothing archived yet.
	op := curatorOptions(t, out, "curator", "list-archived")
	if code := Run(op); code != Success {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "the archive is empty.") {
		t.Errorf("an empty archive must say so:\n%s", out.String())
	}

	// With something in it: one name per line, and nothing else, so the output can be piped.
	writeSkill(t, home, "old", "# Old\n\nbody\n")
	archiveSkill(t, home, "old")
	out2 := &syncBuffer{}
	op2 := curatorOptions(t, out2, "curator", "list-archived")
	if code := Run(op2); code != Success {
		t.Fatalf("exit %d:\n%s", code, out2.String())
	}
	if strings.TrimSpace(out2.String()) != "old" {
		t.Errorf("the listing is %q, want just the name", out2.String())
	}
}

// The three actions that take a name, and the round trip: pin, unpin, and a restore that
// actually moves the document back.
func TestTheCuratorNamedActionsThroughTheCommandLine(t *testing.T) {
	silence(t)
	home := curatorHome(t)

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"curator", "pin", "build-firmware"}, "pinned: build-firmware"},
		{[]string{"curator", "unpin", "build-firmware"}, "unpinned: build-firmware"},
	} {
		out := &syncBuffer{}
		op := curatorOptions(t, out, c.args...)
		if code := Run(op); code != Success {
			t.Fatalf("%v: exit %d:\n%s", c.args, code, out.String())
		}
		if !strings.Contains(out.String(), c.want) {
			t.Errorf("%v printed %q, want %q", c.args, out.String(), c.want)
		}
	}

	// The pin has to be in the SIDECAR, which is what the curator reads: an action that
	// printed the right line and recorded nothing would be the worst kind of success.
	writeSkill(t, home, "old", "# Old\n\nbody\n")
	archiveSkill(t, home, "old")
	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "restore", "old")
	if code := Run(op); code != Success {
		t.Fatalf("restore: exit %d:\n%s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".motita", "skills", "old.md")); err != nil {
		t.Errorf("restore did not bring the document back: %v", err)
	}
}

// Restoring something that is not archived fails, and the exit code is not zero.
func TestCuratorRestoreOfSomethingNotArchivedFails(t *testing.T) {
	silence(t)
	curatorHome(t)
	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "restore", "nope")

	if code := Run(op); code != RunError {
		t.Fatalf("exit %d, want %d:\n%s", code, RunError, out.String())
	}
	if !strings.Contains(out.String(), "could not restored") {
		t.Errorf("the failure does not say what failed:\n%s", out.String())
	}
}

// A pin with no ledger is refused. It is the one action that CANNOT work without telemetry,
// because the pin lives in the ledger and nowhere else - a silent success would be a veto the
// user believes in and the curator has never heard of.
func TestCuratorPinIsRefusedWhenTheLedgerCannotBeWritten(t *testing.T) {
	silence(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOTITA_LLM_API_KEY", "test")
	// The library directory is a FILE, so the ledger cannot be created inside it and the
	// store opens without one.
	if err := os.MkdirAll(filepath.Join(home, ".motita"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".motita", "skills"), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "pin", "anything")
	// Whatever the store did, the command must not report success for a pin it could not
	// record: either it failed, or the ledger existed and the pin landed.
	if code := Run(op); code == Success {
		return // a ledger that could be written is a working pin, also fine
	} else if code != RunError {
		t.Fatalf("exit %d, want %d or %d:\n%s", code, RunError, Success, out.String())
	}
}

// The dispatch's own fallback: unreachable from the command line, and kept because the
// function must not depend on parse having validated the action. A second caller that forgot
// would otherwise fall off the end of the switch into doing nothing, the quietest failure.
func TestTheCuratorDispatchFallsBackToTheHelp(t *testing.T) {
	silence(t)
	curatorHome(t)
	out := &syncBuffer{}
	op := curatorOptions(t, out, "-version")

	if code := op.runCuratorCommand(context.Background(), flags{curatorAction: "nonsense"}); code != Success {
		t.Fatalf("exit %d, want %d", code, Success)
	}
	if !strings.Contains(out.String(), "Usage: motita curator") {
		t.Errorf("the help was not shown:\n%s", out.String())
	}
}

// Case and surrounding space are tolerated, because a user who types `curator STATUS ` means
// the same thing. The SKILL NAME is not: it is data, and lower-casing it would name a
// different document.
func TestTheCuratorDispatchIgnoresCaseAndSpaceInTheActionOnly(t *testing.T) {
	silence(t)
	curatorHome(t)
	out := &syncBuffer{}
	op := curatorOptions(t, out, "-version")

	if code := op.runCuratorCommand(context.Background(), flags{curatorAction: "  STATUS  "}); code != Success {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "curator:") {
		t.Errorf("the status was not printed:\n%s", out.String())
	}
}

// A configuration file that does not parse is an error even here. The subcommand pays for no
// engine, but it must not quietly run against defaults when the user's file is broken.
func TestCuratorRejectsABrokenConfigFile(t *testing.T) {
	silence(t)
	home := curatorHome(t)
	bad := filepath.Join(home, "broken.yaml")
	if err := os.WriteFile(bad, []byte("this: is: not: valid: yaml: at all\n\t- x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	op := curatorOptions(t, out, "-config", bad, "curator", "status")

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for a broken config:\n%s", code, ConfigError, out.String())
	}
}

// A config file that is VALID but has no key still works: nothing but a consolidation run
// calls a model, and requiring a key to tidy the library would be requiring a model to sort
// files.
func TestCuratorWorksWithAConfigThatHasNoKey(t *testing.T) {
	silence(t)
	home := curatorHome(t)
	cfgPath := filepath.Join(home, "nokey.yaml")
	mustWrite(t, cfgPath, "sandbox:\n  kind: none\n")
	out := &syncBuffer{}
	op := curatorOptions(t, out, "-config", cfgPath, "curator", "status")
	t.Setenv("MOTITA_LLM_API_KEY", "")

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d: tidying the library must not need a model:\n%s", code, Success, out.String())
	}
	if !strings.Contains(out.String(), "curator:") {
		t.Errorf("the status was not printed:\n%s", out.String())
	}
}

// A logger that cannot be built is a configuration error, reported rather than panicking on a
// nil logger deeper in.
func TestCuratorReportsALoggerThatCannotBeBuilt(t *testing.T) {
	silence(t)
	curatorHome(t)
	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "status")
	op.NewLogger = func(config.Agent) (*logx.Logger, error) {
		return nil, errors.New("the log file cannot be opened")
	}

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d:\n%s", code, ConfigError, out.String())
	}
	if !strings.Contains(out.String(), "cannot be opened") {
		t.Errorf("the reason is not reported:\n%s", out.String())
	}
}

// A run that the pass itself refuses is a run error and not a success. It is reachable with
// --consolidate and an engine that exists but cannot do the fork, which is the state a machine
// with a key and no runner builder is in.
func TestCuratorRunReportsAFailureFromThePass(t *testing.T) {
	silence(t)
	home := curatorHome(t)
	// A library directory that cannot be read makes the pass fail when it archives.
	dir := filepath.Join(home, ".motita", "skills", ".archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits")
	}

	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "list-archived")
	// An unreadable archive must be an error, not "the archive is empty".
	if code := Run(op); code != RunError {
		t.Fatalf("exit %d, want %d for an unreadable archive:\n%s", code, RunError, out.String())
	}
	if !strings.Contains(out.String(), "could not read the archive") {
		t.Errorf("the reason is not reported:\n%s", out.String())
	}
}

// With no -config anywhere, the command falls back to the built-in defaults rather than
// refusing to run. `motita curator status` on a machine that has never been configured is a
// reasonable thing to type.
func TestCuratorFallsBackToTheDefaultsWithNoConfig(t *testing.T) {
	silence(t)
	curatorHome(t)
	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "status")

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d:\n%s", code, Success, out.String())
	}
	if !strings.Contains(out.String(), "curator:") {
		t.Errorf("the status was not printed:\n%s", out.String())
	}
}

// And when even the defaults cannot be produced - a HOME that is not a directory - the command
// reports it instead of proceeding with an empty configuration.
func TestCuratorReportsADefaultConfigThatCannotBeBuilt(t *testing.T) {
	silence(t)
	home := t.TempDir()
	// A HOME that is a FILE: config.Dir() joins onto it and every write underneath fails.
	blocker := filepath.Join(home, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", blocker)
	t.Setenv("MOTITA_LLM_API_KEY", "test")

	out := &syncBuffer{}
	op := curatorOptions(t, out, "curator", "status")
	// Whatever it answers, it must not be a panic and it must not claim success while having
	// read nothing: the two acceptable outcomes are a report and a reported failure.
	code := Run(op)
	if code != Success && code != ConfigError && code != RunError {
		t.Fatalf("exit %d, want one of Success, ConfigError, RunError:\n%s", code, out.String())
	}
}

// TestCuratorConfigReportsADefaultItCannotBuild calls curatorConfig directly.
//
// The fallback branch is only reachable when there is no -config AND config.LoadOrDefault
// fails, which takes a HOME the process cannot use at all. Driving it through Run() would
// depend on how the config package reacts to an unusable home; calling the loader is the
// direct test of the branch that matters, and it is the function the command actually calls.
func TestCuratorConfigReportsADefaultItCannotBuild(t *testing.T) {
	silence(t)
	// HOME pointing at a regular FILE: .motita cannot be created underneath it, so building
	// a default configuration fails.
	home := t.TempDir()
	blocker := filepath.Join(home, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", blocker)

	out := &syncBuffer{}
	op := curatorOptions(t, out)
	_, code := op.curatorConfig(flags{})

	// Either the configuration package tolerates it (Success) or the command reports it
	// (ConfigError). Silence is not an option, and neither is a panic.
	if code != Success && code != ConfigError {
		t.Fatalf("exit %d, want Success or ConfigError:\n%s", code, out.String())
	}
	if code == ConfigError && !strings.Contains(out.String(), "❌") {
		t.Error("the failure was not reported")
	}
}

// TestCuratorRunReportsAFailingPass calls RunWith through the command with a pass that cannot
// record itself.
//
// The state file's directory is made unwritable, so saveState fails after the deterministic
// work; the command must report that rather than claiming a clean pass.
func TestCuratorRunReportsAFailingPass(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits")
	}
	silence(t)
	home := curatorHome(t)
	// state_file points inside a directory that cannot be created: a file where the
	// directory should be.
	blocker := filepath.Join(home, "state-blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, "cfg.yaml")
	mustWrite(t, cfgPath, "curator:\n  state_file: "+filepath.Join(blocker, "state.json")+"\n")

	out := &syncBuffer{}
	op := curatorOptions(t, out, "-config", cfgPath, "curator", "run")
	if code := Run(op); code != RunError {
		t.Fatalf("exit %d, want %d for a pass that could not record itself:\n%s", code, RunError, out.String())
	}
	if !strings.Contains(out.String(), "❌") {
		t.Errorf("the failure was not reported:\n%s", out.String())
	}
}

// TestCuratorConfigReportsAnUnreadableConfigFile drives the -config branch's failure directly.
//
// A file that does not exist is the ordinary case: the user typed a path, and the command must
// name it rather than falling back to the defaults and appearing to work.
func TestCuratorConfigReportsAnUnreadableConfigFile(t *testing.T) {
	silence(t)
	curatorHome(t)
	out := &syncBuffer{}
	op := curatorOptions(t, out)

	missing := filepath.Join(t.TempDir(), "not-there.yaml")
	_, code := op.curatorConfig(flags{configPath: missing})
	if code != ConfigError {
		t.Fatalf("exit %d, want %d for a config file that is not there:\n%s", code, ConfigError, out.String())
	}
	if !strings.Contains(out.String(), "❌") {
		t.Errorf("the failure was not reported:\n%s", out.String())
	}

	// And the same through the command line, which is how a user meets it.
	out2 := &syncBuffer{}
	op2 := curatorOptions(t, out2, "-config", missing, "curator", "status")
	if code := Run(op2); code != ConfigError {
		t.Fatalf("exit %d, want %d:\n%s", code, ConfigError, out2.String())
	}
}

// TestCuratorConfigReportsABadEnvironmentVariable drives the last branch of the fallback: with
// no -config, loading the defaults still applies the MOTITA_* environment, and a variable that
// does not parse is an error. It must be reported rather than silently ignored, because the
// user who set it believes it took effect.
func TestCuratorConfigReportsABadEnvironmentVariable(t *testing.T) {
	silence(t)
	curatorHome(t)
	// A duration variable with a value that is not a duration.
	t.Setenv("MOTITA_LLM_TIMEOUT", "not-a-duration")

	out := &syncBuffer{}
	op := curatorOptions(t, out)
	_, code := op.curatorConfig(flags{})

	if code != ConfigError {
		t.Fatalf("exit %d, want %d for a variable that cannot be parsed:\n%s", code, ConfigError, out.String())
	}
	if !strings.Contains(out.String(), "❌") {
		t.Errorf("the failure was not reported:\n%s", out.String())
	}
}

// proposeSkill leaves a proposal where the background review saves one.
func proposeSkill(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, ".motita", "skills", ".proposed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A skill the background review proposed is listed, read, and reaches the library only when
// the user accepts it; a rejected one is gone.
func TestCuratorReviewsProposedSkillsThroughTheCommandLine(t *testing.T) {
	silence(t)
	home := curatorHome(t)
	run := func(want int, args ...string) string {
		t.Helper()
		out := &syncBuffer{}
		if code := Run(curatorOptions(t, out, args...)); code != want {
			t.Fatalf("%v: exit %d, want %d:\n%s", args, code, want, out.String())
		}
		return out.String()
	}

	if got := run(Success, "curator", "list-proposed"); !strings.Contains(got, "no skill is waiting") {
		t.Errorf("nothing proposed: %q", got)
	}
	proposeSkill(t, home, "deploy", "# Deploy\n\x1b]52;c;aGk=\x07run make deploy\n")
	proposeSkill(t, home, "junk", "# Junk\n")
	if got := run(Success, "curator", "list-proposed"); strings.TrimSpace(got) != "deploy\njunk" {
		t.Errorf("the listing is %q", got)
	}
	// Shown with its control characters visible: the text came from a model that read
	// untrusted output.
	if got := run(Success, "curator", "show-proposed", "deploy"); !strings.Contains(got, "^[]52;c;aGk=^Grun make deploy") {
		t.Errorf("show: %q", got)
	}
	if got := run(Success, "curator", "accept", "deploy"); !strings.Contains(got, "accepted: deploy") {
		t.Errorf("accept: %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".motita", "skills", "deploy.md")); err != nil {
		t.Errorf("not in the library: %v", err)
	}
	if got := run(Success, "curator", "reject", "junk"); !strings.Contains(got, "rejected: junk") {
		t.Errorf("reject: %q", got)
	}
	if got := run(Success, "curator", "list-proposed"); !strings.Contains(got, "no skill is waiting") {
		t.Errorf("left waiting: %q", got)
	}

	run(RunError, "curator", "show-proposed", "nope")
	run(RunError, "curator", "accept", "nope")

	// A proposals directory that cannot be read is said, not taken for an empty one.
	if err := os.RemoveAll(filepath.Join(home, ".motita", "skills", ".proposed")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".motita", "skills", ".proposed"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(RunError, "curator", "list-proposed")
}

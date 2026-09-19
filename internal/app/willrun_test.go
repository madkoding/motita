package app

import (
	"testing"

	"github.com/madkoding/starlight/internal/config"
)

// willRunTUI answers one question — does this invocation end in the conversational interface?
// — and it is asked in two places that must agree: the logger is silenced early, and the
// interface is launched later. These tests fix every answer.
//
// The bug this replaces was not a wrong answer but a duplicated one: two predicates that had
// drifted, so the interface was entered on a path where the logger had not been silenced and
// raw JSON lines landed in the middle of the chat.

func runTUIFor(fl flags, args ...string) bool {
	return Options{Args: args}.willRunTUI(fl)
}

// TestTheBareInvocationRunsTheInterface: no flags, no arguments, nothing to do — the user
// opened the program, so it opens the chat.
func TestTheBareInvocationRunsTheInterface(t *testing.T) {
	if !runTUIFor(flags{}) {
		t.Error("a bare invocation must start the interface")
	}
}

// TestTheExplicitFlagRunsTheInterface: asking for it by name always works.
func TestTheExplicitFlagRunsTheInterface(t *testing.T) {
	if !runTUIFor(flags{tui: true}) {
		t.Error("-tui must start the interface")
	}
}

// TestEveryExplicitRequestRulesOutTheInterface: each of these produces its own output, and
// starting the chat instead would swallow the result the user asked for.
func TestEveryExplicitRequestRulesOutTheInterface(t *testing.T) {
	for _, tc := range []struct {
		name string
		fl   flags
		args []string
	}{
		{"a configuration file", flags{configPath: "starlight.yaml"}, nil},
		{"a task", flags{task: "count the files"}, nil},
		{"a task file", flags{taskFile: "tasks.txt"}, nil},
		{"a configuration check", flags{validateConfig: true}, nil},
		{"an isolation run", flags{isolation: true}, nil},
		{"the version", flags{version: true}, nil},
		{"the first-run wizard", flags{initConfig: true}, nil},
		{"a positional argument", flags{}, []string{"something"}},
	} {
		if runTUIFor(tc.fl, tc.args...) {
			t.Errorf("%s must not start the interface", tc.name)
		}
	}
}

// TestTheExplicitFlagBeatsEverythingElse: -tui is the user saying it outright. Even combined
// with something that would otherwise rule it out, the request stands — the other flags then
// shape what the interface does rather than replacing it.
func TestTheExplicitFlagBeatsEverythingElse(t *testing.T) {
	fl := flags{tui: true, configPath: "starlight.yaml", task: "count"}

	if !runTUIFor(fl) {
		t.Error("-tui must win over the flags that would otherwise rule the interface out")
	}
}

// TestTheLoggerIsSilencedExactlyWhenTheInterfaceRuns: the two call sites have to agree, and
// this is the property that failed before — not the value of either predicate on its own but
// their agreement.
func TestTheLoggerIsSilencedExactlyWhenTheInterfaceRuns(t *testing.T) {
	for _, fl := range []flags{
		{}, {tui: true}, {configPath: "x"}, {task: "t"}, {taskFile: "f"},
		{validateConfig: true}, {isolation: true}, {version: true}, {initConfig: true},
	} {
		for _, args := range [][]string{nil, {"a"}} {
			op := Options{Args: args}
			// Both decisions come from the same function by construction, so the assertion is
			// that calling it twice — the way the two call sites do — gives one answer.
			first, second := op.willRunTUI(fl), op.willRunTUI(fl)
			if first != second {
				t.Errorf("the predicate answered %v then %v for %+v", first, second, fl)
			}
			// And that the answer is the documented one for this combination.
			want := fl.tui || (fl.configPath == "" && fl.task == "" && fl.taskFile == "" &&
				!fl.validateConfig && !fl.isolation && !fl.version && !fl.initConfig && len(args) == 0)
			if first != want {
				t.Errorf("willRunTUI(%+v, args=%v) = %v, want %v", fl, args, first, want)
			}
		}
	}
}

// TestTheSilencedLoggerKeepsWritingToTheFile: turning the console log off must not turn the
// file log off. A user who reports a problem needs the file to have the run in it.
func TestTheSilencedLoggerKeepsWritingToTheFile(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.LogConsole = false
	if cfg.Agent.LogFile == "" {
		t.Fatal("the default must keep a file to log to")
	}
	if LogPath(cfg) == "stderr" {
		t.Error("a configured log file must still be reported, not stderr")
	}
}

// TestTheInterfaceNeverSilencesItsOnlyLog: the console is turned off when the interface
// starts, so a configuration with no log file would leave the run with no record at all — and
// the user most likely to need that record is the one in the chat, reporting a problem.
//
// The default therefore names a file, and this asserts the property rather than the path: any
// default that keeps the console on when there is nothing else to write to is fine, and one
// that goes silent into nowhere is not.
func TestTheInterfaceNeverSilencesItsOnlyLog(t *testing.T) {
	cfg := config.Default()
	if cfg.Agent.LogFile == "" && !cfg.Agent.LogConsole {
		t.Error("silencing the console with no log file leaves no record of the run")
	}
	if cfg.Agent.LogFile == "" {
		t.Errorf("the default must name a log file, so a chat can be reported on")
	}
}

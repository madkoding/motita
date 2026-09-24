package app

import (
	"strings"
	"testing"
)

// `motita gateway start` was rejected as an unknown flag before this: the parser is a hand-
// written loop over arguments that knows only about flags, and a subcommand is a POSITIONAL
// argument. The subcommand is therefore read before that loop.

func TestTheGatewaySubcommandIsRecognised(t *testing.T) {
	cases := []struct {
		args   []string
		action string
	}{
		{[]string{"gateway", "start"}, "start"},
		{[]string{"gateway", "stop"}, "stop"},
		{[]string{"gateway", "status"}, "status"},
		// Recorded as typed, not lowercased: what the user wrote is what the process reports.
		{[]string{"gateway", "START"}, "START"},
	}
	for _, c := range cases {
		fl, err := parse(c.args)
		if err != nil {
			t.Errorf("parse(%v): %v", c.args, err)
			continue
		}
		if fl.gatewayAction != c.action {
			t.Errorf("parse(%v) produced action %q, want %q", c.args, fl.gatewayAction, c.action)
		}
	}
}

// `motita -config x gateway start` has to keep reading the configuration, because the service
// listens where that file says. A subcommand that swallowed the flags before it would start a
// gateway on the wrong address - and the user would have no way to tell.
func TestFlagsAroundASubcommandStillWork(t *testing.T) {
	fl, err := parse([]string{"-config", "custom.yaml", "gateway", "start"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fl.configPath != "custom.yaml" {
		t.Errorf("the configuration flag was lost: %q", fl.configPath)
	}
	if fl.gatewayAction != "start" {
		t.Errorf("action is %q, want start", fl.gatewayAction)
	}
}

// Flags AFTER the subcommand too: the argument list is rebuilt, and a flag left on the wrong side
// of the removal would be silently dropped.
func TestFlagsAfterASubcommandStillWork(t *testing.T) {
	fl, err := parse([]string{"gateway", "start", "-gateway", "127.0.0.1:9999"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fl.gatewayAction != "start" {
		t.Errorf("action is %q, want start", fl.gatewayAction)
	}
	if fl.gateway != "127.0.0.1:9999" {
		t.Errorf("the listen address flag was lost: %q", fl.gateway)
	}
}

// A typo is rejected with the list of what exists. A silent fallthrough would turn
// `motita gateway strat` into a TUI, which is the kind of failure a user cannot even describe.
func TestAnUnknownGatewayActionIsRejected(t *testing.T) {
	_, err := parse([]string{"gateway", "strat"})
	if err == nil {
		t.Fatal("an unknown gateway action must be rejected, not ignored")
	}
	for _, want := range []string{"strat", "start", "stop", "status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not mention %q: %v", want, err)
		}
	}
}

// Even an action that differs only in case is checked: "STOP" is valid, "STOPS" is not.
func TestAnUnknownGatewayActionInAnotherCaseIsRejected(t *testing.T) {
	if _, err := parse([]string{"gateway", "Stops"}); err == nil {
		t.Fatal("an unknown action must be rejected whatever its case")
	}
}

// `motita gateway` alone must say what the actions are rather than report "missing value".
func TestABareGatewaySaysWhatTheActionsAre(t *testing.T) {
	_, err := parse([]string{"gateway"})
	if err == nil {
		t.Fatal("a bare `gateway` must ask for an action")
	}
	if !strings.Contains(err.Error(), "start") {
		t.Errorf("the message does not say what the actions are: %v", err)
	}
}

// The subcommand is not confused with a value: `-session gateway` names a conversation called
// "gateway", and treating it as a command would start a service instead of a client.
func TestAFlagValueNamedGatewayIsNotASubcommand(t *testing.T) {
	fl, err := parse([]string{"-session", "gateway", "-connect", "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fl.gatewayAction != "" {
		t.Fatalf("a flag VALUE named gateway was taken as a subcommand: %q", fl.gatewayAction)
	}
	if fl.session != "gateway" {
		t.Fatalf("session = %q, want the value the user gave", fl.session)
	}
}

// Every documented flag still parses. This change ADDS a way to invoke the program, and breaking
// the existing ones would turn a feature into a forced migration.
func TestTheExistingFlagsAreUntouched(t *testing.T) {
	cases := []struct {
		args  []string
		check func(flags) bool
		name  string
	}{
		{[]string{"-config", "x.yaml"}, func(f flags) bool { return f.configPath == "x.yaml" }, "-config"},
		{[]string{"-task", "do it"}, func(f flags) bool { return f.task == "do it" }, "-task"},
		{[]string{"-task-file", "t.txt"}, func(f flags) bool { return f.taskFile == "t.txt" }, "-task-file"},
		{[]string{"-plan"}, func(f flags) bool { return f.plan }, "-plan"},
		{[]string{"-tui"}, func(f flags) bool { return f.tui }, "-tui"},
		{[]string{"-serve"}, func(f flags) bool { return f.serve }, "-serve"},
		{[]string{"-gateway", "off"}, func(f flags) bool { return f.gateway == "off" }, "-gateway"},
		{[]string{"-connect", "127.0.0.1:1"}, func(f flags) bool { return f.connect == "127.0.0.1:1" }, "-connect"},
		{[]string{"-session", "a"}, func(f flags) bool { return f.session == "a" }, "-session"},
		{[]string{"-p", "hi"}, func(f flags) bool { return f.prompt == "hi" }, "-p"},
		{[]string{"-init"}, func(f flags) bool { return f.initConfig }, "-init"},
		{[]string{"-validate-config"}, func(f flags) bool { return f.validateConfig }, "-validate-config"},
		{[]string{"-version"}, func(f flags) bool { return f.version }, "-version"},
		{[]string{"-isolation"}, func(f flags) bool { return f.isolation }, "-isolation"},
	}
	for _, c := range cases {
		fl, err := parse(c.args)
		if err != nil {
			t.Errorf("%s: parse(%v): %v", c.name, c.args, err)
			continue
		}
		if !c.check(fl) {
			t.Errorf("%s no longer parses: %+v", c.name, fl)
		}
		if fl.gatewayAction != "" {
			t.Errorf("%s produced a gateway action it should not have: %q", c.name, fl.gatewayAction)
		}
	}
}

// The equals form still works for the flags, and works for a subcommand's neighbours.
func TestTheEqualsFormStillWorks(t *testing.T) {
	fl, err := parse([]string{"-gateway=off", "gateway", "status"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fl.gateway != "off" || fl.gatewayAction != "status" {
		t.Fatalf("got %+v", fl)
	}
}

package app

import (
	"strings"
	"testing"
)

// The subcommand is positional, like `gateway`, and the parser is the only thing that decides
// whether `motita curator pin build-firmware` is a command or a complaint. Every case here is
// a shape a user will type.

func TestTheCuratorSubcommandIsRecognised(t *testing.T) {
	cases := []struct {
		args   []string
		action string
		skill  string
	}{
		{[]string{"curator", "status"}, "status", ""},
		{[]string{"curator", "run"}, "run", ""},
		{[]string{"curator", "list-archived"}, "list-archived", ""},
		{[]string{"curator", "pin", "build-firmware"}, "pin", "build-firmware"},
		{[]string{"curator", "unpin", "one"}, "unpin", "one"},
		{[]string{"curator", "restore", "one"}, "restore", "one"},
		{[]string{"curator", "list-proposed"}, "list-proposed", ""},
		{[]string{"curator", "show-proposed", "one"}, "show-proposed", "one"},
		{[]string{"curator", "accept", "one"}, "accept", "one"},
		{[]string{"curator", "reject", "one"}, "reject", "one"},
		// Recorded lower-cased for the actions themselves, which is how the switch reads
		// them, and the skill name exactly as typed: a name is data.
		{[]string{"curator", "RUN"}, "run", ""},
		{[]string{"curator", "Pin", "One.md"}, "pin", "One.md"},
	}
	for _, c := range cases {
		fl, err := parse(c.args)
		if err != nil {
			t.Errorf("parse(%v): %v", c.args, err)
			continue
		}
		if fl.curatorAction != c.action || fl.curatorSkill != c.skill {
			t.Errorf("parse(%v) = (%q, %q), want (%q, %q)",
				c.args, fl.curatorAction, fl.curatorSkill, c.action, c.skill)
		}
	}
}

func TestCuratorRunTakesItsTwoSwitches(t *testing.T) {
	fl, err := parse([]string{"curator", "run", "--consolidate", "--dry-run"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !fl.curatorConsolidate || !fl.curatorDryRun {
		t.Fatalf("a switch was lost: %+v", fl)
	}
}

// A switch takes NO value, so it must not swallow the word after it.
func TestACuratorSwitchDoesNotConsumeWhatFollows(t *testing.T) {
	fl, err := parse([]string{"-config", "c.yaml", "curator", "run", "--dry-run"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fl.configPath != "c.yaml" || fl.curatorAction != "run" || !fl.curatorDryRun {
		t.Fatalf("got %+v", fl)
	}
}

// A typo is rejected with the list of what was meant, rather than falling through to another
// mode entirely.
func TestAnUnknownCuratorActionIsRejected(t *testing.T) {
	_, err := parse([]string{"curator", "strat"})
	if err == nil {
		t.Fatal("a typo must be rejected, not ignored")
	}
	for _, want := range []string{"strat", "status", "run", "pin", "restore", "list-archived"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not mention %q: %v", want, err)
		}
	}
}

func TestCuratorPinWithoutASkillIsRejected(t *testing.T) {
	for _, action := range []string{"pin", "unpin", "restore"} {
		if _, err := parse([]string{"curator", action}); err == nil {
			t.Errorf("`curator %s` with no skill must be refused", action)
		}
	}
}

func TestABareCuratorSaysWhatTheActionsAre(t *testing.T) {
	_, err := parse([]string{"curator"})
	if err == nil {
		t.Fatal("a bare `curator` must ask for an action")
	}
	if !strings.Contains(err.Error(), "list-archived") {
		t.Errorf("the message does not list the actions: %v", err)
	}
}

// The two subcommands coexist: adding one must not break the other.
func TestTheGatewaySubcommandStillParsesNextToTheCurator(t *testing.T) {
	fl, err := parse([]string{"gateway", "status"})
	if err != nil || fl.gatewayAction != "status" {
		t.Fatalf("got %+v, %v", fl, err)
	}
	fl, err = parse([]string{"curator", "status"})
	if err != nil || fl.curatorAction != "status" {
		t.Fatalf("got %+v, %v", fl, err)
	}
	fl, err = parse([]string{"gateway", "start", "curator", "status"})
	if err != nil || fl.gatewayAction != "start" || fl.curatorAction != "status" {
		t.Fatalf("two subcommands on one line: got %+v, %v", fl, err)
	}
}

// A word that merely LOOKS like a subcommand is left alone.
//
// `-session gateway` names a conversation called "gateway". The scan skips the values of
// flags for exactly this reason, and without that the session would be renamed and the
// command would run against a conversation the user never asked for.
func TestASessionNamedCuratorIsNotASubcommand(t *testing.T) {
	fl, err := parse([]string{"-session", "curator", "-connect", "host:1234"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fl.curatorAction != "" {
		t.Errorf("`-session curator ...` was read as a curator command: %+v", fl)
	}
	if fl.session != "curator" {
		t.Errorf("session = %q, want the value of -session", fl.session)
	}
	if fl.connect != "host:1234" {
		t.Errorf("connect = %q", fl.connect)
	}

	// And the same for the gateway word, which has been there longer.
	fl, err = parse([]string{"-session", "gateway", "-connect", "host:1234"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fl.gatewayAction != "" {
		t.Errorf("`-session gateway ...` was read as a gateway command: %+v", fl)
	}
	if fl.session != "gateway" {
		t.Errorf("session = %q, want the value of -session", fl.session)
	}
}

// The switches are recognised in both spellings the rest of the program accepts.
func TestTheCuratorSwitchesTakeBothSpellings(t *testing.T) {
	for _, args := range [][]string{
		{"curator", "run", "-consolidate", "-dry-run"},
		{"curator", "run", "--consolidate", "--dry-run"},
	} {
		fl, err := parse(args)
		if err != nil {
			t.Fatalf("parse(%v): %v", args, err)
		}
		if !fl.curatorConsolidate || !fl.curatorDryRun {
			t.Errorf("parse(%v) lost a switch: %+v", args, fl)
		}
	}
}

// The help text names every action the parser accepts, so the two cannot drift.
func TestTheCuratorHelpListsEveryAction(t *testing.T) {
	for _, want := range []string{"status", "run", "pin", "unpin", "restore", "list-archived",
		"list-proposed", "show-proposed", "accept", "reject", "--consolidate", "--dry-run", "Usage: motita curator <action>"} {
		if !strings.Contains(curatorCommandHelp, want) {
			t.Errorf("the help does not mention %q:\n%s", want, curatorCommandHelp)
		}
	}
}

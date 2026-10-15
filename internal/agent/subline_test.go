package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSubLineRoundTripsAndNamesItsAgent(t *testing.T) {
	in := SubLine{Agent: "a2", Role: "review", Kind: SubCommand, Text: "git log", Exit: 1, Out: "boom"}
	got, ok := ParseSubLine(in.Line())
	if !ok || got != in {
		t.Fatalf("round trip = %+v, %v", got, ok)
	}
	if got.Tag() != "a2 · review" {
		t.Errorf("tag = %q", got.Tag())
	}
	if (SubLine{Agent: "a1"}).Tag() != "a1" {
		t.Error("a code agent is named by its id alone")
	}
}

func TestParseSubLineRefusesOtherLines(t *testing.T) {
	for _, line := range []string{"running: ls", SubPrefix + "{not json", SubPrefix + `{"kind":"command"}`} {
		if _, ok := ParseSubLine(line); ok {
			t.Errorf("%q is not a sub line", line)
		}
	}
}

// A background agent's commands and finished thoughts go up to the main agent's channel, tagged;
// everything else it reports stays its own activity.
func TestARelayPassesCommandsAndThoughtsUp(t *testing.T) {
	parent := &Agent{}
	var up []string
	parent.Progress = func(format string, args ...any) { up = append(up, strings.TrimSpace(fmt.Sprintf(format, args...))) }
	var own []string
	relay := parent.relayProgress("a1", "code", func(format string, args ...any) { own = append(own, fmt.Sprintf(format, args...)) })
	relay("deciding action (round %d/%d)...", 1, 100)
	relay("running: %s", "ls")
	relay("output (exit %d):\n%s", 0, "a.txt")
	relay("%sfirst I read the tree", ThinkingPrefix)
	if len(own) != 4 {
		t.Fatalf("the agent's own progress saw %d lines, want all 4", len(own))
	}
	if len(up) != 2 {
		t.Fatalf("passed up %d lines, want 2: %q", len(up), up)
	}
	cmd, _ := ParseSubLine(up[0])
	if cmd.Agent != "a1" || cmd.Role != "" || cmd.Kind != SubCommand || cmd.Text != "ls" || cmd.Out != "a.txt" {
		t.Errorf("command = %+v", cmd)
	}
	thought, _ := ParseSubLine(up[1])
	if thought.Kind != SubThought || thought.Text != "first I read the tree" {
		t.Errorf("thought = %+v", thought)
	}
}

func TestARelayWithoutAnInterfaceStaysQuiet(t *testing.T) {
	relay := (&Agent{}).relayProgress("a3", "research", func(string, ...any) {})
	relay("running: ls")
	relay("output (exit 0):\n")
}

func TestARelayNamesTheRoleOfAReviewer(t *testing.T) {
	parent := &Agent{}
	var up []string
	parent.Progress = func(format string, args ...any) { up = append(up, fmt.Sprintf(format, args...)) }
	relay := parent.relayProgress("a2", "review", func(string, ...any) {})
	relay("%sno tests", ThinkingPrefix)
	if s, _ := ParseSubLine(up[0]); s.Role != "review" {
		t.Errorf("role = %q", s.Role)
	}
}

// A finished agent is no longer doing what its last phase line said.
func TestFinishingAnAgentClearsItsActivity(t *testing.T) {
	fleetTiming(t, 0, time.Hour)
	sink := &lineSink{}
	f := newFleet(sink.add)
	main := f.add("", "main", "the task", "")
	sub := f.add(main.id, "a1", "a piece", "")
	f.setActivity(sub, "synthesizing answer...")
	f.finish(sub, AgentPassed, "all done")
	got := sink.last(t)
	f.close()
	for _, a := range got {
		if a.ID == "a1" && (a.Activity != "" || a.Summary != "all done") {
			t.Errorf("a1 = %+v", a)
		}
	}
}

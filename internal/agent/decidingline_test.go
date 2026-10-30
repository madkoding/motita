package agent

import (
	"strings"
	"testing"
)

func TestDecidingLineSaysWhatTheAgentIsWorkingOn(t *testing.T) {
	long := strings.Repeat("word ", 40)
	cases := []struct {
		name string
		run  taskRun
		want string
	}{
		{"nothing known yet", taskRun{}, decidingFirst},
		{"plan with a first step", taskRun{plan: Plan{Steps: []Step{{Action: " read  the\ntest "}}}},
			startingWith + "read the test"},
		{"plan whose first step is blank", taskRun{plan: Plan{Steps: []Step{{}}}}, decidingFirst},
		{"after a round the words are quoted, not the command", taskRun{lastDoing: "Running the tests",
			journal: []roundRecord{{commands: "go test ./..."}}}, decidingNextWith + "Running the tests"},
		{"a long description is cut", taskRun{lastDoing: long, journal: []roundRecord{{}}},
			decidingNextWith + truncate(long, focusWidth)},
		{"after a round with no words", taskRun{journal: []roundRecord{{commands: "go test ./..."}}}, decidingNext},
		{"after a round that ran nothing", taskRun{journal: []roundRecord{{}}}, decidingNext},
	}
	for _, c := range cases {
		if got := c.run.decidingLine(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

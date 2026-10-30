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
		{"after a round", taskRun{journal: []roundRecord{{commands: "go test ./..."}}},
			decidingNextWith + "go test ./..."},
		{"after a round that ran nothing", taskRun{journal: []roundRecord{{}}}, decidingNext},
		{"a long command is cut", taskRun{journal: []roundRecord{{commands: long}}},
			decidingNextWith + truncate(collapse(long), focusWidth)},
	}
	for _, c := range cases {
		if got := c.run.decidingLine(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

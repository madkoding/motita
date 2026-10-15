package agent

import (
	"encoding/json"
	"fmt"
	"github.com/madkoding/motita/internal/config"
	"strconv"
	"strings"
)

// WHAT A BACKGROUND AGENT DOES, as the interfaces see it.
//
// A background agent's own progress lines only ever fed its row in the agents list, so while it
// worked the terminal and thinking views showed the main agent alone: three agents running
// commands and reasoning, and nothing to read. A command it ran and a thought it finished are
// reported on the main agent's progress channel as ONE line, carrying the agent that did it, so
// the views can show them with its name and two agents never interleave the halves of a command.

// SubPrefix marks a progress line that is one finished thing a background agent did.
const SubPrefix = "sub: "

// Kinds of SubLine.
const (
	SubCommand = "command"
	SubThought = "thought"
)

// SubLine is a command a background agent ran, with what it printed, or a thought it finished.
type SubLine struct {
	Agent string `json:"agent"`
	Role  string `json:"role,omitempty"` // empty for code, the default
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Exit  int    `json:"exit,omitempty"`
	Out   string `json:"out,omitempty"`
}

// Tag is how the agent is named beside what it did: "a1", or "a2 · review".
func (s SubLine) Tag() string {
	if s.Role == "" {
		return s.Agent
	}
	return s.Agent + " · " + s.Role
}

// Line renders the SubLine as a progress line.
func (s SubLine) Line() string {
	data, _ := json.Marshal(s) // plain fields only: it cannot fail
	return SubPrefix + string(data)
}

// ParseSubLine reads a line made by SubLine.Line. ok is false for any other line.
func ParseSubLine(line string) (SubLine, bool) {
	body, found := strings.CutPrefix(line, SubPrefix)
	if !found {
		return SubLine{}, false
	}
	var s SubLine
	if err := json.Unmarshal([]byte(body), &s); err != nil || s.Agent == "" {
		return SubLine{}, false
	}
	return s, true
}

// relayProgress is the Progress of a background agent: what it reports still becomes its activity
// in the fleet (base), and its commands and finished thoughts are also passed up to the main
// agent's channel, tagged with the agent. parent is read when a line is sent, not before: an
// interface may attach its callback after the agent is built.
func (a *Agent) relayProgress(id, role string, base func(string, ...any)) func(string, ...any) {
	if role == config.TaskCode {
		role = ""
	}
	var cmd string // the command waiting for its output; a background agent reports from one goroutine
	send := func(s SubLine) {
		if a.Progress != nil {
			a.Progress("%s", s.Line())
		}
	}
	return func(format string, args ...any) {
		base(format, args...)
		line := fmt.Sprintf(format, args...)
		switch {
		case strings.HasPrefix(line, "running: "):
			cmd = strings.TrimPrefix(line, "running: ")
		case strings.HasPrefix(line, "output (exit "):
			num, body, _ := strings.Cut(strings.TrimPrefix(line, "output (exit "), "):")
			code, _ := strconv.Atoi(num)
			send(SubLine{Agent: id, Role: role, Kind: SubCommand, Text: cmd, Exit: code,
				Out: strings.TrimPrefix(body, "\n")})
		case strings.HasPrefix(line, ThinkingPrefix):
			send(SubLine{Agent: id, Role: role, Kind: SubThought, Text: strings.TrimPrefix(line, ThinkingPrefix)})
		default:
			// Phase lines and the like are the agent's own activity, already given to base.
		}
	}
}

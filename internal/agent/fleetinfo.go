package agent

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/llm"
)

// THE AGENTS OF A RUN, as the interfaces see them.
//
// A run can start agents of its own in the background (spawn_agent). The person watching needs
// to know what is running, why, for how long and at what cost - a list nobody can see is a bill
// nobody can explain. The list travels on the same progress channel as every other line, so the
// local TUI, the gateway and its clients need no second pipe: a line that starts with
// AgentsPrefix carries the whole list as JSON, and each one replaces the one before it.

// AgentsPrefix marks a progress line that is a snapshot of the run's agents. It is a snapshot,
// like LivePrefix: an interface keeps the latest, and a log keeps none.
const AgentsPrefix = "agents: "

// Agent states.
const (
	AgentRunning   = "running"
	AgentPassed    = "passed"
	AgentFailed    = "failed"
	AgentCancelled = "cancelled"
)

// AgentInfo is one agent of a run. The main agent is in the list too (Parent empty), so the
// total cost of a run is the sum of its rows.
type AgentInfo struct {
	ID      string `json:"id"`
	Parent  string `json:"parent,omitempty"`
	Purpose string `json:"purpose"`
	// State is one of AgentRunning, AgentPassed, AgentFailed, AgentCancelled.
	State    string    `json:"state"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	// ElapsedMS is measured when the snapshot is taken; an interface that ticks the clock itself
	// counts from Started while State is running.
	ElapsedMS int64     `json:"elapsed_ms"`
	Tokens    llm.Usage `json:"tokens"`
	Calls     int64     `json:"calls"`
	Round     int       `json:"round,omitempty"`
	// Activity is the agent's latest progress line, for a one-line "what is it doing now".
	Activity string `json:"activity,omitempty"`
	// Branch is where a background agent's work is, when it ran in a worktree of its own.
	Branch string `json:"branch,omitempty"`
	// Summary is the agent's own account of what it did, once it finished.
	Summary string `json:"summary,omitempty"`
}

// AgentsLine renders a snapshot as a progress line.
func AgentsLine(agents []AgentInfo) string {
	if agents == nil {
		agents = []AgentInfo{}
	}
	data, _ := json.Marshal(agents) // plain fields only: it cannot fail
	return AgentsPrefix + string(data)
}

// ParseAgentsLine reads a line made by AgentsLine. ok is false for any other line.
func ParseAgentsLine(line string) (agents []AgentInfo, ok bool) {
	body, found := strings.CutPrefix(line, AgentsPrefix)
	if !found {
		return nil, false
	}
	if err := json.Unmarshal([]byte(body), &agents); err != nil {
		return nil, false
	}
	return agents, true
}

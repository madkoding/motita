package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/llm"
)

func TestAgentsLineRoundTrips(t *testing.T) {
	started := time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)
	in := []AgentInfo{
		{ID: "main", Purpose: "the task", State: AgentRunning, Started: started, ElapsedMS: 1200,
			Tokens: llm.Usage{Input: 10, Output: 3}, Calls: 2, Round: 4, Activity: "running: go test"},
		{ID: "a1", Parent: "main", Purpose: "write the tests", State: AgentPassed, Started: started,
			Finished: started.Add(time.Minute), Branch: "motita/sub/a1", Summary: "done"},
	}
	line := AgentsLine(in)
	if !strings.HasPrefix(line, AgentsPrefix) {
		t.Fatalf("line = %q", line)
	}
	if strings.Contains(line, `"finished":"0001`) {
		t.Fatalf("an unfinished agent must not carry a zero finish time: %s", line)
	}
	out, ok := ParseAgentsLine(line)
	if !ok || len(out) != 2 || out[1].Branch != "motita/sub/a1" || out[0].Tokens.Input != 10 || !out[1].Finished.Equal(in[1].Finished) {
		t.Fatalf("ok=%v out=%+v", ok, out)
	}
}

func TestAgentsLineOfNoAgentsIsAnEmptyList(t *testing.T) {
	if got := AgentsLine(nil); got != AgentsPrefix+"[]" {
		t.Fatalf("got %q", got)
	}
}

func TestParseAgentsLineRefusesOtherLines(t *testing.T) {
	for _, line := range []string{"running: ls", AgentsPrefix + "{not json"} {
		if _, ok := ParseAgentsLine(line); ok {
			t.Errorf("%q must not parse", line)
		}
	}
}

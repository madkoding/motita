package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
)

// tierActs is one spawn_agent of a tier.
func tierActs(command, tier string) string {
	b, _ := json.Marshal(map[string]any{
		"reasoning": "scripted",
		"actions": []map[string]string{
			{"kind": "spawn_agent", "description": "step", "command": command, "tier": tier},
		},
		"final_action": map[string]string{"command": ""}, "done": false,
	})
	return string(b)
}

// withCheap installs tiers on a: only cheap has an engine, and the classifier answers verdict.
func withCheap(a *Agent, cheap *llm.Client, verdict *string, asked *int) {
	a.SetTiers(func(tier string) *llm.Client {
		if tier == TierCheap {
			return cheap
		}
		return nil
	}, func(context.Context, string, string) string {
		*asked++
		return *verdict
	})
}

// Nothing is routed until a cheap model is assigned, and a tier the models block does not know is
// refused when an agent is started.
func TestTierForNeedsACheapModel(t *testing.T) {
	ctx := context.Background()
	a := bareAgent(t, nil)
	if tier, ok := a.tierFor(ctx, Command{Tier: "cheap"}, config.TaskCode, "p", "b"); tier != "" || !ok {
		t.Errorf("no tiers installed: %q %v", tier, ok)
	}
	verdict, asked := "cheap", 0
	withCheap(a, &llm.Client{}, &verdict, &asked)
	if _, ok := a.tierFor(ctx, Command{Tier: "medium"}, config.TaskCode, "p", "b"); ok {
		t.Error("an unknown tier is refused")
	}
	a.startFleet(taskOf("t"))
	defer a.endFleet(TaskResult{})
	out, err := a.spawnAgent(ctx, Command{Command: "p\nb", Tier: "medium"})
	if err == nil || !strings.Contains(out, "not one of cheap, strong") {
		t.Errorf("spawn with an unknown tier: %q %v", out, err)
	}
}

// The classifier judges only an agent that writes, and anything but "cheap" from it is strong.
func TestTierForClassifiesWhatWrites(t *testing.T) {
	ctx := context.Background()
	a := bareAgent(t, nil)
	verdict, asked := "cheap", 0
	withCheap(a, &llm.Client{}, &verdict, &asked)
	tier, _ := a.tierFor(ctx, Command{Tier: " Strong "}, config.TaskCode, "p", "b")
	if tier != TierStrong || asked != 0 {
		t.Errorf("a named tier is not judged: %q asked=%d", tier, asked)
	}
	if tier, _ = a.tierFor(ctx, Command{}, config.TaskCode, "p", "b"); tier != TierCheap || asked != 1 {
		t.Errorf("an easy piece goes cheap: %q asked=%d", tier, asked)
	}
	verdict = "banana"
	if tier, _ = a.tierFor(ctx, Command{}, config.TaskCode, "p", "b"); tier != TierStrong {
		t.Errorf("anything but cheap is strong: %q", tier)
	}
	if tier, _ = a.tierFor(ctx, Command{}, config.TaskReview, "p", "b"); tier != "" || asked != 2 {
		t.Errorf("a reviewer is not judged: %q asked=%d", tier, asked)
	}
}

// engineOf is the tier's model when one is assigned, else the role's, else the agent's own.
func TestEngineOfPrefersTheTierThenTheRole(t *testing.T) {
	a := bareAgent(t, nil)
	cheap, role := &llm.Client{}, &llm.Client{}
	verdict, asked := "", 0
	withCheap(a, cheap, &verdict, &asked)
	a.SetRoleEngines(func(string) *llm.Client { return role })
	if a.engineOf(config.TaskCode, TierCheap) != cheap {
		t.Error("the tier's engine")
	}
	if a.engineOf(config.TaskCode, TierStrong) != role || a.engineOf(config.TaskCode, "") != role {
		t.Error("the role's engine when the tier has none")
	}
	a.SetRoleEngines(nil)
	if a.engineOf(config.TaskCode, TierStrong) != a.engine {
		t.Error("the agent's own engine last")
	}
}

// The audit and the synthesis run on the summary model when the session assigns one; the phases
// that decide what to do, and every phase without a summary model, run on the agent's own.
func TestTheAuditAndTheSynthesisRunOnTheSummaryModel(t *testing.T) {
	a := bareAgent(t, nil)
	summary := &llm.Client{}
	a.SetRoleEngines(func(role string) *llm.Client {
		if role == config.TaskSummary {
			return summary
		}
		return nil
	})
	for _, phase := range []string{"audit", "synthesize"} {
		if a.phaseEngine(phase) != summary {
			t.Errorf("%s runs on the summary model", phase)
		}
	}
	for _, phase := range []string{"analyze", "plan", "execute"} {
		if a.phaseEngine(phase) != a.engine {
			t.Errorf("%s keeps the agent's own engine", phase)
		}
	}
	a.SetRoleEngines(nil)
	if a.phaseEngine("audit") != a.engine {
		t.Error("without a summary model the audit runs on the agent's own engine")
	}
}

// escalationScript is a cheap agent that never finishes and a strong one that does, once told.
func escalationScript() *scriptServer {
	s := &scriptServer{}
	mainRound := 0
	s.execute = func(_ int, prompt string) string {
		if isChildPrompt(prompt) {
			if strings.Contains(prompt, "A cheaper model already tried") {
				return step(true, "echo hello > docs.txt")
			}
			return step(false, "true")
		}
		mainRound++
		switch mainRound {
		case 1:
			return tierActs("write the docs\nCreate docs.txt saying hello.", "cheap")
		case 2:
			return acts(false, kindAct{"wait_agents", ""})
		default:
			return step(true, "true")
		}
	}
	return s
}

// A cheap agent that never finishes is run again on the strong model, told what happened; the
// report says both tiers.
func TestACheapAgentThatFailsEscalatesToTheStrongModel(t *testing.T) {
	s := escalationScript()
	_, result, sink := fleetRun(t, s, func(c *config.Config) {
		inRepo(t)(c)
		c.Agent.MaxSteps = 6
	}, func(e *fixture) {
		own := e.agent.engine
		e.agent.SetTiers(func(tier string) *llm.Client {
			if tier == TierCheap {
				return own
			}
			return nil
		}, nil)
	})
	if !result.Pass {
		t.Fatalf("the run must pass: %s", result.Reason)
	}
	main, child := s.split()
	if len(child) < 2 || !strings.Contains(strings.Join(child, "\n"), "A cheaper model already tried") {
		t.Fatalf("the strong model must be told what the cheap one did:\n%v", child)
	}
	if all := strings.Join(main, "\n"); !strings.Contains(all, "model tier: cheap → strong") {
		t.Errorf("the report names both tiers:\n%s", all)
	}
	var seen string
	ids := map[string]bool{}
	for _, ag := range sink.lastAgents(t) {
		seen += ag.Purpose + "|"
		if ids[ag.ID] {
			t.Errorf("two fleet rows share the id %q", ag.ID)
		}
		ids[ag.ID] = true
	}
	if !strings.Contains(seen, "cheap") {
		t.Errorf("the fleet names the tier: %s", seen)
	}
}

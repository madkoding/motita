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
		"reasoning":    "scripted",
		"actions":      []map[string]string{{"kind": "spawn_agent", "description": "step", "command": command, "tier": tier}},
		"final_action": map[string]string{"command": ""}, "done": false,
	})
	return string(b)
}

// A tier is routed only when the cheap model is assigned; a stranger is refused; the classifier
// judges only an agent that writes, and anything but "cheap" from it is strong.
func TestTierForRoutesOnlyWhenACheapModelIsAssigned(t *testing.T) {
	ctx := context.Background()
	a := bareAgent(t, nil)
	if tier, ok := a.tierFor(ctx, Command{Tier: "cheap"}, config.TaskCode, "p", "b"); tier != "" || !ok {
		t.Errorf("no tiers installed: %q %v", tier, ok)
	}
	cheap, strong := &llm.Client{}, &llm.Client{}
	asked := 0
	verdict := "cheap"
	a.SetTiers(func(tier string) *llm.Client {
		if tier == TierCheap {
			return cheap
		}
		return nil
	}, func(context.Context, string, string) string { asked++; return verdict })
	if _, ok := a.tierFor(ctx, Command{Tier: "medium"}, config.TaskCode, "p", "b"); ok {
		t.Error("an unknown tier is refused")
	}
	if tier, _ := a.tierFor(ctx, Command{Tier: " Strong "}, config.TaskCode, "p", "b"); tier != TierStrong || asked != 0 {
		t.Errorf("a named tier is not judged: %q asked=%d", tier, asked)
	}
	if tier, _ := a.tierFor(ctx, Command{}, config.TaskCode, "p", "b"); tier != TierCheap || asked != 1 {
		t.Errorf("an easy piece goes cheap: %q asked=%d", tier, asked)
	}
	verdict = "banana"
	if tier, _ := a.tierFor(ctx, Command{}, config.TaskCode, "p", "b"); tier != TierStrong {
		t.Errorf("anything but cheap is strong: %q", tier)
	}
	if tier, _ := a.tierFor(ctx, Command{}, config.TaskReview, "p", "b"); tier != "" || asked != 2 {
		t.Errorf("a reviewer is not judged: %q asked=%d", tier, asked)
	}

	// engineOf: the tier's model, else the role's, else the agent's own.
	role := &llm.Client{}
	a.SetRoleEngines(func(string) *llm.Client { return role })
	a.SetTiers(func(tier string) *llm.Client {
		if tier == TierCheap {
			return cheap
		}
		return nil
	}, nil)
	_ = strong
	if a.engineOf(config.TaskCode, TierCheap) != cheap || a.engineOf(config.TaskCode, TierStrong) != role ||
		a.engineOf(config.TaskCode, "") != role {
		t.Error("tier, then role")
	}
	a.SetRoleEngines(nil)
	if a.engineOf(config.TaskCode, TierStrong) != a.engine {
		t.Error("then the agent's own")
	}
}

// A cheap agent that never finishes is run again on the strong model, told what happened; the
// report says both tiers.
func TestACheapAgentThatFailsEscalatesToTheStrongModel(t *testing.T) {
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
	for _, ag := range sink.lastAgents(t) {
		seen += ag.Purpose + "|"
	}
	if !strings.Contains(seen, "cheap") {
		t.Errorf("the fleet names the tier: %s", seen)
	}
}

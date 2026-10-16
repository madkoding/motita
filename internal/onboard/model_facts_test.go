package onboard

import (
	"strings"
	"testing"
)

var (
	knownTiers = map[string]bool{"": true, "basic": true, "mid": true, "frontier": true}
	knownCosts = map[string]bool{"": true, "local": true, "low": true, "mid": true, "high": true}
)

// modelFactsProblem is what is wrong with a model's tier, cost and price, or "".
func modelFactsProblem(m Model) string {
	switch {
	case !knownTiers[m.Tier]:
		return "unknown tier " + m.Tier
	case !knownCosts[m.Cost]:
		return "unknown cost " + m.Cost
	case (m.PriceIn == 0) != (m.PriceOut == 0):
		return "a price needs both input and output"
	case m.Cost == "local" && m.PriceIn != 0:
		return "a local model has no per-token price"
	}
	return ""
}

// The catalogue's tier, cost and price are optional, but a value that is there must be one the page
// knows how to draw, and a price must come as a pair.
func TestModelFactsAreKnownValues(t *testing.T) {
	for _, p := range Providers() {
		for _, m := range p.Models {
			if bad := modelFactsProblem(m); bad != "" {
				t.Errorf("%s/%s: %s", p.ID, m.ID, bad)
			}
		}
	}
}

// The check itself catches each kind of mistake.
func TestModelFactsProblemNamesTheMistake(t *testing.T) {
	cases := map[string]Model{
		"unknown tier":       {Tier: "huge"},
		"unknown cost":       {Cost: "free"},
		"needs both":         {PriceIn: 1},
		"local model has no": {Cost: "local", PriceIn: 1, PriceOut: 2},
	}
	for want, m := range cases {
		if got := modelFactsProblem(m); got == "" || !strings.Contains(got, want) {
			t.Errorf("%+v: got %q, want it to mention %q", m, got, want)
		}
	}
}

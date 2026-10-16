package onboard

import "testing"

// The catalogue's tier, cost and price are optional, but a value that is there must be one the page
// knows how to draw, and a price must come as a pair.
func TestModelFactsAreKnownValues(t *testing.T) {
	tiers := map[string]bool{"": true, "basic": true, "mid": true, "frontier": true}
	costs := map[string]bool{"": true, "local": true, "low": true, "mid": true, "high": true}
	for _, p := range Providers() {
		for _, m := range p.Models {
			if !tiers[m.Tier] {
				t.Errorf("%s/%s: unknown tier %q", p.ID, m.ID, m.Tier)
			}
			if !costs[m.Cost] {
				t.Errorf("%s/%s: unknown cost %q", p.ID, m.ID, m.Cost)
			}
			if (m.PriceIn == 0) != (m.PriceOut == 0) {
				t.Errorf("%s/%s: price needs both input and output", p.ID, m.ID)
			}
			if m.Cost == "local" && m.PriceIn != 0 {
				t.Errorf("%s/%s: a local model has no per-token price", p.ID, m.ID)
			}
		}
	}
}

package onboard

import (
	"regexp"
	"testing"
)

// Every entry of prices.json names a model of the catalogue and carries both prices, so a typo in
// an id cannot silently leave a model unpriced.
func TestPricesNameCatalogueModels(t *testing.T) {
	models := map[string]map[string]bool{}
	for _, p := range Providers() {
		models[p.ID] = map[string]bool{}
		for _, m := range p.Models {
			models[p.ID][m.ID] = true
		}
	}
	for provider, byModel := range catalogPrices.Prices {
		for id, pr := range byModel {
			if !models[provider][id] {
				t.Errorf("prices.json prices %s/%s, which the catalogue does not list", provider, id)
			}
			if pr.In <= 0 || pr.Out <= 0 {
				t.Errorf("%s/%s: a price needs both input and output, got %+v", provider, id, pr)
			}
		}
	}
}

// The catalogue's models carry the prices of the file, and the day they were checked is a date.
func TestPricesReachTheModels(t *testing.T) {
	for _, p := range Providers() {
		if p.ID != "openai" {
			continue
		}
		for _, m := range p.Models {
			if m.ID == "gpt-5" && (m.PriceIn != 1.25 || m.PriceOut != 10) {
				t.Errorf("gpt-5: %v / %v", m.PriceIn, m.PriceOut)
			}
		}
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(PricesChecked()) {
		t.Errorf("checked day: %q", PricesChecked())
	}
}

// A file that does not parse stops the program at once, so it cannot ship.
func TestReadPricesPanicsOnBadJSON(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a broken prices file must panic")
		}
	}()
	readPrices([]byte("{"))
}

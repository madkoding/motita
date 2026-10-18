package config

import "strings"

// retiredBrowserProvider is the provider that ran a small model in a browser tab. It answered
// nonsense, so it was removed; a file that still names it falls back to the default.
const retiredBrowserProvider = "browser"

// dropRetiredBrowserModel lets a saved file that still points at the removed browser provider
// load: the main model returns to the default, and a models entry follows the default again.
func (c *Config) dropRetiredBrowserModel() {
	if c.LLM.Provider == retiredBrowserProvider {
		d := Default().LLM
		c.LLM.Provider, c.LLM.Model, c.LLM.BaseURL = d.Provider, d.Model, d.BaseURL
	}
	for _, e := range []*string{&c.Models.Plan, &c.Models.Code, &c.Models.Review, &c.Models.Research,
		&c.Models.Summary, &c.Models.Cheap, &c.Models.Strong} {
		if provider, _, _ := strings.Cut(strings.TrimSpace(*e), "/"); strings.EqualFold(provider, retiredBrowserProvider) {
			*e = ""
		}
	}
}

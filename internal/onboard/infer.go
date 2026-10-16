package onboard

import (
	"regexp"
	"strconv"
	"strings"
)

// A family of models, recognised by a piece of its name, with the tier and cost the family has.
// The first match wins, so the more specific names come first.
var families = []struct{ part, tier, cost string }{
	{"nano", "basic", "low"},
	{"lite", "basic", "low"},
	{"-mini", "mid", "low"},
	{"flash", "mid", "low"},
	{"haiku", "mid", "low"},
	{"opus", "frontier", "high"},
	{"sonnet", "frontier", "mid"},
	{"-pro", "frontier", "mid"},
	{"gpt-5", "frontier", "mid"},
	{"gpt-4", "mid", "mid"},
}

// paramsB finds the size an open model names in its tag ("qwen3:30b", "gemma4:31b").
var paramsB = regexp.MustCompile(`(\d+(?:\.\d+)?)b\b`)

// Sizes, in billions of parameters, from which an open model is counted mid or frontier.
const (
	midParams      = 30
	frontierParams = 100
)

// Infer guesses the tier and cost of a model the catalogue does not list, from its family or, for
// Ollama, its size. It gives only what the name supports: an unknown model gets "" and "", and no
// price is ever inferred.
func Infer(provider, id string) (tier, cost string) {
	id = strings.ToLower(id)
	switch provider {
	case "ollama":
		return inferOpen(id)
	case "ollama-cloud":
		// Hosted open models are billed, so the size says how capable one is and nothing about what
		// it costs.
		tier, _ = inferOpen(id)
		return tier, ""
	default:
		// Every other provider is told apart by its model's family below.
	}
	for _, f := range families {
		if strings.Contains(id, f.part) {
			return f.tier, f.cost
		}
	}
	return "", ""
}

// inferOpen is a model run through Ollama: local, with a tier by size when its name says it.
func inferOpen(id string) (tier, cost string) {
	m := paramsB.FindStringSubmatch(id)
	if m == nil {
		return "", "local"
	}
	size, _ := strconv.ParseFloat(m[1], 64)
	switch {
	case size >= frontierParams:
		return "frontier", "local"
	case size >= midParams:
		return "mid", "local"
	default:
		return "basic", "local"
	}
}

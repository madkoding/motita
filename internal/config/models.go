package config

import (
	"fmt"
	"os"
	"strings"
	"unicode"
)

// THE MODEL OF EACH KIND OF WORK.
//
// llm.provider and llm.model are the DEFAULT: what every call runs on unless the kind of work it
// does names a model of its own here. A planning turn can run on a model that reasons well, the
// pieces handed to background agents on a faster one, a review on a third, and the small calls
// (a title, a compaction summary, auto mode's guess) on a cheap one. An empty entry follows the
// default, so a configuration without this block behaves exactly as before.
//
// The cheap and strong entries are TIERS, not kinds of work: a background agent started for an
// easy piece runs on cheap (a small or local model), a hard one on strong, and a cheap agent that
// fails is run again on strong. With cheap empty nothing is routed and every agent runs on the
// model of its role, as before.
//
// Each entry is "provider/model": the provider is one of Providers, which never holds a slash,
// so the model after the first slash may hold its own ("ollama/library/qwen3:8b").
// The key and the endpoint are the provider's own, found the way a provider switch finds them.
//
// It is read by each SESSION from its own configuration: the engines a session builds for these
// entries belong to that session, and so do the background agents that run on them.

// The kinds of work a model can be assigned to.
const (
	TaskPlan     = "plan"     // plan mode: read-only turns that explore and propose
	TaskCode     = "code"     // task mode: the main agent, and the background agents that write
	TaskReview   = "review"   // background agents that review the work and change nothing
	TaskResearch = "research" // background agents that investigate and change nothing
	TaskSummary  = "summary"  // the small calls: titles, summaries, auto mode's guess, the audit and synthesis
	TaskCheap    = "cheap"    // tier: easy pieces handed to background agents (a small or local model)
	TaskStrong   = "strong"   // tier: hard pieces, and where a cheap agent escalates when it fails
)

// TaskKinds lists the kinds, in the order the interfaces show them.
var TaskKinds = []string{TaskPlan, TaskCode, TaskReview, TaskResearch, TaskSummary, TaskCheap, TaskStrong}

// Models is the models block: one "provider/model" per kind of work, empty for the default.
type Models struct {
	Plan     string `yaml:"plan"`
	Code     string `yaml:"code"`
	Review   string `yaml:"review"`
	Research string `yaml:"research"`
	Summary  string `yaml:"summary"`
	Cheap    string `yaml:"cheap"`
	Strong   string `yaml:"strong"`
}

// ModelChoice is one entry of the models block, split.
type ModelChoice struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Empty reports whether the choice follows the default.
func (m ModelChoice) Empty() bool { return m.Provider == "" && m.Model == "" }

// String is the choice as the block writes it.
func (m ModelChoice) String() string {
	if m.Empty() {
		return ""
	}
	return m.Provider + "/" + m.Model
}

// entry is the raw value of one kind, and whether the kind exists.
func (m Models) entry(kind string) (string, bool) {
	switch kind {
	case TaskPlan:
		return m.Plan, true
	case TaskCode:
		return m.Code, true
	case TaskReview:
		return m.Review, true
	case TaskResearch:
		return m.Research, true
	case TaskSummary:
		return m.Summary, true
	case TaskCheap:
		return m.Cheap, true
	case TaskStrong:
		return m.Strong, true
	default:
		return "", false
	}
}

// For is the model assigned to a kind of work: empty when it follows the default, when the kind is
// unknown, or when the entry cannot be read (validation refuses such an entry before it is used).
func (m Models) For(kind string) ModelChoice {
	raw, _ := m.entry(kind)
	c, err := ParseModelChoice(raw)
	if err != nil {
		return ModelChoice{}
	}
	return c
}

// ParseModelChoice splits "provider/model". An empty value is the default.
func ParseModelChoice(raw string) (ModelChoice, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ModelChoice{}, nil
	}
	provider, model, ok := strings.Cut(raw, "/")
	provider, model = strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(model)
	if !ok || model == "" {
		return ModelChoice{}, fmt.Errorf("%q is not provider/model", raw)
	}
	if err := oneOf("the provider", provider, Providers...); err != nil {
		return ModelChoice{}, err
	}
	if len(model) > 200 || strings.IndexFunc(model, unicode.IsControl) >= 0 {
		return ModelChoice{}, fmt.Errorf("%q is not a model name", model)
	}
	return ModelChoice{Provider: provider, Model: model}, nil
}

// validateModels checks every entry of the models block.
func (c *Config) validateModels() error {
	for _, kind := range TaskKinds {
		raw, _ := c.Models.entry(kind)
		if _, err := ParseModelChoice(raw); err != nil {
			return fmt.Errorf("models.%s: %w", kind, err)
		}
	}
	return c.validateThinking()
}

// SetTaskModels writes the models block into the configuration at path, in place and atomically:
// one line per kind, empty for the default, every other line of the file as it was.
func SetTaskModels(path string, choices map[string]ModelChoice) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	kv := make([][2]string, 0, len(TaskKinds))
	for _, kind := range TaskKinds {
		c := choices[kind]
		if !c.Empty() {
			checked, err := ParseModelChoice(c.String())
			if err != nil {
				return fmt.Errorf("models.%s: %w", kind, err)
			}
			c = checked
		}
		kv = append(kv, [2]string{kind, yamlQuoted(c.String())})
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	updated, err := setBlockValues(string(body), "models", kv)
	if err != nil {
		return err
	}
	return writeConfigAtomic(path, []byte(updated), info.Mode().Perm())
}

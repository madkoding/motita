package config

import (
	"fmt"
	"os"
	"strings"
)

// HOW HARD THE MODEL THINKS ON EACH KIND OF WORK.
//
// llm.reasoning is the session's level: what every call thinks at unless the kind of work it does
// names a level of its own here. Planning can think hard while the small calls (a title, a summary)
// think not at all. An empty entry follows the session's level, so a configuration without this
// block behaves exactly as before. The kinds are the ones of the models block.

// ThinkingLevels are the levels an entry may name, besides empty (the session's level).
var ThinkingLevels = []string{"off", "low", "medium", "high"}

// Thinking is the thinking block: one level per kind of work, empty for the session's level.
type Thinking struct {
	Plan     string `yaml:"plan"`
	Code     string `yaml:"code"`
	Review   string `yaml:"review"`
	Research string `yaml:"research"`
	Summary  string `yaml:"summary"`
	Cheap    string `yaml:"cheap"`
	Strong   string `yaml:"strong"`
}

// entry is the raw value of one kind, and whether the kind exists.
func (t Thinking) entry(kind string) (string, bool) {
	switch kind {
	case TaskPlan:
		return t.Plan, true
	case TaskCode:
		return t.Code, true
	case TaskReview:
		return t.Review, true
	case TaskResearch:
		return t.Research, true
	case TaskSummary:
		return t.Summary, true
	case TaskCheap:
		return t.Cheap, true
	case TaskStrong:
		return t.Strong, true
	default:
		return "", false
	}
}

// For is the level assigned to a kind of work: empty when it follows the session's level, when the
// kind is unknown, or when the entry is not a level (validation refuses such an entry before use).
func (t Thinking) For(kind string) string {
	raw, _ := t.entry(kind)
	level, err := ParseThinkingLevel(raw)
	if err != nil {
		return ""
	}
	return level
}

// ParseThinkingLevel normalises one entry. An empty value is the session's level.
func ParseThinkingLevel(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if err := oneOf("the thinking level", raw, ThinkingLevels...); err != nil {
		return "", err
	}
	return raw, nil
}

// validateThinking checks every entry of the thinking block.
func (c *Config) validateThinking() error {
	for _, kind := range TaskKinds {
		raw, _ := c.Thinking.entry(kind)
		if _, err := ParseThinkingLevel(raw); err != nil {
			return fmt.Errorf("thinking.%s: %w", kind, err)
		}
	}
	return nil
}

// SetTaskThinking writes the thinking block into the configuration at path, in place and
// atomically: one line per kind, empty for the session's level, every other line as it was.
func SetTaskThinking(path string, levels map[string]string) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	kv := make([][2]string, 0, len(TaskKinds))
	for _, kind := range TaskKinds {
		level, err := ParseThinkingLevel(levels[kind])
		if err != nil {
			return fmt.Errorf("thinking.%s: %w", kind, err)
		}
		kv = append(kv, [2]string{kind, yamlQuoted(level)})
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	updated, err := setBlockValues(string(body), "thinking", kv)
	if err != nil {
		return err
	}
	return writeConfigAtomic(path, []byte(updated), info.Mode().Perm())
}

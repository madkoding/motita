package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// THE CUSTOM MODEL.
//
// The custom provider speaks the OpenAI chat protocol at an endpoint of the user's choosing, so
// nothing in a catalogue says what its model accepts. The custom_model block does: the model's
// name and the sampling and size limits it runs with. They apply only to that model on the custom
// provider; every other model keeps llm.temperature, llm.max_tokens and llm.session.context_window.

// ProviderCustom is the id of the custom provider.
const ProviderCustom = "custom"

// Bounds a custom model's numbers must keep to, so a typo cannot ask for an absurd request.
const (
	maxCustomTemperature = 2
	maxCustomTokens      = 10_000_000
)

// CustomModel is the custom_model block.
type CustomModel struct {
	// Model is the name the endpoint knows the model by ("llama-3.3-70b-instruct").
	Model string `yaml:"model" json:"model"`
	// Temperature is the sampling temperature, nil for the endpoint's default.
	Temperature *float64 `yaml:"temperature" json:"temperature,omitempty"`
	// MaxTokens caps the tokens of one answer; zero keeps llm.max_tokens.
	MaxTokens int `yaml:"max_tokens" json:"max_tokens"`
	// ContextWindow is the model's window in tokens; zero keeps what the session derives.
	ContextWindow int `yaml:"context_window" json:"context_window"`
}

// Empty reports whether the block names no model.
func (m CustomModel) Empty() bool { return strings.TrimSpace(m.Model) == "" }

// Validate checks the block; an empty one is valid.
func (m CustomModel) Validate() error {
	if m.Empty() {
		return nil
	}
	if len(m.Model) > 200 || strings.IndexFunc(m.Model, unicode.IsControl) >= 0 {
		return fmt.Errorf("custom_model.model: %q is not a model name", m.Model)
	}
	if t := m.Temperature; t != nil && (*t < 0 || *t > maxCustomTemperature) {
		return fmt.Errorf("custom_model.temperature must be between 0 and %d", maxCustomTemperature)
	}
	if m.MaxTokens < 0 || m.MaxTokens > maxCustomTokens {
		return fmt.Errorf("custom_model.max_tokens must be between 0 and %d", maxCustomTokens)
	}
	if m.ContextWindow < 0 || m.ContextWindow > maxCustomTokens*10 {
		return fmt.Errorf("custom_model.context_window must be between 0 and %d", maxCustomTokens*10)
	}
	return nil
}

// applies reports whether the block describes the model this LLM block runs.
func (l LLM) customApplies() bool {
	return strings.EqualFold(l.Provider, ProviderCustom) && !l.Custom.Empty() && l.Custom.Model == l.Model
}

// Tuned is the LLM block with the custom model's temperature and answer limit in place of the
// general ones, when it runs that model; otherwise it is returned as it is.
func (l LLM) Tuned() LLM {
	if !l.customApplies() {
		return l
	}
	if l.Custom.Temperature != nil {
		l.Temperature = l.Custom.Temperature
	}
	if l.Custom.MaxTokens > 0 {
		l.MaxTokens = l.Custom.MaxTokens
	}
	return l
}

// WindowOverride is the context window the configuration forces: the custom model's when it runs
// that model, else llm.session.context_window. Zero means "ask the model".
func (l LLM) WindowOverride() int {
	if l.customApplies() && l.Custom.ContextWindow > 0 {
		return l.Custom.ContextWindow
	}
	return l.Session.ContextWindow
}

// SetCustomModel writes the custom_model block into the configuration at path, in place and
// atomically, every other line as it was.
func SetCustomModel(path string, m CustomModel) error {
	if err := m.Validate(); err != nil {
		return err
	}
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	temperature := "null"
	if m.Temperature != nil {
		temperature = strconv.FormatFloat(*m.Temperature, 'f', -1, 64)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path) // #nosec G304 -- the configuration file this process was started with.
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	updated, err := setBlockValues(string(body), "custom_model", [][2]string{
		{"model", yamlQuoted(strings.TrimSpace(m.Model))},
		{"temperature", temperature},
		{"max_tokens", strconv.Itoa(m.MaxTokens)},
		{"context_window", strconv.Itoa(m.ContextWindow)},
	})
	if err != nil {
		return err
	}
	return writeConfigAtomic(path, []byte(updated), info.Mode().Perm())
}

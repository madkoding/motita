package config

import (
	"fmt"
	"path/filepath"
)

// ContextMemory is the archive of the conversation in progress: the turns that have left
// the model's window, kept on disk so a later turn can still recall them.
//
// It is NOT the person's memory (see memorydir.go): that one is a folder they write in and
// keep, and recall reaches everything ever written there. This archive belongs to one
// conversation, is written by the agent as the conversation moves, and is deleted with the
// conversation. It lives apart so deleting a session never touches what the person saved.
//
// It is on by default because it is what makes a long conversation survive its own window;
// `enabled: false` turns it off and nothing is written.
type ContextMemory struct {
	Enabled bool `yaml:"enabled"`
	// RecallTokens is the hard budget one recall may add to a prompt. RecallHits is how
	// many archived sections it returns.
	RecallTokens int `yaml:"recall_tokens"`
	RecallHits   int `yaml:"recall_hits"`
	// MaxTurns bounds one conversation's archive; the oldest turns are dropped.
	MaxTurns int `yaml:"max_turns"`
}

// ContextDir is the folder that holds one archive per conversation, ~/.motita/context.
// Empty without a home to keep it in.
func ContextDir() string {
	d := Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "context")
}

// validateContextMemory refuses a negative limit. Zero means "use the default", which is a
// value an operator can leave alone; a negative one is a number nobody meant.
func (c *Config) validateContextMemory() error {
	if c.ContextMemory.RecallTokens < 0 {
		return fmt.Errorf("context_memory.recall_tokens cannot be negative, got %d", c.ContextMemory.RecallTokens)
	}
	if c.ContextMemory.RecallHits < 0 {
		return fmt.Errorf("context_memory.recall_hits cannot be negative, got %d", c.ContextMemory.RecallHits)
	}
	if c.ContextMemory.MaxTurns < 0 {
		return fmt.Errorf("context_memory.max_turns cannot be negative, got %d", c.ContextMemory.MaxTurns)
	}
	return nil
}

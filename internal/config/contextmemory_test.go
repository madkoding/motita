package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The archive of the conversation in progress: one folder per conversation, apart from the
// person's memory, deleted with the conversation.

func TestContextMemoryIsOnByDefault(t *testing.T) {
	cfg := Default()
	if !cfg.ContextMemory.Enabled {
		t.Error("context_memory is off by default")
	}
	if cfg.ContextMemory.RecallTokens != 2000 || cfg.ContextMemory.RecallHits != 5 || cfg.ContextMemory.MaxTurns != 400 {
		t.Errorf("defaults = %+v", cfg.ContextMemory)
	}
}

func TestContextDirFollowsTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, want := ContextDir(), filepath.Join(home, ".motita", "context"); got != want {
		t.Errorf("ContextDir = %q, want %q", got, want)
	}

	restore := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = restore })
	t.Setenv("HOME", "")
	osUserHomeDir = func() (string, error) { return "", errors.New("no home") }
	if got := ContextDir(); got != "" {
		t.Errorf("ContextDir without a home = %q", got)
	}
}

func TestContextMemoryRefusesNegativeLimits(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"tokens", func(c *Config) { c.ContextMemory.RecallTokens = -1 }, "context_memory.recall_tokens"},
		{"hits", func(c *Config) { c.ContextMemory.RecallHits = -1 }, "context_memory.recall_hits"},
		{"turns", func(c *Config) { c.ContextMemory.MaxTurns = -1 }, "context_memory.max_turns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.change(&cfg)
			err := cfg.validateContextMemory()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to name %s", err, tc.want)
			}
		})
	}
	cfg := Default()
	cfg.ContextMemory = ContextMemory{}
	if err := cfg.validateContextMemory(); err != nil {
		t.Errorf("zero means the default: %v", err)
	}

	// The same through the whole validation: a negative limit stops Load, it does not warn.
	bad := Default()
	bad.ContextMemory.MaxTurns = -1
	if err := bad.ValidateWithoutKey(); err == nil || !strings.Contains(err.Error(), "context_memory.max_turns") {
		t.Errorf("ValidateWithoutKey = %v, want the negative limit named", err)
	}
}

func TestContextMemoryFromTheEnvironment(t *testing.T) {
	t.Setenv("MOTITA_CONTEXT_MEMORY_ENABLED", "false")
	t.Setenv("MOTITA_CONTEXT_MEMORY_RECALL_TOKENS", "1234")
	t.Setenv("MOTITA_CONTEXT_MEMORY_RECALL_HITS", "7")
	t.Setenv("MOTITA_CONTEXT_MEMORY_MAX_TURNS", "99")
	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	want := ContextMemory{Enabled: false, RecallTokens: 1234, RecallHits: 7, MaxTurns: 99}
	if cfg.ContextMemory != want {
		t.Errorf("context_memory = %+v, want %+v", cfg.ContextMemory, want)
	}
}

func TestContextMemoryLoadsFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "motita.yaml")
	body := "llm:\n  api_key: k\n" +
		"context_memory:\n  enabled: false\n  recall_tokens: 800\n" +
		"  recall_hits: 3\n  max_turns: 20\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatalf("LoadWithoutKey: %v", err)
	}
	want := ContextMemory{Enabled: false, RecallTokens: 800, RecallHits: 3, MaxTurns: 20}
	if cfg.ContextMemory != want {
		t.Errorf("context_memory = %+v, want %+v", cfg.ContextMemory, want)
	}
}

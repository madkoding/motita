package config

import (
	"strings"
	"testing"
)

// The session settings are read from the environment like every other numeric setting, and
// a value that cannot be parsed must be reported rather than silently ignored: an operator
// who set the reserve to "4k" needs to know it did nothing, not discover it when the agent
// overflows its context.

func TestSessionSettingsComeFromTheEnvironment(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_SESSION_CONTEXT_WINDOW", "32000")
	t.Setenv("STARLIGHT_LLM_SESSION_RESERVE", "2048")
	t.Setenv("STARLIGHT_LLM_SESSION_KEEP_RECENT", "8")
	t.Setenv("STARLIGHT_LLM_SESSION_COMPACT_AT", "0.6")

	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.LLM.Session.ContextWindow != 32000 {
		t.Errorf("context window = %d, want 32000", cfg.LLM.Session.ContextWindow)
	}
	if cfg.LLM.Session.Reserve != 2048 {
		t.Errorf("reserve = %d, want 2048", cfg.LLM.Session.Reserve)
	}
	if cfg.LLM.Session.KeepRecent != 8 {
		t.Errorf("keep recent = %d, want 8", cfg.LLM.Session.KeepRecent)
	}
	if cfg.LLM.Session.CompactAt != 0.6 {
		t.Errorf("compact at = %v, want 0.6", cfg.LLM.Session.CompactAt)
	}
}

// TestAnEmptyVariableLeavesTheDefault: an exported-but-empty variable is how a shell
// script leaves a setting alone, and it must not become a zero.
func TestAnEmptyVariableLeavesTheDefault(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_SESSION_CONTEXT_WINDOW", "")
	t.Setenv("STARLIGHT_LLM_SESSION_RESERVE", "   ")
	t.Setenv("STARLIGHT_LLM_SESSION_COMPACT_AT", "")

	cfg := Default()
	before := cfg.LLM.Session
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Session != before {
		t.Errorf("empty variables must change nothing: %+v, was %+v", cfg.LLM.Session, before)
	}
}

// TestAnUnparsableSettingIsReported: each one names itself, so the operator knows which
// line of their script is wrong.
func TestAnUnparsableSettingIsReported(t *testing.T) {
	for _, key := range []string{
		"STARLIGHT_LLM_SESSION_CONTEXT_WINDOW",
		"STARLIGHT_LLM_SESSION_RESERVE",
		"STARLIGHT_LLM_SESSION_KEEP_RECENT",
		"STARLIGHT_LLM_SESSION_COMPACT_AT",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "not-a-number")
			cfg := Default()
			err := ApplyEnvironment(&cfg)
			if err == nil {
				t.Fatalf("%s must be reported when it cannot be parsed", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("the error must name the variable, got %v", err)
			}
		})
	}
}

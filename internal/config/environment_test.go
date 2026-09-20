package config

import (
	"strings"
	"testing"
)

// TestTheSkillsDirectoryIsOverridable: the library is where a user keeps their procedures, so
// where it lives has to be configurable the same way as everything else.
func TestTheSkillsDirectoryIsOverridable(t *testing.T) {
	t.Setenv("STARLIGHT_SKILLS_DIR", "/tmp/my-skills")

	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.Dir != "/tmp/my-skills" {
		t.Errorf("Skills.Dir = %q, want the variable's value", cfg.Skills.Dir)
	}
}

// TestTheSkillsCapIsOverridable: the cap decides how large a document may be before it would
// cost more context than it returns, so it is a policy the user sets.
func TestTheSkillsCapIsOverridable(t *testing.T) {
	t.Setenv("STARLIGHT_SKILLS_MAX_FILE_BYTES", "2048")

	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.MaxFileBytes != 2048 {
		t.Errorf("Skills.MaxFileBytes = %d, want 2048", cfg.Skills.MaxFileBytes)
	}
}

// TestAMalformedSkillsCapIsReported: a value that cannot be read must be an error rather than
// a silent fallback to the default. A user who typed a size and got the old one would never
// know their setting was ignored.
func TestAMalformedSkillsCapIsReported(t *testing.T) {
	t.Setenv("STARLIGHT_SKILLS_MAX_FILE_BYTES", "not-a-number")

	cfg := Default()
	err := ApplyEnvironment(&cfg)
	if err == nil {
		t.Fatal("a malformed number must be reported")
	}
	if !strings.Contains(err.Error(), "STARLIGHT_SKILLS_MAX_FILE_BYTES") {
		t.Errorf("the error must name the variable: %v", err)
	}
}

// The effect is configurable without editing the file, so a user can try it — or turn it off for
// one run — with nothing written down.
func TestTheEffectIsConfigurableFromTheEnvironment(t *testing.T) {
	t.Setenv("STARLIGHT_CRT_ENABLED", "false")
	t.Setenv("STARLIGHT_CRT_TYPEWRITER", "off")
	t.Setenv("STARLIGHT_CRT_TYPEWRITER_CPS", "120")

	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if c.CRT.Enabled {
		t.Error("enabled must be off")
	}
	if c.CRT.Typewriter {
		t.Error("typewriter must be off")
	}
	if c.CRT.TypewriterCPS != 120 {
		t.Errorf("typewriter_cps = %v", c.CRT.TypewriterCPS)
	}
}

// A value that cannot be parsed is reported, naming the variable: silently keeping the default
// would leave the user wondering why their setting did nothing.
func TestEffectEnvironmentErrorsAreReported(t *testing.T) {
	for _, c := range []struct{ key, value string }{
		{"STARLIGHT_CRT_ENABLED", "quizas"},
		{"STARLIGHT_CRT_TYPEWRITER", "quizas"},
		{"STARLIGHT_CRT_TYPEWRITER_CPS", "rapido"},
	} {
		t.Run(c.key, func(t *testing.T) {
			t.Setenv(c.key, c.value)
			cfg := Default()
			err := ApplyEnvironment(&cfg)
			if err == nil {
				t.Fatalf("%s=%q must be an error", c.key, c.value)
			}
			if !strings.Contains(err.Error(), c.key) {
				t.Errorf("the failure must name the variable: %v", err)
			}
		})
	}
}

// A blank variable is treated as unset rather than as an empty value: an exported-but-empty
// variable is a common accident, and it must not blank out a setting.
func TestBlankEffectVariablesLeaveTheDefaults(t *testing.T) {
	t.Setenv("STARLIGHT_CRT_TYPEWRITER_CPS", "  ")
	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if c.CRT.TypewriterCPS != Default().CRT.TypewriterCPS {
		t.Fatalf("a blank variable must leave the default, got %v", c.CRT.TypewriterCPS)
	}
}

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

// Every CRT setting can be read from the environment, so a user can try the effect without
// editing their file — and turn it off for one run without leaving a trace in the config.
func TestTheCRTEffectIsConfigurableFromTheEnvironment(t *testing.T) {
	t.Setenv("STARLIGHT_CRT_ENABLED", "false")
	t.Setenv("STARLIGHT_CRT_COLOR", "#00ff00")
	t.Setenv("STARLIGHT_CRT_GLOW", "off")
	t.Setenv("STARLIGHT_CRT_TYPEWRITER", "no")
	t.Setenv("STARLIGHT_CRT_SCANLINES", "0.75")
	t.Setenv("STARLIGHT_CRT_FLICKER", "0.02")
	t.Setenv("STARLIGHT_CRT_VIGNETTE", "0.5")
	t.Setenv("STARLIGHT_CRT_NOISE", "0.05")
	t.Setenv("STARLIGHT_CRT_TYPEWRITER_CPS", "120")

	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if c.CRT.Enabled {
		t.Error("enabled must be off")
	}
	if c.CRT.Color != "#00ff00" {
		t.Errorf("color = %q", c.CRT.Color)
	}
	if c.CRT.Glow {
		t.Error("glow must be off")
	}
	if c.CRT.Typewriter {
		t.Error("typewriter must be off")
	}
	if c.CRT.Scanlines != 0.75 || c.CRT.Flicker != 0.02 || c.CRT.Vignette != 0.5 || c.CRT.Noise != 0.05 {
		t.Errorf("intensities = %v %v %v %v", c.CRT.Scanlines, c.CRT.Flicker, c.CRT.Vignette, c.CRT.Noise)
	}
	if c.CRT.TypewriterCPS != 120 {
		t.Errorf("typewriter_cps = %v", c.CRT.TypewriterCPS)
	}
}

// A value that cannot be parsed is reported, naming the variable: silently keeping the default
// would leave the user wondering why their setting did nothing.
func TestCRTEffectEnvironmentErrorsAreReported(t *testing.T) {
	for _, c := range []struct{ key, value string }{
		{"STARLIGHT_CRT_ENABLED", "quizas"},
		{"STARLIGHT_CRT_GLOW", "quizas"},
		{"STARLIGHT_CRT_TYPEWRITER", "quizas"},
		{"STARLIGHT_CRT_SCANLINES", "mucho"},
		{"STARLIGHT_CRT_FLICKER", "mucho"},
		{"STARLIGHT_CRT_VIGNETTE", "mucho"},
		{"STARLIGHT_CRT_NOISE", "mucho"},
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
func TestBlankCRTVariablesLeaveTheDefaults(t *testing.T) {
	t.Setenv("STARLIGHT_CRT_COLOR", "")
	t.Setenv("STARLIGHT_CRT_SCANLINES", "  ")
	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if c.CRT.Color != Default().CRT.Color || c.CRT.Scanlines != Default().CRT.Scanlines {
		t.Fatalf("blank variables must leave the defaults, got %q and %v", c.CRT.Color, c.CRT.Scanlines)
	}
}

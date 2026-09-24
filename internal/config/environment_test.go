package config

import (
	"strings"
	"testing"
)

// TestTheSkillsDirectoryIsOverridable: the library is where a user keeps their procedures, so
// where it lives has to be configurable the same way as everything else.
func TestTheSkillsDirectoryIsOverridable(t *testing.T) {
	t.Setenv("MOTITA_SKILLS_DIR", "/tmp/my-skills")

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
	t.Setenv("MOTITA_SKILLS_MAX_FILE_BYTES", "2048")

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
	t.Setenv("MOTITA_SKILLS_MAX_FILE_BYTES", "not-a-number")

	cfg := Default()
	err := ApplyEnvironment(&cfg)
	if err == nil {
		t.Fatal("a malformed number must be reported")
	}
	if !strings.Contains(err.Error(), "MOTITA_SKILLS_MAX_FILE_BYTES") {
		t.Errorf("the error must name the variable: %v", err)
	}
}

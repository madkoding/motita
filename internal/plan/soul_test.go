package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveSoulFallsBackToTheEmbeddedDefault: with no ~/.motita/SOUL.md, the
// function returns the package SystemPrompt — the embedded soul plus the
// operational instructions. The caller never sees an empty string.
func TestResolveSoulFallsBackToTheEmbeddedDefault(t *testing.T) {
	got := ResolveSoul("/nonexistent-home-for-test")
	if got != SystemPrompt {
		t.Error("a home with no SOUL.md must return the embedded default")
	}
	if !strings.Contains(got, "Motita") {
		t.Error("the default soul must identify itself as Motita")
	}
	if !strings.Contains(got, "search_skills") {
		t.Error("the default soul must carry the operational instructions")
	}
}

// TestResolveSoulReadsAUserFile: when ~/.motita/SOUL.md exists and is non-empty,
// its content replaces the embedded personality. The operational instructions
// are still appended, so the procedure does not change with the personality.
func TestResolveSoulReadsAUserFile(t *testing.T) {
	dir := t.TempDir()
	soulDir := filepath.Join(dir, ".motita")
	if err := os.MkdirAll(soulDir, 0o700); err != nil {
		t.Fatal(err)
	}
	custom := "# My Agent\n\nYou are a pirate. Arrr!"
	if err := os.WriteFile(filepath.Join(soulDir, "SOUL.md"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ResolveSoul(dir)
	if !strings.Contains(got, "You are a pirate") {
		t.Error("a user SOUL.md must replace the embedded personality")
	}
	if strings.Contains(got, "Motita") {
		t.Error("a user SOUL.md must NOT carry the embedded personality")
	}
	if !strings.Contains(got, "search_skills") {
		t.Error("the operational instructions must follow whatever the soul says")
	}
}

// TestResolveSoulTreatsAnEmptyFileAsDefault: a zero-length SOUL.md is not a
// personality — it produces a model with no identity. The function falls back
// to the embedded default rather than sending the model nothing.
func TestResolveSoulTreatsAnEmptyFileAsDefault(t *testing.T) {
	dir := t.TempDir()
	soulDir := filepath.Join(dir, ".motita")
	if err := os.MkdirAll(soulDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(soulDir, "SOUL.md"), []byte("   \n\n  "), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ResolveSoul(dir)
	if got != SystemPrompt {
		t.Error("an empty SOUL.md must fall back to the embedded default")
	}
}

// TestResolveSoulWithEmptyHomeReturnsDefault: a caller that cannot determine
// the home directory passes an empty string, and gets the embedded default.
func TestResolveSoulWithEmptyHomeReturnsDefault(t *testing.T) {
	got := ResolveSoul("")
	if got != SystemPrompt {
		t.Error("an empty home must return the embedded default")
	}
}

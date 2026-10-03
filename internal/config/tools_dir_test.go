package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The tools directory defaults to ~/.motita-tools, outside every repository, and a check gets a
// timeout long enough for a whole project gate.
func TestToolsDirAndCheckTimeoutDefaults(t *testing.T) {
	c := Default()
	if want := filepath.Join(os.Getenv("HOME"), ".motita-tools"); c.Sandbox.ToolsDir != want {
		t.Errorf("tools_dir = %q, want %q", c.Sandbox.ToolsDir, want)
	}
	if c.Sandbox.CheckTimeout != 15*time.Minute {
		t.Errorf("check_timeout = %v, want 15m", c.Sandbox.CheckTimeout)
	}
	t.Setenv("HOME", "")
	if got := defaultToolsDir(); got != "" {
		t.Errorf("with no home the tools directory is off, got %q", got)
	}
}

// A YAML value is not read by a shell, so "~" is expanded here; with no home it stays as written.
func TestToolsDirExpandsTheHome(t *testing.T) {
	home := os.Getenv("HOME")
	for in, want := range map[string]string{
		"~":           home,
		"~/tools":     filepath.Join(home, "tools"),
		"/abs/tools":  "/abs/tools",
		"~other/x":    "~other/x",
		"  ~/spaced ": filepath.Join(home, "spaced"),
	} {
		c := Default()
		c.Sandbox.ToolsDir = in
		c.normalize()
		if c.Sandbox.ToolsDir != want {
			t.Errorf("tools_dir %q -> %q, want %q", in, c.Sandbox.ToolsDir, want)
		}
	}
	t.Setenv("HOME", "")
	if got := expandHome("~/x"); got != "~/x" {
		t.Errorf("with no home the path stays as written, got %q", got)
	}
}

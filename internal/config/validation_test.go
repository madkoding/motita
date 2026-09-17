package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadWithoutKeyDoesNotReplaceTheFile: the failure this prevents, found when
// testing the real binary, was that `-validar-config` with an invalid YAML
// reported "valid configuration" and exited with 0, because the failed load was
// silently replaced by the default values. It hid exactly the error the
// validation mode exists to detect.
func TestLoadWithoutKeyDoesNotReplaceTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("task_source:\n  kind: telepathy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadWithoutKey(path); err == nil {
		t.Fatal("an unknown source kind must fail even when the key is not required")
	} else if !strings.Contains(err.Error(), "telepathy") {
		t.Errorf("the error must say which value is invalid: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load must fail as well")
	}
}

func TestLoadWithoutKeyToleratesMissingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-key.yaml")
	content := "anchor:\n  kind: command\n  command: make\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// With no key: Load fails, LoadWithoutKey does not.
	if _, err := Load(path); err == nil {
		t.Fatal("Load must require the key")
	}
	cfg, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatalf("LoadWithoutKey must not require the key: %v", err)
	}
	if cfg.Anchor.Command != "make" {
		t.Errorf("the file was not read: %+v", cfg.Anchor)
	}
}

func TestLoadWithoutKeyKeepsValidatingEverythingElse(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		contains string
	}{
		{"unknown provider", "llm:\n  provider: wizard\n", "unknown llm.provider"},
		{"unknown sandbox", "sandbox:\n  kind: magic\n", "unknown sandbox.kind"},
		{"chroot without root", "sandbox:\n  kind: chroot\n", "requires 'root'"},
		{"unknown key", "llm:\n  color: blue\n", "color"},
		{"invalid level", "agent:\n  log_level: verbose\n", "unknown agent.log_level"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.yaml")
			os.WriteFile(path, []byte(tc.content), 0o644)
			_, err := LoadWithoutKey(path)
			if err == nil {
				t.Fatalf("an error was expected for %q", tc.content)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error = %q, it was expected to contain %q", err, tc.contains)
			}
		})
	}
}

func TestMissingFileFails(t *testing.T) {
	if _, err := LoadWithoutKey("/does/not/exist/config.yaml"); err == nil {
		t.Fatal("a missing file must fail, not fall back to the default values")
	}
}

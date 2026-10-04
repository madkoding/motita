package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUILanguageDefaultsToAutoAndIsValidated(t *testing.T) {
	c := Default()
	if c.UI.Language != "auto" {
		t.Fatalf("default ui.language = %q, want auto", c.UI.Language)
	}
	c.UI.Language = " ES "
	if err := c.ValidateWithoutKey(); err != nil || c.UI.Language != "es" {
		t.Fatalf("es must be accepted and normalised: %v %q", err, c.UI.Language)
	}
	c.UI.Language = "fr"
	err := c.ValidateWithoutKey()
	if err == nil || !strings.Contains(err.Error(), "ui.language") {
		t.Fatalf("fr must be refused, naming the setting: %v", err)
	}
}

func TestUILanguageComesFromTheEnvironment(t *testing.T) {
	t.Setenv("MOTITA_UI_LANGUAGE", "es")
	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatal(err)
	}
	if c.UI.Language != "es" {
		t.Fatalf("MOTITA_UI_LANGUAGE was ignored: %q", c.UI.Language)
	}
}

func writeConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func readConfig(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSetUILanguageEditsOnlyTheOneLine(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"an existing language is replaced, its comment kept",
			"# motita\nllm:\n  provider: openai  # mine\nui:\n    # the language\n    language: en   # en | es\nschedule:\n  enabled: true\n",
			"# motita\nllm:\n  provider: openai  # mine\nui:\n    # the language\n    language: es   # en | es\nschedule:\n  enabled: true\n",
		},
		{
			"a block without language gets one, at its own indentation",
			"ui:   # interfaces\n    other: x\nagent:\n  max_steps: 3\n",
			"ui:   # interfaces\n    language: es\n    other: x\nagent:\n  max_steps: 3\n",
		},
		{
			"an empty block followed by another key gets the default indentation",
			"ui:\nagent:\n  max_steps: 3\n",
			"ui:\n  language: es\nagent:\n  max_steps: 3\n",
		},
		{
			"no block: one is added at the end",
			"# comment\nllm:\n  provider: openai\n\n\n",
			"# comment\nllm:\n  provider: openai\n\nui:\n  language: es\n",
		},
		{
			"an empty file gets just the block",
			"",
			"ui:\n  language: es\n",
		},
	}
	for _, c := range cases {
		p := writeConfig(t, c.in, 0o600)
		if err := SetUILanguage(p, " ES "); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := readConfig(t, p); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s: the file's mode must be kept, got %v", c.name, info.Mode().Perm())
		}
		entries, _ := os.ReadDir(filepath.Dir(p))
		if len(entries) != 1 {
			t.Errorf("%s: no temporary file may be left behind: %v", c.name, entries)
		}
	}
}

func TestSetUILanguageRefusals(t *testing.T) {
	p := writeConfig(t, "ui:\n  language: en\n", 0o644)
	for _, bad := range []string{"", "fr"} {
		if err := SetUILanguage(p, bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if err := SetUILanguage(filepath.Join(t.TempDir(), "missing.yaml"), "es"); err == nil {
		t.Error("a missing file must be an error, not a new file")
	}
	inline := writeConfig(t, "ui: {language: en}\n", 0o644)
	if err := SetUILanguage(inline, "es"); err == nil || !strings.Contains(err.Error(), "inline") {
		t.Errorf("an inline ui value must be refused with a reason: %v", err)
	}
	if got := readConfig(t, inline); got != "ui: {language: en}\n" {
		t.Errorf("a refused edit must change nothing: %q", got)
	}
	dir := t.TempDir()
	if err := SetUILanguage(dir, "es"); err == nil {
		t.Error("a directory is not a configuration file")
	}
}

func TestSetUILanguageWriteFailures(t *testing.T) {
	p := writeConfig(t, "ui:\n  language: en\n", 0o644)
	boom := errors.New("disk full")

	uiCreateTemp = func(string, string) (*os.File, error) { return nil, boom }
	if err := SetUILanguage(p, "es"); !errors.Is(err, boom) {
		t.Errorf("a temp file that cannot be made must be reported: %v", err)
	}
	uiCreateTemp = os.CreateTemp

	uiCreateTemp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err == nil {
			_ = f.Close() // the write that follows fails on a closed file
		}
		return f, err
	}
	if err := SetUILanguage(p, "es"); err == nil {
		t.Error("a write that fails must be reported")
	}
	uiCreateTemp = os.CreateTemp

	uiRename = func(string, string) error { return boom }
	if err := SetUILanguage(p, "es"); !errors.Is(err, boom) {
		t.Errorf("a refused rename must be reported: %v", err)
	}
	uiRename = os.Rename

	if got := readConfig(t, p); got != "ui:\n  language: en\n" {
		t.Errorf("a failed write must leave the file as it was: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("no temporary file may be left behind: %v", entries)
	}
}

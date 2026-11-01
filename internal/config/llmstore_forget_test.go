package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClearLLMAPIKeyEmptiesOnlyTheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	body := "llm:\n  provider: openai # mine\n  api_key: sk-x\n"
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := ClearLLMAPIKey(path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if YAMLAPIKey(path) != "" || !strings.Contains(string(got), "provider: openai # mine") {
		t.Errorf("file:\n%s", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", info.Mode())
	}
	// Nothing to clear: the file is not written.
	before, _ := os.Stat(path)
	if err := ClearLLMAPIKey(path); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Error("a file without a key was rewritten")
	}
}

func TestClearLLMAPIKeyFailures(t *testing.T) {
	dir := t.TempDir()
	inline := filepath.Join(dir, "inline.yaml")
	if err := os.WriteFile(inline, []byte("llm: {provider: openai, api_key: sk}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClearLLMAPIKey(inline); err == nil {
		t.Error("an inline llm block was rewritten")
	}
	old := readConfigForClear
	defer func() { readConfigForClear = old }()
	readConfigForClear = func(string) (os.FileMode, []byte, error) { return 0, nil, os.ErrPermission }
	if err := ClearLLMAPIKey(inline); err == nil {
		t.Error("a read failure was ignored")
	}
	readConfigForClear = old
	// The reader itself: a missing file, and a directory that stats but does not read.
	if _, _, err := readConfigForClear(filepath.Join(dir, "gone.yaml")); err == nil {
		t.Error("a missing file read")
	}
	if _, _, err := readConfigForClear(dir); err == nil {
		t.Error("a directory read as a file")
	}
}

func TestForgetStoredValueClearsEveryCredentialsFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gw := filepath.Join(t.TempDir(), "motita.yaml")
	UseCredentialsBeside(gw)
	defer UseCredentialsBeside("")
	home := File()
	for _, p := range []string{gw, home} {
		if err := SetCredential(p, "GEMINI_API_KEY", "gm"); err != nil {
			t.Fatal(err)
		}
		if err := SetCredential(p, "OTHER", "o"); err != nil {
			t.Fatal(err)
		}
	}
	if err := ForgetStoredValue("GEMINI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{gw, home} {
		c := readCredentials(CredentialsPath(p))
		if c["GEMINI_API_KEY"] != "" || c["OTHER"] != "o" {
			t.Errorf("%s: %v", p, c)
		}
	}
	if err := ForgetStoredValue("NOT_THERE"); err != nil {
		t.Errorf("a name no file holds: %v", err)
	}
	// A file that cannot be written is reported.
	if err := SetCredential(gw, "GEMINI_API_KEY", "gm"); err != nil {
		t.Fatal(err)
	}
	old := clearCredential
	defer func() { clearCredential = old }()
	clearCredential = func(string, string) error { return os.ErrPermission }
	if err := ForgetStoredValue("GEMINI_API_KEY"); err == nil {
		t.Error("a failed write was not reported")
	}
}

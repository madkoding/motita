package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func setCodeIndex(path string, c CodeIndexChange) error {
	return Transaction(path, func(tx Tx) error { return tx.SetCodeIndex(c) })
}

func TestSetCodeIndexWritesOnlyWhatChanged(t *testing.T) {
	path := diskFile(t, "code_index:\n  enabled: true # keep\n  max_files: 10\nllm:\n  provider: openai\n")
	err := setCodeIndex(path, CodeIndexChange{
		Semantic: ptr(false), EmbeddingsAuto: ptr(false), EmbeddingsURL: ptr("http://localhost:11434/v1"),
		EmbeddingsModel: ptr("nomic-embed-text"), MaxFileKB: ptr(64), MaxFiles: ptr(20), Enabled: ptr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithoutKey(path)
	x := cfg.CodeIndex
	if err != nil || x.Semantic || x.EmbeddingsAuto || x.EmbeddingsURL != "http://localhost:11434/v1" ||
		x.EmbeddingsModel != "nomic-embed-text" || x.MaxFileKB != 64 || x.MaxFiles != 20 || !x.Enabled {
		t.Fatalf("loaded = %+v, %v", x, err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "enabled: true # keep") || !strings.Contains(string(body), "provider: openai") {
		t.Errorf("the rest of the file must stay: %q", body)
	}
	if err := setCodeIndex(path, CodeIndexChange{}); err != nil {
		t.Errorf("an empty change is nothing: %v", err)
	}
}

func TestSetCodeIndexRefusals(t *testing.T) {
	bad := CodeIndexChange{Semantic: ptr(false)}
	if err := (Tx{filepath.Join(t.TempDir(), "missing.yaml")}).SetCodeIndex(bad); err == nil {
		t.Error("a missing file")
	}
	if err := setCodeIndex(diskFile(t, "code_index: {}\n"), bad); err == nil {
		t.Error("an inline block must be refused")
	}
	if err := setCodeIndex(diskFile(t, "llm:\n  provider: openai\n"), CodeIndexChange{MaxFiles: ptr(-1)}); err == nil {
		t.Error("a value that does not validate must be refused")
	}
	old := uiCreateTemp
	t.Cleanup(func() { uiCreateTemp = old })
	uiCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("disk full") }
	if err := setCodeIndex(diskFile(t, "llm:\n  provider: openai\n"), bad); err == nil {
		t.Error("a failed write")
	}
}

func TestSetCodeIndexRefusesWhatTheEnvironmentOverrides(t *testing.T) {
	t.Setenv("MOTITA_CODE_INDEX_SEMANTIC", "true")
	path := diskFile(t, "llm:\n  provider: openai\n")
	before, _ := os.ReadFile(path)
	err := setCodeIndex(path, CodeIndexChange{Semantic: ptr(false)})
	if err == nil || !strings.Contains(err.Error(), "MOTITA_CODE_INDEX_") {
		t.Fatalf("err = %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("the file must be restored: %q", after)
	}
}

func TestIsLocalURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://localhost:11434/v1": true, "http://192.168.1.5:8080": true, "http://127.0.0.1:1": true,
		"https://api.openai.com/v1": false, "": false, "://bad": false,
	} {
		if got := IsLocalURL(raw); got != want {
			t.Errorf("IsLocalURL(%q) = %v", raw, got)
		}
	}
}

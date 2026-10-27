package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetSecretKeepsOtherLinesAndRemoves(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "motita.yaml")
	if SecretsPath(cfg) != strings.TrimSuffix(cfg, ".yaml")+".secrets.env" {
		t.Fatal(SecretsPath(cfg))
	}
	for _, kv := range [][2]string{{"A_TOKEN", "it's a 'quoted' one"}, {"B", "two"}, {"A_TOKEN", "three"}} {
		if err := SetSecret(cfg, kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	got := ReadSecrets(cfg)
	if got["A_TOKEN"] != "three" || got["B"] != "two" || len(got) != 2 {
		t.Errorf("got %v", got)
	}
	if names := SecretNames(got); names[0] != "A_TOKEN" || names[1] != "B" {
		t.Errorf("names %v", names)
	}
	if err := SetSecret(cfg, "B", ""); err != nil || len(ReadSecrets(cfg)) != 1 {
		t.Errorf("remove: %v %v", err, ReadSecrets(cfg))
	}
	if info, _ := os.Stat(SecretsPath(cfg)); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode())
	}
}

func TestSetSecretRefusals(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "motita.yaml")
	cases := map[string][3]string{
		"no config":  {"", "A", "x"},
		"bad name":   {cfg, "a-b", "x"},
		"reserved":   {cfg, "ld_preload", "x"},
		"multi-line": {cfg, "A", "a\nb"},
		"too long":   {cfg, "A", strings.Repeat("x", MaxSecretBytes+1)},
	}
	for name, c := range cases {
		if err := SetSecret(c[0], c[1], c[2]); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for i := 0; i < MaxSecrets; i++ {
		if err := SetSecret(cfg, "V"+strings.Repeat("A", i%5)+string(rune('A'+i/5))+string(rune('a'+i%26)), "x"); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := SetSecret(cfg, "ONE_MORE", "x"); err == nil {
		t.Error("the limit was not enforced")
	}
	if err := SetSecret(cfg, "ONE_MORE", ""); err != nil {
		t.Errorf("removing at the limit: %v", err)
	}
	if ReadSecrets("") != nil {
		t.Error("no file, no secrets")
	}
}

func TestCurrentSecretsMergesBothFiles(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "a", "motita.yaml")
	t.Cleanup(func() { UseSecretsBeside("") })
	UseSecretsBeside(shared)
	if err := SetSecret(shared, "SHARED", "1"); err != nil {
		t.Fatal(err)
	}
	// A name the sandbox owns written by hand into the file is ignored.
	_ = os.WriteFile(SecretsPath(shared), append([]byte("export PATH=evil\nexport EMPTY=\n"), mustRead(t, SecretsPath(shared))...), 0o600)
	got := CurrentSecrets()
	if got["SHARED"] != "1" || got["PATH"] != "" || len(got) != 1 {
		t.Errorf("got %v", got)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

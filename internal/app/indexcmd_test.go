package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func indexConfig(t *testing.T, cacheDir string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "motita.yaml")
	body := "llm:\n  provider: ollama\n  model: m\ncode_index:\n  cache_dir: \"" + cacheDir + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runIndex(t *testing.T, config string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(Options{Args: append([]string{"-config", config}, args...), Out: &out, Err: &errOut})
	return code, out.String(), errOut.String()
}

func TestIndexStatusAndClearWorkOnTheCacheDir(t *testing.T) {
	cache := t.TempDir()
	if err := os.WriteFile(filepath.Join(cache, "a.gob"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := indexConfig(t, cache)
	if code, out, _ := runIndex(t, cfg, "index", "status"); code != Success || !strings.Contains(out, "1 index files") {
		t.Errorf("status = %d %q", code, out)
	}
	if code, out, _ := runIndex(t, cfg, "index", "clear"); code != Success || !strings.Contains(out, "1 index files deleted") {
		t.Errorf("clear = %d %q", code, out)
	}
	if code, out, _ := runIndex(t, cfg, "index", "status"); code != Success || !strings.Contains(out, "0 index files") {
		t.Errorf("status after = %d %q", code, out)
	}
	if code, out, _ := runIndex(t, indexConfig(t, ""), "index", "clear"); code != Success || !strings.Contains(out, "memory only") {
		t.Errorf("no cache dir = %d %q", code, out)
	}
}

func TestIndexClearReportsAFailure(t *testing.T) {
	code, _, errOut := runIndex(t, indexConfig(t, "["), "index", "clear")
	if code != RunError || !strings.Contains(errOut, "could not clear") {
		t.Errorf("a cache dir that cannot be listed = %d %q", code, errOut)
	}
}

func TestIndexNeedsAKnownAction(t *testing.T) {
	for _, args := range [][]string{{"index"}, {"index", "wipe"}} {
		var errOut bytes.Buffer
		if code := Run(Options{Args: args, Out: &bytes.Buffer{}, Err: &errOut}); code != ConfigError ||
			!strings.Contains(errOut.String(), indexActions) {
			t.Errorf("%v = %d %q", args, code, errOut.String())
		}
	}
}

func TestIndexFailsOnABrokenConfiguration(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(bad, []byte("llm: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runIndex(t, bad, "index", "status"); code != ConfigError {
		t.Errorf("code = %d", code)
	}
}

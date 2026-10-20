package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Setters that edit the same file at once keep every edit: each reads the file after the other has
// written it, instead of both starting from the same text and the second erasing the first.
func TestConcurrentSettersKeepEveryEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  provider: openai\n  model: m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := SetAskUnconfinedRuns(path, i%2 == 0); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := SetIsolateNetwork(path, i%2 == 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	body, _ := os.ReadFile(path)
	text := string(body)
	for _, key := range []string{"ask_unconfined_runs:", "isolate_network:"} {
		if strings.Count(text, key) != 1 {
			t.Errorf("%s must appear exactly once:\n%s", key, text)
		}
	}
	if !strings.Contains(text, "provider: openai") {
		t.Errorf("the rest of the file must survive:\n%s", text)
	}
}

// SetCredential calls that run at once keep every key: each reads the file after the other wrote it.
func TestConcurrentSetCredentialKeepsEveryKey(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "motita.yaml")
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := SetCredential(cfg, fmt.Sprintf("K%d", i), "v"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	body, _ := os.ReadFile(CredentialsPath(cfg))
	for i := 0; i < 40; i++ {
		if !strings.Contains(string(body), fmt.Sprintf("export K%d=", i)) {
			t.Errorf("K%d was lost", i)
		}
	}
}

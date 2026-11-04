package config

import (
	"path/filepath"
	"testing"
)

// The local model's window is the one its server was last started with, it has no usage to
// follow, and a configured window still wins.
func TestTheLocalProviderRunsWithItsServersWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	UseCredentialsBeside(path)
	t.Cleanup(func() { UseCredentialsBeside(File()) })
	local := LLM{Provider: ProviderLocal, Model: "qwen3-8b-gsq-q2_k"}
	if got := local.WindowOverride(); got != 0 {
		t.Errorf("never started: %d", got)
	}
	for value, want := range map[string]int{"8192": 8192, "-3": 0, "junk": 0} {
		if err := SetCredential(path, LocalContextVariable, value); err != nil {
			t.Fatal(err)
		}
		if got := local.WindowOverride(); got != want {
			t.Errorf("%q: window %d", value, got)
		}
	}
	local.Session.ContextWindow = 4096
	if got := local.WindowOverride(); got != 4096 {
		t.Errorf("configured window: %d", got)
	}
	if ReportsUsage(ProviderLocal) {
		t.Error("the local model has no usage to follow")
	}
	if ProviderKeyVariable(ProviderLocal) != "MOTITA_LOCAL_API_KEY" {
		t.Error("its own key variable")
	}
}

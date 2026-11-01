package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// TestTheApprovalTimeoutIsParsedOnlyWhenTheFileChanges: an unchanged file is not parsed again at
// each question, a changed one is, and a file that is gone or broken is no answer.
func TestTheApprovalTimeoutIsParsedOnlyWhenTheFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed := 0
	wait := 5 * time.Minute
	fail := false
	oldLoad := loadConfig
	loadConfig = func(string) (config.Config, error) {
		parsed++
		if fail {
			return config.Config{}, errors.New("broken yaml")
		}
		var c config.Config
		c.Agent.ApprovalTimeout = wait
		return c, nil
	}
	defer func() { loadConfig = oldLoad }()

	var f fileTimeout
	for range 3 {
		if got, ok := f.read(path); !ok || got != 5*time.Minute {
			t.Fatalf("read = %s, %v", got, ok)
		}
	}
	if parsed != 1 {
		t.Errorf("an unchanged file must be parsed once, parsed %d times", parsed)
	}
	wait = time.Minute
	if err := os.WriteFile(path, []byte("xy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.read(path); got != time.Minute {
		t.Errorf("a changed file must be read again: %s", got)
	}
	fail = true
	if err := os.WriteFile(path, []byte("xyz"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.read(path); ok {
		t.Error("a broken file is no answer")
	}
	if _, ok := f.read(filepath.Join(t.TempDir(), "gone.yaml")); ok {
		t.Error("a missing file is no answer")
	}
}

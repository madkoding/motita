package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain gives the whole package a git identity in a throwaway global
// configuration. A project folder is turned into a repository when it is
// created, and that needs a git user; without this the tests would read - and
// write - the developer's real ~/.gitconfig.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gateway-gitconfig")
	if err != nil {
		panic(err)
	}
	cfg := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Test\n\temail = test@example.com\n"), 0o600); err != nil {
		panic(err)
	}
	os.Setenv("GIT_CONFIG_GLOBAL", cfg)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

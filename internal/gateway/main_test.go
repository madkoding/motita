package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain gives the whole package a git identity in a throwaway configuration
// and cuts the package off from the machine's own git configuration. A project
// folder is turned into a repository when it is created, and that needs a git
// user; without this the tests would read - and write - the developer's real
// ~/.gitconfig. The system file (/etc/gitconfig) is disabled too: a machine that
// sets commit.gpgsign=true there makes every test commit try to sign with a key
// that does not exist, and the failure looks like a bug in motita.
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
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

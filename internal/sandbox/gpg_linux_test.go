//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/gpgagent"
)

// signingHome is a home with a git configuration that signs every commit, and a GnuPG home with a
// key that has no passphrase and an agent running. It is short because the agent's socket path has
// a length limit.
func signingHome(t *testing.T) (home, work string) {
	t.Helper()
	for _, tool := range []string{"git", "gpg", "gpgconf", "gpg-connect-agent"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	home, err := os.MkdirTemp("", "g")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("gpgconf", "--homedir", filepath.Join(home, ".gnupg"), "--kill", "gpg-agent").Run()
		os.RemoveAll(home)
	})
	gnupg := filepath.Join(home, ".gnupg")
	os.MkdirAll(gnupg, 0o700)
	t.Setenv("GNUPGHOME", gnupg)
	gpg := exec.Command("gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase", "",
		"--quick-generate-key", "Signer <signer@example.com>", "default", "default", "never")
	if out, err := gpg.CombinedOutput(); err != nil {
		t.Skipf("no key could be made: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(
		"[user]\n\tname = Signer\n\temail = signer@example.com\n\tsigningkey = signer@example.com\n[commit]\n\tgpgsign = true\n"), 0o644)
	work = filepath.Join(home, "work")
	os.MkdirAll(work, 0o755)
	init := exec.Command("git", "init", "-q", "-b", "main")
	init.Dir = work
	init.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return home, work
}

const signedCommit = "echo x > f.txt && git add f.txt && git commit -q -m signed && git log --format=%G? -1"

// With the user's agent granted, a confined command signs its commit with their key and cannot
// read the private keys.
func TestAConfinedCommandSignsWithTheUsersAgent(t *testing.T) {
	needLandlock(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	home, work := signingHome(t)
	res := gpgagent.Resolve(context.Background(), home)
	if !res.Access.Enabled() {
		t.Skipf("the agent is not reachable here: %s", res.Problem)
	}
	s, err := New(Options{Dir: work, ConfineWrites: true, GitHome: home, GPG: res.Access, ToolsDir: filepath.Join(home, "tools")})
	if err != nil {
		t.Fatal(err)
	}
	out, _, code, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", signedCommit}})
	if err != nil || code != 0 {
		t.Fatalf("the signed commit failed: code=%d err=%v out=%q", code, err, out)
	}
	if !strings.Contains(out, "U") && !strings.Contains(out, "G") {
		t.Errorf("the commit must carry a good signature: %q", out)
	}
	keys := filepath.Join(res.Access.Home, "private-keys-v1.d")
	if entries, err := os.ReadDir(keys); err != nil || len(entries) == 0 {
		t.Fatalf("the test needs a private key to hide: %v", err)
	}
	out, _, _, _ = s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", "ls " + keys + " && cat " + keys + "/*"}})
	if strings.Contains(out, ".key") || strings.Contains(out, "PRIVATE KEY") {
		t.Errorf("the private keys must stay hidden: %q", out)
	}
}

// Without the grant the same commit fails, and the private keys are hidden whether or not it is on,
// including on the whole disk.
func TestWithoutTheGrantACommandCannotSign(t *testing.T) {
	needLandlock(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	home, work := signingHome(t)
	s, err := New(Options{Dir: work, ConfineWrites: true, GitHome: home, FullDisk: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _, code, _ := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", signedCommit}})
	if code == 0 {
		t.Error("a command with no grant must not sign")
	}
	keys := filepath.Join(home, ".gnupg", "private-keys-v1.d")
	out, _, _, _ := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", "cat " + keys + "/*"}})
	if strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, "OpenPGP") {
		t.Errorf("the private keys must be hidden on the whole disk: %q", out)
	}
}

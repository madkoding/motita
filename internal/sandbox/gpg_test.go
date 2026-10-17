package sandbox

import (
	"context"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/logx"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/gpgagent"
)

func gpgSandbox(t *testing.T, access gpgagent.Access, tools string) *Sandbox {
	t.Helper()
	s, err := New(Options{Dir: t.TempDir(), ToolsDir: tools, GPG: access})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A sandbox with no GPG grant, or no tools directory to keep a view in, gives no GNUPGHOME.
func TestNoGPGViewWithoutAGrantOrAHomeForIt(t *testing.T) {
	if got := gpgSandbox(t, gpgagent.Access{}, t.TempDir()).gpgView(); got != "" {
		t.Errorf("no grant: %q", got)
	}
	if got := gpgSandbox(t, gpgagent.Access{Home: "/g", Socket: "/s"}, "").gpgView(); got != "" {
		t.Errorf("no tools directory: %q", got)
	}
}

// The view links the public keyring, points at the agent, and is refreshed on every run.
func TestGPGViewLinksThePublicKeyringAndPointsAtTheAgent(t *testing.T) {
	g := t.TempDir()
	os.WriteFile(filepath.Join(g, "pubring.kbx"), []byte("pub"), 0o600)
	os.WriteFile(filepath.Join(g, "gpg.conf"), []byte("x"), 0o600)
	os.MkdirAll(filepath.Join(g, "private-keys-v1.d"), 0o700)
	s := gpgSandbox(t, gpgagent.Access{Home: g, Socket: "/run/s"}, t.TempDir())
	view := s.gpgView()
	if view == "" || !strings.HasPrefix(view, s.Home()) {
		t.Fatalf("view = %q", view)
	}
	if got, _ := os.ReadFile(filepath.Join(view, "pubring.kbx")); string(got) != "pub" {
		t.Errorf("the keyring must be linked: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(view, "S.gpg-agent")); string(got) != "%Assuan%\nsocket=/run/s\n" {
		t.Errorf("redirect = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(view, "private-keys-v1.d")); err == nil {
		t.Error("the private keys must not be in the view")
	}
	if !slices.Contains(s.environment(), "GNUPGHOME="+view) {
		t.Errorf("GNUPGHOME missing: %v", s.environment())
	}
	// A second run keeps what is right; a key file that went away is unlinked, a new one linked.
	os.Remove(filepath.Join(g, "gpg.conf"))
	os.WriteFile(filepath.Join(g, "pubring.gpg"), []byte("old"), 0o600)
	if again := s.gpgView(); again != view {
		t.Errorf("view moved: %q", again)
	}
	if _, err := os.Lstat(filepath.Join(view, "gpg.conf")); err == nil {
		t.Error("a link to a file that is gone must be removed")
	}
	if _, err := os.Lstat(filepath.Join(view, "pubring.gpg")); err != nil {
		t.Error("a new key file must be linked")
	}
	// A link that points elsewhere is replaced.
	os.Remove(filepath.Join(view, "pubring.kbx"))
	os.Symlink("/nowhere", filepath.Join(view, "pubring.kbx"))
	s.gpgView()
	if got, _ := os.ReadFile(filepath.Join(view, "pubring.kbx")); string(got) != "pub" {
		t.Errorf("a stale link must be replaced: %q", got)
	}
}

func TestGPGViewReportsWhatItCannotWrite(t *testing.T) {
	g := t.TempDir()
	tools := t.TempDir()
	s := gpgSandbox(t, gpgagent.Access{Home: g, Socket: "/run/s"}, tools)
	// A file where the view directory should be.
	os.WriteFile(filepath.Join(s.Home(), ".gnupg-motita"), nil, 0o600)
	if got := s.gpgView(); got != "" {
		t.Errorf("a view that cannot be made: %q", got)
	}
	os.Remove(filepath.Join(s.Home(), ".gnupg-motita"))
	// A directory where a file of the view should be.
	view := filepath.Join(s.Home(), ".gnupg-motita")
	os.MkdirAll(filepath.Join(view, "common.conf", "x"), 0o700)
	if got := s.gpgView(); got != "" {
		t.Errorf("a view that cannot be written: %q", got)
	}
	// A directory where a link should be.
	os.RemoveAll(view)
	os.WriteFile(filepath.Join(g, "pubring.kbx"), nil, 0o600)
	os.MkdirAll(filepath.Join(view, "pubring.kbx", "x"), 0o700)
	if got := s.gpgView(); got == "" {
		t.Error("a link that cannot be made is reported, not fatal")
	}
}

// What a confined command may read: the public keyring and the socket, never the private keys,
// which are hidden whether or not signing is on.
func TestGPGReadRootsAndHiddenPaths(t *testing.T) {
	on := gpgSandbox(t, gpgagent.Access{Home: "/g", Socket: "/run/s"}, t.TempDir())
	roots := on.readRoots(on.base)
	for _, want := range []string{"/g/pubring.kbx", "/g/gpg.conf", "/run/s"} {
		if !slices.Contains(roots, want) {
			t.Errorf("%s must be readable: %v", want, roots)
		}
	}
	if slices.Contains(roots, "/g/private-keys-v1.d") {
		t.Error("the private keys must not be granted")
	}
	if hidden := on.hiddenPaths(nil); !slices.Contains(hidden, "/g/private-keys-v1.d") || !slices.Contains(hidden, "/g/secring.gpg") {
		t.Errorf("hidden = %v", hidden)
	}
	t.Setenv("GNUPGHOME", "")
	off, err := New(Options{Dir: t.TempDir(), GitHome: "/home/u"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(off.readRoots(off.base), "/home/u/.gnupg/pubring.kbx") {
		t.Error("nothing is granted when signing is off")
	}
	if hidden := off.hiddenPaths(nil); !slices.Contains(hidden, "/home/u/.gnupg/private-keys-v1.d") {
		t.Errorf("the private keys are hidden even when signing is off: %v", hidden)
	}
}

func TestGPGAccessIsOffWhenTheSettingIs(t *testing.T) {
	if GPGAccess(false, t.TempDir(), nil).Enabled() {
		t.Error("off means nothing is granted")
	}
	// No git configuration to sign with: nothing to grant, nothing to warn about.
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	if GPGAccess(true, t.TempDir(), nil).Enabled() {
		t.Error("git is not set to sign")
	}
}

// A signing setup that cannot be given is logged with what to do about it.
func TestGPGAccessLogsWhySigningCannotBeGiven(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[commit]\n\tgpgsign = true\n"), 0o644)
	t.Setenv("GNUPGHOME", filepath.Join(home, "none"))
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	logFile := filepath.Join(t.TempDir(), "log")
	logger, err := logx.New(logx.Options{Path: logFile, Level: logx.Warn, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	if GPGAccess(true, home, logger).Enabled() {
		t.Fatal("there is no GnuPG home")
	}
	logger.Close()
	if got, _ := os.ReadFile(logFile); !strings.Contains(string(got), "no GnuPG home") {
		t.Errorf("the reason must be logged: %q", got)
	}
}

// A failed signature in a command's output gets the hint, only when the agent was granted.
func TestAFailedSignatureGetsTheHint(t *testing.T) {
	failing := execx.Request{Command: "/bin/sh", Args: []string{"-c", "echo 'error: gpg failed to sign the data'; exit 1"}}
	g := t.TempDir()
	on := gpgSandbox(t, gpgagent.Access{Home: g, Socket: "/run/s"}, t.TempDir())
	out, _, code, err := on.Run(context.Background(), failing)
	if err != nil || code != 1 || !strings.Contains(out, "sandbox.gpg_signing") {
		t.Errorf("hint missing: %d %v %q", code, err, out)
	}
	off := gpgSandbox(t, gpgagent.Access{}, t.TempDir())
	if out, _, _, _ := off.Run(context.Background(), failing); strings.Contains(out, "sandbox.gpg_signing") {
		t.Errorf("no hint without the grant: %q", out)
	}
}

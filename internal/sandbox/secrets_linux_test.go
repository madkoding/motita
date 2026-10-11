//go:build linux

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/madkoding/motita/internal/execx"
)

// motitaHome lays out a motita home with logins, a stored key and a file that is not a secret.
func motitaHome(t *testing.T) (home, auth, keys string) {
	t.Helper()
	home = t.TempDir()
	auth = filepath.Join(home, "auth")
	keys = filepath.Join(home, "motita.env")
	os.MkdirAll(auth, 0o700)
	os.WriteFile(filepath.Join(auth, "git-github.json"), []byte("TOKEN-IN-AUTH"), 0o600)
	os.WriteFile(keys, []byte("KEY-IN-ENV"), 0o600)
	os.WriteFile(filepath.Join(home, "notes.txt"), []byte("plain-notes"), 0o644)
	return home, auth, keys
}

// A prompt-injected script reads motita's logins and keys and sends them away. Confined, it cannot
// read them by name nor through a link it plants in the workspace; everything else stays readable.
func TestAConfinedCommandCannotReadMotitasSecrets(t *testing.T) {
	needLandlock(t)
	home, auth, keys := motitaHome(t)
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "mine.txt"), []byte("workspace-file"), 0o644)
	s, err := New(Options{Dir: work, ConfineWrites: true, HiddenPaths: []string{auth, keys}})
	if err != nil {
		t.Fatal(err)
	}
	script := "cat " + filepath.Join(auth, "git-github.json") + " " + keys + "; ln -s " + auth + " link; cat link/git-github.json; " +
		"cat " + filepath.Join(home, "notes.txt") + " mine.txt; head -c1 /etc/passwd >/dev/null && echo system-readable"
	out, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "TOKEN-IN-AUTH") || strings.Contains(out, "KEY-IN-ENV") {
		t.Errorf("a confined command read motita's secrets:\n%s", out)
	}
	for _, want := range []string{"plain-notes", "workspace-file", "system-readable"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q: what is not a secret must stay readable:\n%s", want, out)
		}
	}

	out, _, _, err = s.Run(context.Background(), execx.Request{Command: "/bin/cat", Args: []string{keys}, Unconfined: true})
	if err != nil || !strings.Contains(out, "KEY-IN-ENV") {
		t.Errorf("what the user approved is not confined: %v %q", err, out)
	}
}

func TestReadableAroundGrantsEverythingButTheWay(t *testing.T) {
	if readableAround(nil) != nil || readableAround([]string{"relative/path"}) != nil || readableAround([]string{"/"}) != nil {
		t.Error("with nothing to hide nothing is restricted")
	}
	home, auth, keys := motitaHome(t)
	os.Symlink(auth, filepath.Join(home, "to-auth"))
	os.Symlink(home, filepath.Join(home, "to-home"))
	os.Symlink(filepath.Join(home, "missing"), filepath.Join(home, "dangling"))
	os.Symlink(filepath.Join(home, "notes.txt"), filepath.Join(home, "to-notes"))
	got := readableAround([]string{auth, keys, filepath.Join(home, "not-yet", "there")})
	joined := "\n" + strings.Join(got, "\n") + "\n"
	for _, never := range []string{auth, keys, home, filepath.Dir(home), "/"} {
		if strings.Contains(joined, "\n"+never+"\n") {
			t.Errorf("%s is or holds a hidden path and must not be granted: %v", never, got)
		}
	}
	if !strings.Contains(joined, "\n"+filepath.Join(home, "notes.txt")+"\n") || !strings.Contains(joined, "\n/usr\n") {
		t.Errorf("the rest must be granted: %v", got)
	}

	real := readDir
	t.Cleanup(func() { readDir = real })
	readDir = func(string) ([]os.DirEntry, error) { return nil, syscall.EACCES }
	if readableAround([]string{auth}) != nil {
		t.Error("a directory on the way that cannot be listed leaves reads as they were")
	}
}

func TestResolveExistingKeepsWhatIsNotThereYet(t *testing.T) {
	d := t.TempDir()
	os.Symlink(d, filepath.Join(d, "link"))
	if got := resolveExisting(filepath.Join(d, "link", "a", "b")); got != filepath.Join(d, "a", "b") {
		t.Errorf("got %s", got)
	}
}

func TestAHiddenPathThatHoldsARootIsNotHidden(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	s := &Sandbox{op: Options{HiddenPaths: []string{"/home/u/.motita/auth", "/home/u", "relative",
		"/home/u/.motita/motita.env", "/home/u/project/motita.yaml"}}}
	got := s.hiddenPaths([]string{"/home/u/project", "/dev"})
	if strings.Join(got, " ") != "/home/u/.motita/auth /home/u/.motita/motita.env" {
		t.Errorf("hiding a directory a command writes in, or a file it could move, is no hiding: %v", got)
	}
}

// The user's git credential stores hold the same kind of token as motita's logins.
func TestTheGitCredentialStoresAreHidden(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	s := &Sandbox{op: Options{GitHome: "/home/u"}}
	got := strings.Join(s.hiddenPaths([]string{"/work"}), " ")
	if got != "/home/u/.git-credentials /home/u/.config/git/credentials /xdg/git/credentials" {
		t.Errorf("got %s", got)
	}
}

func TestAConfinedCommandCannotReadTheGitCredentialStore(t *testing.T) {
	needLandlock(t)
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".git-credentials"), []byte("https://u:TOKEN@host\n"), 0o600)
	s, err := New(Options{Dir: t.TempDir(), ConfineWrites: true, GitHome: home})
	if err != nil {
		t.Fatal(err)
	}
	out, _, _, _ := s.Run(context.Background(), execx.Request{Command: "/bin/cat", Args: []string{filepath.Join(home, ".git-credentials")}})
	if strings.Contains(out, "TOKEN") {
		t.Errorf("a confined command read the git credential store: %q", out)
	}
}

// With something to hide the ruleset also handles reads, and a read rule that cannot be added
// fails the confinement like a write rule.
func TestConfineWritesHandlesReadsWhenSomethingIsHidden(t *testing.T) {
	needLandlock(t)
	_, auth, _ := motitaHome(t)
	real := landlockSyscall
	t.Cleanup(func() { landlockSyscall = real })
	for _, failing := range []bool{true, false} {
		rules := 0
		landlockSyscall = func(trap, a1, a2, a3 uintptr) (uintptr, uintptr, syscall.Errno) {
			switch {
			case trap == sysLandlockCreateRuleset && a1 == 0:
				return 3, 0, 0
			case trap == sysLandlockCreateRuleset:
				return uintptr(mustOpenDevNull(t)), 0, 0
			case trap == sysLandlockAddRule:
				rules++
				if failing {
					return 0, 0, syscall.EINVAL
				}
			}
			return 0, 0, 0
		}
		// No write root: every rule added is one granting reads around the hidden path.
		err := confineWrites(nil, []string{auth})
		if (err != nil) != failing || rules == 0 {
			t.Errorf("failing=%v: err=%v rules=%d", failing, err, rules)
		}
	}
}

// motita's own environment carries the API keys, and /proc/<pid>/environ of a dumpable process is
// readable by every process of the same user.
func TestNewHidesTheProcessFromItsChildren(t *testing.T) {
	if _, err := New(Options{Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := syscall.RawSyscall(syscall.SYS_PRCTL, 3 /* PR_GET_DUMPABLE */, 0, 0); v != 0 {
		t.Errorf("the process is still dumpable: %d", v)
	}

	real := prctl
	t.Cleanup(func() { prctl = real })
	prctl = func(uintptr, uintptr, uintptr, uintptr) (uintptr, uintptr, syscall.Errno) { return 0, 0, syscall.EPERM }
	s, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(s.NotApplied(), " "), "process hiding") {
		t.Errorf("a failure to hide must be reported: %v", s.NotApplied())
	}
}

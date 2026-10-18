package gpgagent

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fake describes a machine: the answers of the programs Resolve runs, and which files exist.
type fake struct {
	git      map[string]string // `git config --get` answers by key
	gpgconf  string
	gpgErr   error
	missing  map[string]bool
	sockets  map[string]bool
	gitCalls int
}

func install(t *testing.T, f *fake) {
	t.Helper()
	oldCmd, oldStat, oldGetenv, oldGoos := command, stat, getenv, goos
	t.Cleanup(func() { command, stat, getenv, goos = oldCmd, oldStat, oldGetenv, oldGoos })
	goos = "linux"
	getenv = func(string) string { return "" }
	command = func(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
		switch name {
		case "git":
			f.gitCalls++
			key := args[len(args)-1]
			if v, ok := f.git[key]; ok {
				return []byte(v + "\n"), nil
			}
			return nil, errors.New("unset")
		case "gpgconf":
			return []byte(f.gpgconf), f.gpgErr
		default:
			// Any other program is a mistake of the test.
		}
		return nil, errors.New("unexpected " + name)
	}
	stat = func(p string) (os.FileInfo, error) {
		if f.missing[p] {
			return nil, os.ErrNotExist
		}
		return fakeInfo{name: p, socket: f.sockets[p]}, nil
	}
}

type fakeInfo struct {
	os.FileInfo
	name   string
	socket bool
}

func (i fakeInfo) IsDir() bool { return false }
func (i fakeInfo) Mode() os.FileMode {
	if i.socket {
		return os.ModeSocket
	}
	return 0o600
}

const conf = "homedir:%2Fh%2F.gnupg\nagent-socket:%2Frun%2Fuser%2F1%2Fgnupg%2FS.gpg-agent\n" +
	"agent-extra-socket:%2Frun%2Fuser%2F1%2Fgnupg%2FS.gpg-agent.extra\n"

func TestNothingWhenGitDoesNotSign(t *testing.T) {
	install(t, &fake{git: map[string]string{}})
	if res := Resolve(context.Background(), "/h"); res.Wanted || res.Access.Enabled() || res.Problem != "" {
		t.Errorf("no signing asked: %+v", res)
	}
}

func TestNothingOnWindowsOrWithoutAHome(t *testing.T) {
	f := &fake{git: map[string]string{"commit.gpgsign": "true"}}
	install(t, f)
	goos = "windows"
	if res := Resolve(context.Background(), "/h"); res.Wanted {
		t.Errorf("windows: %+v", res)
	}
	goos = "linux"
	if res := Resolve(context.Background(), " "); res.Wanted || f.gitCalls != 0 {
		t.Errorf("no home: %+v", res)
	}
}

func TestTagSigningAloneIsWanted(t *testing.T) {
	install(t, &fake{git: map[string]string{"tag.gpgsign": "true"}, gpgconf: conf,
		sockets: map[string]bool{"/run/user/1/gnupg/S.gpg-agent": true}})
	if res := Resolve(context.Background(), "/h"); !res.Wanted || !res.Access.Enabled() {
		t.Errorf("%+v", res)
	}
}

func TestSigningGrantsTheExtraSocketWhenThereIsOne(t *testing.T) {
	install(t, &fake{git: map[string]string{"commit.gpgsign": "true", "gpg.format": "OpenPGP"}, gpgconf: conf,
		sockets: map[string]bool{"/run/user/1/gnupg/S.gpg-agent": true, "/run/user/1/gnupg/S.gpg-agent.extra": true}})
	res := Resolve(context.Background(), "/h")
	if !res.Wanted || res.Problem != "" || res.Access.Home != "/h/.gnupg" ||
		res.Access.Socket != "/run/user/1/gnupg/S.gpg-agent.extra" {
		t.Errorf("%+v", res)
	}
}

func TestSigningFallsBackToTheMainSocket(t *testing.T) {
	install(t, &fake{git: map[string]string{"commit.gpgsign": "true"}, gpgconf: conf,
		sockets: map[string]bool{"/run/user/1/gnupg/S.gpg-agent": true}})
	if res := Resolve(context.Background(), "/h"); res.Access.Socket != "/run/user/1/gnupg/S.gpg-agent" {
		t.Errorf("%+v", res)
	}
}

func TestOnlyOpenPGPSigningIsOffered(t *testing.T) {
	install(t, &fake{git: map[string]string{"commit.gpgsign": "true", "gpg.format": "ssh"}})
	res := Resolve(context.Background(), "/h")
	if !res.Wanted || res.Access.Enabled() || !strings.Contains(res.Problem, "gpg.format=ssh") {
		t.Errorf("%+v", res)
	}
}

func TestAMissingGnuPGHomeIsReported(t *testing.T) {
	install(t, &fake{git: map[string]string{"commit.gpgsign": "true"}, gpgErr: errors.New("no gpgconf"),
		missing: map[string]bool{"/h/.gnupg": true}})
	res := Resolve(context.Background(), "/h")
	if res.Access.Enabled() || !strings.Contains(res.Problem, "no GnuPG home") {
		t.Errorf("%+v", res)
	}
}

func TestAnAgentThatIsNotRunningIsReportedWithWhatToDo(t *testing.T) {
	install(t, &fake{git: map[string]string{"commit.gpgsign": "true"}, gpgErr: errors.New("no gpgconf")})
	res := Resolve(context.Background(), "/h")
	if res.Access.Enabled() || !strings.Contains(res.Problem, "gpg-agent is not running") ||
		!strings.Contains(res.Problem, "/h/.gnupg/S.gpg-agent") {
		t.Errorf("%+v", res)
	}
}

func TestAFileThatIsNotASocketIsNotAnAgent(t *testing.T) {
	install(t, &fake{git: map[string]string{"commit.gpgsign": "true"}, gpgconf: conf})
	if res := Resolve(context.Background(), "/h"); res.Access.Enabled() || res.Problem == "" {
		t.Errorf("%+v", res)
	}
}

func TestGpgconfNoiseIsIgnored(t *testing.T) {
	install(t, &fake{git: map[string]string{"commit.gpgsign": "true"},
		gpgconf: "garbage\nhomedir:relative\nhomedir:%zz\nhomedir:%2Fother\n",
		sockets: map[string]bool{"/other/S.gpg-agent": true}})
	if res := Resolve(context.Background(), "/h"); res.Access.Home != "/other" {
		t.Errorf("%+v", res)
	}
}

func TestHome(t *testing.T) {
	install(t, &fake{})
	if got := Home("/h"); got != "/h/.gnupg" {
		t.Errorf("default = %q", got)
	}
	if got := Home(""); got != "" {
		t.Errorf("no home = %q", got)
	}
	getenv = func(string) string { return "/x/../custom" }
	if got := Home("/h"); got != "/custom" {
		t.Errorf("GNUPGHOME = %q", got)
	}
	getenv = func(string) string { return "relative" }
	if got := Home("/h"); got != "/h/.gnupg" {
		t.Errorf("a relative GNUPGHOME is ignored: %q", got)
	}
}

func TestPublicAndPrivatePathsNeverOverlap(t *testing.T) {
	private := PrivatePaths("/g")
	for _, p := range PublicPaths("/g") {
		for _, q := range private {
			if p == q || strings.HasPrefix(p, q+"/") {
				t.Errorf("%s is public and private", p)
			}
		}
	}
	if len(private) != 3 || !strings.HasSuffix(private[0], "private-keys-v1.d") {
		t.Errorf("private = %v", private)
	}
}

func TestFailedToSign(t *testing.T) {
	for out, want := range map[string]bool{
		"error: gpg failed to sign the data":                        true,
		"gpg: signing failed\nfatal: failed to write commit object": true,
		"fatal: failed to write commit object":                      false,
		"all good":                                                  false,
	} {
		if FailedToSign(out) != want {
			t.Errorf("FailedToSign(%q) != %v", out, want)
		}
	}
}

func TestGitEnvPicksTheUsersConfig(t *testing.T) {
	install(t, &fake{})
	home := t.TempDir()
	stat = os.Stat
	if got := gitEnv(home); got[0] != "GIT_CONFIG_GLOBAL="+os.DevNull {
		t.Errorf("none: %v", got)
	}
	os.MkdirAll(filepath.Join(home, ".config", "git"), 0o755)
	xdg := filepath.Join(home, ".config", "git", "config")
	os.WriteFile(xdg, nil, 0o644)
	if got := gitEnv(home); got[0] != "GIT_CONFIG_GLOBAL="+xdg {
		t.Errorf("xdg: %v", got)
	}
	main := filepath.Join(home, ".gitconfig")
	os.WriteFile(main, nil, 0o644)
	if got := gitEnv(home); got[0] != "GIT_CONFIG_GLOBAL="+main {
		t.Errorf("main: %v", got)
	}
}

// The real programs, against a socket this test listens on.
func TestCommandRunsTheRealPrograms(t *testing.T) {
	if _, err := command(context.Background(), []string{"X=1"}, "sh", "-c", "echo $X"); err != nil {
		t.Skip("no sh")
	}
	dir, err := os.MkdirTemp("", "s")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "S"))
	if err != nil {
		t.Skip("no unix sockets")
	}
	defer l.Close()
	info, err := os.Stat(filepath.Join(dir, "S"))
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Errorf("not a socket: %v", err)
	}
}

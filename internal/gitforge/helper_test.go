package gitforge

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/oauth"
)

func helperStore(t *testing.T) Store {
	s := Store{Dir: t.TempDir(), Env: noEnv}
	_ = s.Save(ghService(), oauth.Token{AccessToken: "gho_secret"}, "octo", false)
	bb := Defaults(noEnv)[2]
	_ = s.Save(bb, oauth.Token{AccessToken: "app-pw"}, "bbuser", true)
	return s
}

func ask(t *testing.T, s Store, action, request string) string {
	t.Helper()
	var out bytes.Buffer
	s.RunHelper(context.Background(), action, strings.NewReader(request), &out)
	return out.String()
}

func TestHelperAnswersForAConnectedHost(t *testing.T) {
	s := helperStore(t)
	got := ask(t, s, "get", "protocol=https\nhost=github.com\npath=a/b.git\n\n")
	if got != "username=x-access-token\npassword=gho_secret\n" {
		t.Errorf("github: %q", got)
	}
	got = ask(t, s, "get", "protocol=https\nhost=bitbucket.org\nusername=ignored\n")
	if got != "username=bbuser\npassword=app-pw\n" {
		t.Errorf("bitbucket: %q", got)
	}
	// A port after the host does not hide it.
	if got = ask(t, s, "get", "protocol=https\nhost=github.com:443\n"); !strings.Contains(got, "gho_secret") {
		t.Errorf("port: %q", got)
	}
}

func TestHelperStaysSilentWhenItCannotHelp(t *testing.T) {
	s := helperStore(t)
	for name, req := range map[string]string{
		"http":       "protocol=http\nhost=github.com\n",
		"no host":    "protocol=https\n",
		"ssh":        "protocol=ssh\nhost=github.com\n",
		"unknown":    "protocol=https\nhost=example.org\n",
		"not logged": "protocol=https\nhost=gitlab.com\n",
		"malformed":  "garbage\nprotocol=https\n",
	} {
		if got := ask(t, s, "get", req); got != "" {
			t.Errorf("%s: answered %q", name, got)
		}
	}
	// A login with no token at all is not an answer either.
	_ = oauth.SaveCredential(s.Dir, oauth.Credential{Provider: "git-gitlab", RefreshToken: "r"})
	if got := ask(t, s, "get", "protocol=https\nhost=gitlab.com\n"); got != "" {
		t.Errorf("empty token: %q", got)
	}
	// store and erase are accepted and do nothing: a wrong password once must
	// never delete a login the user made in the settings.
	for _, action := range []string{"store", "erase"} {
		if got := ask(t, s, action, "protocol=https\nhost=github.com\nusername=x\npassword=y\n"); got != "" {
			t.Errorf("%s answered %q", action, got)
		}
	}
	if !s.Connected(ghService()) {
		t.Error("erase must not remove the login")
	}
}

func TestGitEnv(t *testing.T) {
	home := t.TempDir()
	env := strings.Join(GitEnv("/opt/my motita/motita", "/auth", home), "\n")
	for _, want := range []string{
		"MOTITA_AUTH_DIR=/auth",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=!'/opt/my motita/motita' git-credential",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q in\n%s", want, env)
		}
	}
	if strings.Contains(env, "GIT_CONFIG_GLOBAL") {
		t.Error("there is no gitconfig to point at")
	}
	// The user's own configuration is passed on, so their identity and helpers
	// still apply inside the worktree.
	cfg := filepath.Join(home, ".gitconfig")
	_ = os.WriteFile(cfg, []byte("[user]\nname=x\n"), 0o600)
	if env = strings.Join(GitEnv("m", "", home), "\n"); !strings.Contains(env, "GIT_CONFIG_GLOBAL="+cfg) || strings.Contains(env, "MOTITA_AUTH_DIR") {
		t.Errorf("env = %s", env)
	}
	// The XDG location is the fallback.
	home2 := t.TempDir()
	xdg := filepath.Join(home2, ".config", "git", "config")
	_ = os.MkdirAll(filepath.Dir(xdg), 0o755)
	_ = os.WriteFile(xdg, nil, 0o600)
	if env = strings.Join(GitEnv("m", "", home2), "\n"); !strings.Contains(env, "GIT_CONFIG_GLOBAL="+xdg) {
		t.Errorf("env = %s", env)
	}
	// A directory is not a configuration file.
	home3 := t.TempDir()
	_ = os.Mkdir(filepath.Join(home3, ".gitconfig"), 0o755)
	if env = strings.Join(GitEnv("", "", home3), "\n"); strings.Contains(env, "GIT_CONFIG") {
		t.Errorf("env = %s", env)
	}
	if got := GitEnv("", "", ""); len(got) != 1 || got[0] != "GIT_TERMINAL_PROMPT=0" {
		t.Errorf("minimal env = %v", got)
	}
	if shellQuote("it's") != `'it'\''s'` {
		t.Errorf("quote = %s", shellQuote("it's"))
	}
}

func TestCloneEnvKeepsTheProcessEnvironment(t *testing.T) {
	t.Setenv("MOTITA_TEST_MARKER", "1")
	env := strings.Join(CloneEnv("m", "/auth"), "\n")
	if !strings.Contains(env, "MOTITA_TEST_MARKER=1") || !strings.Contains(env, "GIT_CONFIG_COUNT=1") {
		t.Errorf("env missing parts")
	}
}

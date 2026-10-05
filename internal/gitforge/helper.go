package gitforge

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// HelperCommand is the argument that makes the motita binary act as a git
// credential helper: git runs `motita git-credential get`.
const HelperCommand = "git-credential"

// RunHelper implements git's credential protocol for the logins motita holds.
//
// `get` answers with the stored token for the host git is asking about, and says
// nothing for a host that is not connected, which makes git fall through to the
// next helper (the user's own). `store` and `erase` are accepted and ignored:
// the logins are managed by the user in the settings, never rewritten by a git
// command that had a wrong password once.
func (s Store) RunHelper(ctx context.Context, action string, in io.Reader, out io.Writer) {
	if action != "get" {
		// Drain the request: git writes it before it reads, and closing early is a
		// broken pipe in its output.
		_, _ = io.Copy(io.Discard, in)
		return
	}
	req := map[string]string{}
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			req[k] = v
		}
	}
	if req["protocol"] != "https" || req["host"] == "" {
		return
	}
	svc, ok := s.ForHost(hostOnly(req["host"]))
	if !ok {
		return
	}
	cred, err := s.Credential(ctx, svc)
	if err != nil || cred.AccessToken == "" {
		return
	}
	fmt.Fprintf(out, "username=%s\npassword=%s\n", GitUser(svc, cred), cred.AccessToken)
}

// hostOnly drops the port git appends to a host that is not on 443.
func hostOnly(h string) string {
	host, _, _ := strings.Cut(h, ":")
	return host
}

// GitEnv is the environment that lets a `git` command run for the agent see
// what the user's terminal sees: it names motita as a credential helper, makes git
// fail instead of prompting for a password nobody can type, and points at the
// user's own git configuration.
//
// It exists because the agent's shell runs with a HOME of its own, so a
// worktree's git found neither the user's ~/.gitconfig (their identity, their
// credential helpers) nor any login: the credentials the user had configured
// globally were invisible exactly where they were needed.
//
// exe is the motita binary and authDir the directory of the logins. realHome is
// the user's real home; its .gitconfig, when there is one, is passed as the
// global configuration.
func GitEnv(exe, authDir, realHome string) []string {
	var env []string
	if authDir != "" {
		env = append(env, "MOTITA_AUTH_DIR="+authDir)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if realHome != "" {
		if cfg := filepath.Join(realHome, ".gitconfig"); fileExists(cfg) {
			env = append(env, "GIT_CONFIG_GLOBAL="+cfg)
		} else if xdg := filepath.Join(realHome, ".config", "git", "config"); fileExists(xdg) {
			env = append(env, "GIT_CONFIG_GLOBAL="+xdg)
		}
	}
	if exe != "" {
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=credential.helper",
			"GIT_CONFIG_VALUE_0=!"+shellQuote(exe)+" "+HelperCommand,
		)
	}
	return env
}

// CloneEnv is GitEnv for a command motita runs itself (the clone of a new
// project): it inherits the process environment, so only what is missing is added.
func CloneEnv(exe, authDir string) []string {
	return append(os.Environ(), GitEnv(exe, authDir, "")...)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// shellQuote wraps a path for the `!` form of credential.helper, which git hands
// to a shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

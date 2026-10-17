// Package gpgagent finds the user's gpg-agent so the git the agent runs can sign its commits with
// the keys the user already has, without a single key being copied.
//
// The sandbox gives a command a HOME of its own, so git found neither the user's signing
// configuration nor their GnuPG home, and `commit.gpgsign=true` made every commit fail with "gpg
// failed to sign the data". What a command needs is small: the public half of the keyring, to find
// the key, and the socket of the agent that holds the private half and does the signing. The
// private keys are never granted (see PrivatePaths), and a passphrase is the agent's business: a
// key that is not cached has nobody to ask in a sandboxed command, so Resolve reports it instead
// of leaving the commit to fail with git's one-line message.
package gpgagent

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Access is what a sandboxed command is granted to sign with the user's agent. The zero value
// grants nothing.
type Access struct {
	// Home is the user's GnuPG home, passed to the command as GNUPGHOME.
	Home string
	// Socket is the agent's socket, inside Home or under /run/user: the one file the command talks
	// to. It is the agent's restricted "extra" socket when there is one, which signs and decrypts but
	// cannot export a private key.
	Socket string
}

// Enabled reports whether a command is to be given anything.
func (a Access) Enabled() bool { return a.Home != "" }

// Result is what Resolve found.
type Result struct {
	Access Access
	// Wanted is whether the user's git configuration asks for signed commits.
	Wanted bool
	// Problem says, in words the user can act on, why signing was asked for and cannot be given.
	// Empty when Access is enabled or signing is not wanted.
	Problem string
}

// seams for the tests.
var (
	goos    = runtime.GOOS
	getenv  = os.Getenv
	stat    = os.Stat
	command = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = append(os.Environ(), env...)
		return cmd.Output()
	}
)

const probeTimeout = 5 * time.Second

// Resolve reads the user's global git configuration (realHome/.gitconfig or the XDG one, plus the
// system's) and, when it asks for signed OpenPGP commits, locates the GnuPG home and the agent's
// socket. It never starts an agent: one that is not running is a Problem to report.
func Resolve(ctx context.Context, realHome string) Result {
	if goos == "windows" || strings.TrimSpace(realHome) == "" {
		return Result{}
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	env := gitEnv(realHome)
	if !gitBool(ctx, env, "commit.gpgsign") && !gitBool(ctx, env, "tag.gpgsign") {
		return Result{}
	}
	res := Result{Wanted: true}
	if format := gitValue(ctx, env, "gpg.format"); format != "" && format != "openpgp" {
		res.Problem = fmt.Sprintf("git signs with gpg.format=%s, which does not use gpg-agent; "+
			"only openpgp signing is made available to commands", format)
		return res
	}
	home, socket, extra := locate(ctx, realHome)
	if home == "" {
		res.Problem = "git is set to sign commits but no GnuPG home was found; " +
			"set GNUPGHOME or create ~/.gnupg with `gpg --list-secret-keys`"
		return res
	}
	if info, err := stat(socket); err != nil || info.Mode()&os.ModeSocket == 0 {
		res.Problem = "git is set to sign commits but gpg-agent is not running (no socket at " + socket +
			"); start it once with `gpg-connect-agent /bye` (or enable the gpg-agent systemd socket) " +
			"and cache the key's passphrase with `echo test | gpg --clearsign >/dev/null`"
		return res
	}
	res.Access = Access{Home: home, Socket: socket}
	if info, err := stat(extra); err == nil && info.Mode()&os.ModeSocket != 0 {
		res.Access.Socket = extra
	}
	return res
}

// Hint is the line to show a command's output when git failed to sign: what to do, since a
// sandboxed command has no terminal for the agent's pinentry.
const Hint = "motita: signing failed. A sandboxed command cannot answer pinentry: unlock the key once " +
	"in your own terminal (`echo test | gpg --clearsign >/dev/null`) so gpg-agent caches the " +
	"passphrase, or use a key without one. Set sandbox.gpg_signing: false to stop using your agent."

// FailedToSign reports whether git's output says the signature could not be made.
func FailedToSign(output string) bool {
	return strings.Contains(output, "gpg failed to sign the data") ||
		strings.Contains(output, "failed to write commit object") && strings.Contains(output, "gpg")
}

// PublicNames are the entries of a GnuPG home a command's own view of it links to: the public
// keyring (both formats) and the options file. The trust database is left out, gpg makes its own
// in the view, which signing does not need.
var PublicNames = []string{"pubring.kbx", "pubring.gpg", "public-keys.d", "gpg.conf"}

// PublicPaths are the files of a GnuPG home a command reads to find the key it signs with. The
// private keys are not among them.
func PublicPaths(home string) []string {
	var out []string
	for _, name := range PublicNames {
		out = append(out, filepath.Join(home, name))
	}
	return out
}

// PrivatePaths are the files of a GnuPG home that hold private key material. A command is never
// able to read them, whether or not it signs: the agent, outside the sandbox, is the only one that
// does.
func PrivatePaths(home string) []string {
	var out []string
	for _, name := range []string{"private-keys-v1.d", "secring.gpg", "openpgp-revocs.d"} {
		out = append(out, filepath.Join(home, name))
	}
	return out
}

// Home is the GnuPG home that realHome's user has, without asking gpg: GNUPGHOME or ~/.gnupg.
func Home(realHome string) string {
	if h := strings.TrimSpace(getenv("GNUPGHOME")); filepath.IsAbs(h) {
		return filepath.Clean(h)
	}
	if realHome == "" {
		return ""
	}
	return filepath.Join(realHome, ".gnupg")
}

// locate asks gpgconf for the home and the agent's sockets, and falls back to the conventional ones.
func locate(ctx context.Context, realHome string) (home, socket, extra string) {
	home = Home(realHome)
	out, err := command(ctx, []string{"GNUPGHOME=" + home}, "gpgconf",
		"--list-dirs", "homedir", "agent-socket", "agent-extra-socket")
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok {
				continue
			}
			value, err := url.PathUnescape(value)
			if err != nil || !filepath.IsAbs(value) {
				continue
			}
			switch key {
			case "homedir":
				home = filepath.Clean(value)
			case "agent-socket":
				socket = filepath.Clean(value)
			case "agent-extra-socket":
				extra = filepath.Clean(value)
			default:
				// Other directories gpgconf lists are not ones a command needs.
			}
		}
	}
	if socket == "" {
		socket = filepath.Join(home, "S.gpg-agent")
	}
	if extra == "" {
		extra = filepath.Join(home, "S.gpg-agent.extra")
	}
	if _, err := stat(home); err != nil {
		return "", "", ""
	}
	return home, socket, extra
}

// gitEnv points git at the user's own configuration, as the sandboxed git is.
func gitEnv(realHome string) []string {
	configs := []string{filepath.Join(realHome, ".gitconfig"), filepath.Join(realHome, ".config", "git", "config")}
	for _, cfg := range configs {
		if info, err := stat(cfg); err == nil && !info.IsDir() {
			return []string{"GIT_CONFIG_GLOBAL=" + cfg}
		}
	}
	return []string{"GIT_CONFIG_GLOBAL=" + os.DevNull}
}

func gitValue(ctx context.Context, env []string, key string) string {
	out, err := command(ctx, env, "git", "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(out)))
}

func gitBool(ctx context.Context, env []string, key string) bool {
	out, err := command(ctx, env, "git", "config", "--type=bool", "--get", key)
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

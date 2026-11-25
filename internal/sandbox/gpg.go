package sandbox

import (
	"context"
	"os"
	"path/filepath"

	"github.com/madkoding/motita/internal/gpgagent"
	"github.com/madkoding/motita/internal/logx"
)

// GPGAccess is the access to the user's gpg-agent Options.GPG takes: what gpgagent.Resolve found
// in the git configuration of home, or nothing when sandbox.gpg_signing is off or git is not set
// to sign. A signing setup that cannot be given is logged with what to do about it, once per
// sandbox built, rather than left to show up as a failed commit.
func GPGAccess(enabled bool, home string, log *logx.Logger) gpgagent.Access {
	if !enabled {
		return gpgagent.Access{}
	}
	res := gpgagent.Resolve(context.Background(), home)
	if res.Problem != "" && log != nil {
		log.Warn("commits cannot be signed with your gpg-agent", "reason", res.Problem)
	}
	return res.Access
}

// gpgView is the GnuPG home a command gets, or "" when it gets none.
//
// It is not the user's own. gpg takes a lock beside the keyring it reads, which would need the
// user's GnuPG home writable, and a writable home is a way to plant a gpg-agent.conf whose
// pinentry-program the agent, running outside the sandbox, would execute. So the command gets a
// home of its own inside the HOME the sandbox keeps for it, holding links to the public half of
// the user's keyring and a pointer to the agent's socket (libassuan's socket redirection file):
// gpg finds the keys, asks the real agent to sign, and writes its locks in the view. The private
// keys are not linked, and the agent is never started from here (no-autostart).
func (s *Sandbox) gpgView() string {
	acc := s.op.GPG
	home := s.Home()
	if !acc.Enabled() || home == "" {
		return ""
	}
	// Everything below goes through os.Root: the HOME is written by the confined command, which can
	// leave a symlink where the view, a link or a file of it should be. This process then writes
	// with the user's own rights, and a path that followed the link would overwrite ~/.gnupg or
	// any other file the user owns.
	root, err := os.OpenRoot(home)
	if err != nil {
		s.log.Warn("the gpg view could not be made; commits cannot be signed", "error", err)
		return ""
	}
	defer root.Close()
	const viewName = ".gnupg-motita"
	view, err := openDir(root, viewName)
	if err != nil {
		s.log.Warn("the gpg view could not be made; commits cannot be signed", "error", err)
		return ""
	}
	defer view.Close()
	for _, name := range gpgagent.PublicNames {
		target := filepath.Join(acc.Home, name)
		if _, err := os.Stat(target); err != nil {
			_ = view.Remove(name)
			continue
		}
		if cur, err := view.Readlink(name); err == nil && cur == target {
			continue
		}
		_ = view.Remove(name)
		if err := view.Symlink(target, name); err != nil {
			s.log.Warn("the gpg view could not link the keyring", "name", name, "error", err)
		}
	}
	files := map[string]string{
		"S.gpg-agent": "%Assuan%\nsocket=" + acc.Socket + "\n",
		"common.conf": "no-autostart\n",
	}
	for name, body := range files {
		if cur, err := view.ReadFile(name); err == nil && string(cur) == body {
			continue
		}
		if err := view.WriteFile(name, []byte(body), 0o600); err != nil {
			s.log.Warn("the gpg view could not be written", "name", name, "error", err)
			return ""
		}
	}
	return filepath.Join(home, viewName)
}

// openDir makes the directory name inside root, if it is not there, and opens it as a root of its
// own. A name that is a symlink out of root is an error, not a way out.
func openDir(root *os.Root, name string) (*os.Root, error) {
	if err := root.MkdirAll(name, 0o700); err != nil {
		return nil, err
	}
	return root.OpenRoot(name)
}

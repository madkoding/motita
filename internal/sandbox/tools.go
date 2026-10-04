package sandbox

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// systemPath is the PATH a sandboxed command starts from: the system directories and
// nothing of the user's own environment.
const systemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// THE TOOLS DIRECTORY, and why the agent's HOME, TMPDIR and toolchains live there.
//
// Reported from a real session on a machine with no go, node, gh or make: HOME was the working
// directory, so the toolchains the agent installed, plus .config and .gnupg, landed INSIDE the
// repository - gofmt then walked the Go toolchain and `make check` failed, and about ten rounds
// went into moving them out. PATH was fixed to the system directories, so nothing installed in one
// round existed in the next: every command re-exported a 200-character PATH, 22 rounds went into
// bootstrapping, and the anchor's `make check` failed in 3 ms because `make` was not on its PATH -
// the run could never pass. A directory outside every repository, persistent across sessions, with
// its bin directories on PATH, makes installing a toolchain a one-time cost instead of a per-round one.

// toolsDir is the tools directory this sandbox uses, or "" for the old layout. A chroot cannot
// see a directory outside its root, so it keeps the old layout too.
func (s *Sandbox) toolsDir() string {
	if s.op.UseChroot || strings.TrimSpace(s.op.ToolsDir) == "" {
		return ""
	}
	tools := filepath.Clean(s.op.ToolsDir)
	// HOME must exist for the programs that write to it. A failure to create it is not a
	// failure of the run (the command may not need a HOME), and it shows in the command's
	// own output when it does.
	_ = os.MkdirAll(filepath.Join(tools, "home"), 0o755)
	return tools
}

// tempRoot is where the per-run temporary directories are created: under the tools directory
// when there is one and it can be prepared, the working directory otherwise.
func (s *Sandbox) tempRoot() string {
	tools := s.toolsDir()
	if tools == "" {
		return s.base
	}
	root := filepath.Join(tools, "tmp")
	if err := os.MkdirAll(root, 0o755); err != nil {
		s.log.Warn("could not prepare the temporary root in the tools directory; using the working directory",
			"dir", root, "error", err)
		return s.base
	}
	return root
}

// ToolPath returns path with the tools directory's bin directories in front: <tools>/bin and
// every <tools>/tools/<name>/bin that exists. It is read on every call, so a toolchain
// unpacked in one round is on PATH in the next.
func ToolPath(tools, path string) string {
	if strings.TrimSpace(tools) == "" {
		return path
	}
	var dirs []string
	if isDir(filepath.Join(tools, "bin")) {
		dirs = append(dirs, filepath.Join(tools, "bin"))
	}
	// The pattern is fixed and well formed, so Glob cannot fail with ErrBadPattern.
	found, _ := filepath.Glob(filepath.Join(tools, "tools", "*", "bin"))
	sort.Strings(found)
	for _, d := range found {
		if isDir(d) {
			dirs = append(dirs, d)
		}
	}
	if path != "" {
		dirs = append(dirs, path)
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// ToolEnvironment is env (or this process' environment when env is nil) with the tools
// directory's bin directories in front of its PATH. It is for the commands that run outside the
// sandbox - the anchor without one - which inherit the environment instead of building it.
func ToolEnvironment(tools string, env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+ToolPath(tools, path))
}

// LookTool returns the absolute path of name in the tools directory's bin directories, or name
// unchanged when it is a path or is not there. A process started directly resolves its program
// against the PARENT's PATH, not the one handed to the child, so a tool the agent installed would
// not be found by name without this.
func LookTool(tools, name string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name
	}
	for _, dir := range filepath.SplitList(ToolPath(tools, "")) {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return name
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// HasTool reports whether name is an executable on the PATH this sandbox gives its commands: the
// tools directory's bin directories in front of the system ones. A name with a path, and a sandbox
// in a chroot (whose own tree this process cannot see), answer true: there is nothing to look up.
func (s *Sandbox) HasTool(name string) bool {
	if s.op.UseChroot || strings.ContainsRune(name, os.PathSeparator) {
		return true
	}
	for _, dir := range filepath.SplitList(ToolPath(s.toolsDir(), systemPath)) {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

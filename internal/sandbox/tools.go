package sandbox

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// systemPath is the PATH a sandboxed command starts from: the system directories and
// nothing of the user's own environment.
const systemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// commandPath is the PATH a sandboxed command gets before the tools directory's bin directories:
// systemPath, then the directories of motita's own PATH that are not among the user's files
// (privatePaths). Those hold what is installed for the whole machine outside the standard
// directories - /usr/local/go/bin, /snap/bin, /opt/homebrew/bin, /opt/<tool>/bin - which a command
// found as "not found" with systemPath alone. A directory in the user's home stays out: it is the
// user's own environment, and confined to the workspace it could not be read anyway.
//
// Windows has no system directories in POSIX form, so its PATH is motita's own, filtered the same
// way. Under a chroot motita's PATH names nothing inside the root, and systemPath is kept.
func (s *Sandbox) commandPath() string {
	if s.op.UseChroot {
		return systemPath
	}
	var dirs []string
	if goos != "windows" {
		dirs = strings.Split(systemPath, ":")
	}
	private := s.privatePaths()
	for _, d := range filepath.SplitList(getenv("PATH")) {
		if !filepath.IsAbs(d) || pathUnder(d, private) || slices.Contains(dirs, filepath.Clean(d)) {
			continue
		}
		dirs = append(dirs, filepath.Clean(d))
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

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
	_ = os.MkdirAll(s.homeIn(tools), 0o755)
	return tools
}

// Home is the HOME the sandbox gives its commands inside the tools directory, or "" for the old
// layout, where HOME is the working directory, and for no sandbox at all.
func (s *Sandbox) Home() string {
	if s == nil {
		return ""
	}
	tools := s.toolsDir()
	if tools == "" {
		return ""
	}
	return s.homeIn(tools)
}

// homeIn is Options.Home inside tools. A name that would leave the tools directory, or none, is
// the shared "home".
func (s *Sandbox) homeIn(tools string) string {
	name := filepath.Clean(strings.TrimSpace(s.op.Home))
	if name == "." || !filepath.IsLocal(name) {
		name = "home"
	}
	return filepath.Join(tools, name)
}

// ProjectHome is the HOME, relative to the tools directory, of the sessions of the project whose
// key is given: one per project, beside the shared "home" the sessions of no project use.
func ProjectHome(key string) string {
	if strings.TrimSpace(key) == "" {
		return ""
	}
	return filepath.Join("homes", key)
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
	if !s.op.Keep {
		// What a run could not delete, or a gateway that died mid-run left, piled up here for good.
		removeOlder(root, "tmp-", staleTempAge)
	}
	return root
}

// staleTempAge is when a run's temporary directory is surely left over: no run lives that long.
const staleTempAge = 24 * time.Hour

// removeOlder removes the entries of dir whose name starts with prefix and that were last changed
// more than age ago.
func removeOlder(dir, prefix string, age time.Duration) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > age {
			_ = removeTree(filepath.Join(dir, e.Name()))
		}
	}
}

// removeTree is os.RemoveAll that also removes what a command left read-only: a directory without
// write permission (`chmod -R a-w`, Go's module cache, a package manager's store) cannot be emptied,
// and every run that made one left its whole temporary directory behind. The directories are made
// writable and the removal tried again.
func removeTree(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	// Through an os.Root: a symlink the command left (or swapped in) cannot lead the chmod outside.
	if root, err := os.OpenRoot(dir); err == nil {
		_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = root.Chmod(p, 0o700) // #nosec G302 -- a directory must keep its search bit to be emptied.
			}
			return nil
		})
		_ = root.Close()
	}
	return os.RemoveAll(dir)
}

// ToolPath returns path with the tools directory's bin directories in front: <tools>/bin and
// every <tools>/tools/<name>/bin that exists. It is read on every call, so a toolchain
// unpacked in one round is on PATH in the next.
func ToolPath(tools, path string) string {
	return joinToolPath(tools, path, false)
}

// ToolPathLast is ToolPath with the tools directory's bin directories BEHIND path. The anchor uses
// it: the directory is written by the agent, so a `make`, `go` or `npm` the agent dropped there must
// not shadow the system's one - a shim that exits 0 would be a PASS nobody earned. A tool only the
// tools directory has is still found, because it is only behind path, not gone.
func ToolPathLast(tools, path string) string {
	return joinToolPath(tools, path, true)
}

func joinToolPath(tools, path string, last bool) string {
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
		if last {
			dirs = append([]string{path}, dirs...)
		} else {
			dirs = append(dirs, path)
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// ToolEnvironment is env (or this process' environment when env is nil) with the tools
// directory's bin directories in front of its PATH. It is for the commands that run outside the
// sandbox - the anchor without one - which inherit the environment instead of building it.
func ToolEnvironment(tools string, env []string) []string {
	return toolEnvironment(tools, env, false)
}

// ToolEnvironmentLast is ToolEnvironment with the tools directory behind the system PATH (see
// ToolPathLast).
func ToolEnvironmentLast(tools string, env []string) []string {
	return toolEnvironment(tools, env, true)
}

func toolEnvironment(tools string, env []string, last bool) []string {
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
	return append(out, "PATH="+joinToolPath(tools, path, last))
}

// LookTool returns the absolute path of name in the tools directory's bin directories, or name
// unchanged when it is a path or is not there. A process started directly resolves its program
// against the PARENT's PATH, not the one handed to the child, so a tool the agent installed would
// not be found by name without this.
func LookTool(tools, name string) string {
	return lookTool(ToolPath(tools, ""), name)
}

// LookToolLast is LookTool for the anchor: the program is looked up on the process' own PATH
// first and only then in the tools directory, so a shim there cannot replace a system tool.
func LookToolLast(tools, name string) string {
	return lookTool(ToolPathLast(tools, os.Getenv("PATH")), name)
}

func lookTool(path, name string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name
	}
	for _, dir := range filepath.SplitList(path) {
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
	for _, dir := range filepath.SplitList(ToolPath(s.toolsDir(), s.commandPath())) {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

// PrivateRoots is a copy of the roots this sandbox was told to keep private (Options.PrivateRoots).
func (s *Sandbox) PrivateRoots() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.op.PrivateRoots...)
}

// scratchMaxAge is how long a file in the scratch directory is kept.
const scratchMaxAge = 24 * time.Hour

// scratchDir is a temporary directory that outlives one command: $TMPDIR is deleted when the command
// ends, and /tmp is not writable, so a file one command writes for the next (a test log, a diff) had
// nowhere to go. It lives in the sandbox's HOME, a write root, and what is older than scratchMaxAge
// is removed each time it is handed out. "" when there is no tools directory or it cannot be made.
func (s *Sandbox) scratchDir() string {
	home := s.Home()
	if home == "" {
		return ""
	}
	dir := filepath.Join(home, "scratch")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return ""
	}
	removeOlder(dir, "", scratchMaxAge)
	return dir
}

package tui

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
)

// shellDir is the folder the commands a person types run in, kept between lines the way a
// terminal keeps it. It is stored relative to the workspace and never leaves it: the typed shell
// works where the session works, and a line that must run elsewhere still says so itself
// (`cd /else && ...`), where the policy judges it.
type shellDir struct {
	mu sync.Mutex
	// rel is the folder relative to the workspace ("" is the workspace itself), prev the one
	// before the last cd, for `cd -`.
	rel, prev string
}

// workspaceRoot is the absolute workspace cfg's commands run in, as the agent's policy reads it.
func workspaceRoot(cfg config.Config) string {
	abs, _ := filepath.Abs(cmp.Or(strings.TrimSpace(cfg.Agent.WorkspaceDir), "."))
	return abs
}

// shellMeta are the characters that make a cd line more than a cd: a list, a pipe, an expansion.
// Such a line runs as written and moves nothing for the next one, as `cd x && make` always has.
const shellMeta = ";&|<>$`*?(){}\n"

// loneCd reports whether line is a cd and nothing else, with its operand ("" for a bare cd).
func loneCd(line string) (string, bool) {
	line = strings.TrimSpace(line)
	name, rest, _ := strings.Cut(line, " ")
	if name != "cd" && !strings.EqualFold(name, "Set-Location") && !strings.EqualFold(name, "chdir") {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	if strings.ContainsAny(rest, shellMeta) {
		return "", false
	}
	if len(rest) >= 2 && (rest[0] == '\'' || rest[0] == '"') && rest[len(rest)-1] == rest[0] {
		return rest[1 : len(rest)-1], true
	}
	if strings.ContainsAny(rest, " \t'\"") {
		// More than one word: a flag (`cd -P x`) or a quoting it is not worth parsing here.
		return "", false
	}
	return rest, true
}

// change moves the folder to target, read from the current one. A bare cd goes back to the
// workspace (the session's "home"), `cd -` to the previous folder. Like a shell's cd it prints
// nothing when it works.
func (d *shellDir) change(ws, target string) (string, int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var abs string
	switch {
	case target == "" || target == "~":
		abs = ws
	case target == "-":
		abs = filepath.Join(ws, d.prev)
	case strings.HasPrefix(target, "~/"):
		home, _ := os.UserHomeDir()
		abs = filepath.Join(home, target[2:])
	case filepath.IsAbs(target):
		abs = filepath.Clean(target)
	default:
		abs = filepath.Join(ws, d.rel, target)
	}
	rel, err := insideWorkspace(ws, abs)
	if err != nil {
		return fmt.Sprintf("cd: %s: %v\n", target, err), 1, err
	}
	d.prev, d.rel = d.rel, rel
	return "", 0, nil
}

// insideWorkspace is abs relative to ws when it is a folder inside it, links resolved: a link
// inside the workspace that points out of it is outside. The path is first checked by its spelling,
// so nothing outside the workspace is even looked at, and then by where its links lead. A name with
// ".." anywhere in it is refused with the climbs: telling `a..b` from `../b` is not worth the doubt.
func insideWorkspace(ws, abs string) (string, error) {
	spelled, err := filepath.Rel(ws, filepath.Clean(abs))
	if err != nil || strings.Contains(spelled, "..") {
		return "", outsideWorkspace(ws)
	}
	abs = filepath.Join(ws, spelled)
	info, err := os.Stat(abs)
	if err != nil {
		return "", errors.New("no such folder")
	}
	if !info.IsDir() {
		return "", errors.New("not a folder")
	}
	realWs, errWs := filepath.EvalSymlinks(ws)
	realAbs, errAbs := filepath.EvalSymlinks(abs)
	rel, err := filepath.Rel(realWs, realAbs)
	if errors.Join(errWs, errAbs, err) != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", outsideWorkspace(ws)
	}
	if rel == "." {
		rel = ""
	}
	return rel, nil
}

// outsideWorkspace is why a cd out of the workspace did not move.
func outsideWorkspace(ws string) error {
	return fmt.Errorf("outside the session's folder (%s): typed commands stay in it", ws)
}

// errFolderGone is why a line did not run: the folder it would have run in was deleted.
var errFolderGone = errors.New("folder gone")

// prefix is command made to run in the current folder, and that folder. In the workspace it is
// the line untouched; below it, the line starts with a cd there, written as the agent writes its
// own (`cd sub && ...`), so the policy reads the same line a person would and paths stay judged
// against the workspace, which from a deeper folder is the stricter reading.
//
// A folder deleted since the cd is not swapped for the workspace in silence: the line was meant
// for another place, so nothing runs and the folder goes back to the workspace.
func (d *shellDir) prefix(ws, shell, command string) (string, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rel == "" {
		return command, ws, nil
	}
	dir := filepath.Join(ws, d.rel)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		gone := d.rel
		d.rel, d.prev = "", ""
		return "", ws, fmt.Errorf("%w: %s no longer exists, so nothing ran; back in %s", errFolderGone, gone, ws)
	}
	return cdLine(shell, d.rel, command), dir, nil
}

// cdLine is command run in rel, for shell.
func cdLine(shell, rel, command string) string {
	switch {
	case execx.IsPowerShell(shell):
		// Windows PowerShell has no &&; Set-Location stops the line when the folder is missing
		// only with -ErrorAction Stop, which cd also takes.
		return "cd " + execx.Quote(shell, rel) + " -ErrorAction Stop; " + command
	case execx.IsCmd(shell):
		// pushd and not `cd /d`: it changes the drive too, and its only operand is the folder.
		return `pushd "` + rel + `" && ` + command
	default:
		return "cd " + execx.Quote(shell, rel) + " && " + command
	}
}

// reset puts the folder back at the workspace.
func (d *shellDir) reset() {
	d.mu.Lock()
	d.rel, d.prev = "", ""
	d.mu.Unlock()
}

// repoHint is what a failed line gets when git said the folder is not a repository and there are
// repositories right below it - a project's folder holds its repositories one level down, so git
// typed at its top fails the way the report showed.
func repoHint(dir, out string) string {
	if !strings.Contains(out, "not a git repository") {
		return ""
	}
	repos := reposBelow(dir)
	if len(repos) == 0 {
		return ""
	}
	return fmt.Sprintf("[this folder is not a git repository; the repositories are in: %s - run `cd %s` first]\n",
		strings.Join(repos, ", "), strings.TrimSuffix(repos[0], "/"))
}

// reposBelow are the folders right inside dir that are git repositories or worktrees (a .git
// folder or file), named with a trailing slash.
func reposBelow(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var repos []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), ".git")); err == nil {
			repos = append(repos, e.Name()+"/")
		}
	}
	sort.Strings(repos)
	return repos
}

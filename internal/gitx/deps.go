package gitx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The dependency directories a fresh checkout needs in order to RUN. It lives here, beside
// AddWorktree, because two callers make checkouts that have to run a gate: the gateway's
// session worktrees and the anchor's baseline (a clean checkout of the commit a run started
// from, used to tell a failure the run caused from one that was already there).

// LinkDependencyDirs points a fresh checkout at the dependency directories the project
// already has, so its gate can run before anything is installed.
//
// Only directories that are BOTH ignored by git and already present in the project are
// linked, and each candidate is a well-known dependency folder. That list is deliberately
// short: an ignored directory can hold anything (build output, a local database, the
// user's own scratch), and linking one the toolchain does not read would hand the session
// a directory it may write to that is not its own.
//
// A failure is silent by design: the session still works, it simply has to install its own
// dependencies, which is exactly what happened before this existed. A link that cannot be
// made must not stop a session from being created.
//
// The directories are looked for at EVERY level of the checkout, not only at its root, down to
// dependencyDirsMaxDepth. A monorepo keeps its dependencies next to each package - `web/node_modules`,
// `packages/api/node_modules`, `services/ml/.venv` - and linking the root alone left exactly those
// checks with no toolchain: this repository's own web UI is one. The walk follows the CHECKOUT's
// directories, which are the tracked ones, so it never descends into something git ignores, and
// it never enters a dependency directory or a link it made.
//
// git reads a link as a FILE, so a directory rule (`node_modules/`) does not hide it, and `add -A`
// would commit a link to a host path. Each link is therefore ignored by its own root-anchored path.
func LinkDependencyDirs(projectDir, worktree string) {
	var links []string
	visited := 0
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		visited++
		links = append(links, linkDependencyDirsAt(projectDir, worktree, rel)...)
		if depth >= dependencyDirsMaxDepth {
			return
		}
		entries, err := os.ReadDir(filepath.Join(worktree, rel))
		if err != nil {
			return
		}
		for _, e := range entries {
			// A bound on the walk itself: a checkout with tens of thousands of tracked
			// directories must not make creating a session slow.
			if visited >= dependencyDirsMaxVisits {
				return
			}
			if !isWalkableDir(e) {
				continue
			}
			walk(filepath.Join(rel, e.Name()), depth+1)
		}
	}
	walk("", 0)
	_ = ignoreLinks(context.Background(), worktree, links)
}

// linkDependencyDirsAt links the dependency directories of projectDir/rel into worktree/rel and
// returns their root-anchored paths. Nothing to share, or something is already there: both are
// "leave it alone". A path git would read as a pattern is left alone too: its link could not be
// ignored by name. A link that cannot be made is listed anyway, since its path is a dependency
// folder either way - see LinkDependencyDirs.
func linkDependencyDirsAt(projectDir, worktree, rel string) []string {
	var links []string
	for _, name := range dependencyDirs {
		src := filepath.Join(projectDir, rel, name)
		dst := filepath.Join(worktree, rel, name)
		path := "/" + filepath.ToSlash(filepath.Join(rel, name))
		if strings.ContainsAny(path, "*?[]!#\\\n\r") || !isReadableDir(src) || pathExists(dst) {
			continue
		}
		_ = os.Symlink(src, dst)
		links = append(links, path)
	}
	return links
}

// isWalkableDir reports whether the walk enters e: a real directory that is neither .git nor a
// dependency directory. DirEntry reports a symlink as a symlink, so a link made by the walk is
// never entered.
func isWalkableDir(e os.DirEntry) bool {
	return e.IsDir() && e.Name() != ".git" && !isDependencyDir(e.Name())
}

// dependencyDirsMaxDepth is how deep below the checkout's root dependency directories are looked
// for. Four levels covers `packages/<group>/<name>/node_modules`, the deepest common layout.
const dependencyDirsMaxDepth = 4

// dependencyDirsMaxVisits bounds how many directories the walk looks at. A variable so a test can
// reach the bound without building a checkout of that size.
var dependencyDirsMaxVisits = 5000

// isDependencyDir reports whether name is one of dependencyDirs.
func isDependencyDir(name string) bool {
	for _, d := range dependencyDirs {
		if d == name {
			return true
		}
	}
	return false
}

// dependencyDirs are the ignored directories a checkout needs in order to RUN its
// own gate. Each is a dependency tree a package manager writes and reads, never project
// source: linking one shares the bytes instead of copying them.
var dependencyDirs = []string{
	"node_modules", // npm, pnpm, yarn
	".venv",        // python
	"venv",         // python, the other spelling
	"vendor",       // go, php
	"target",       // rust, and java's build output
	".bundle",      // ruby
	"Pods",         // cocoa
}

// isReadableDir reports whether path is a directory that can be read.
func isReadableDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// pathExists reports whether anything at all is at path, symlink included.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// MissingDependency is a dependency folder a project needs and does not have: the manifest that
// asks for it is there, the folder a package manager would have written is not.
type MissingDependency struct {
	// Path is the folder that holds the manifest, relative to the project: "." for its root.
	Path string `json:"path"`
	// Manager is the package manager that writes the folder: composer or npm.
	Manager string `json:"manager"`
	// Dir is the folder that is missing, by name: vendor or node_modules.
	Dir string `json:"dir"`
	// Command installs it, as it is run from Path.
	Command string `json:"command"`
}

// manifests are the files that say a folder needs its dependencies installed, with the keys that
// list them and what installs them. Only package managers whose folder lives next to the manifest
// are here: a language that keeps its dependencies elsewhere (go's module cache) has nothing to
// check.
var manifests = []struct {
	file, manager, dir, command string
	keys                        []string
}{
	{"composer.json", "composer", "vendor", "composer install", []string{"require", "require-dev"}},
	{"package.json", "npm", "node_modules", "npm install", []string{"dependencies", "devDependencies"}},
}

// MissingDependencies lists the dependency folders dir needs and does not have, at its root and one
// folder down (a monorepo's packages). A manifest that lists nothing needs no folder, and neither
// does a folder that is there empty: the package manager ran. It reads, and never writes.
//
// It is how a session that was handed a checkout of a repository nobody installed is told so in
// plain words, instead of finding out from a test run that cannot start.
func MissingDependencies(dir string) []MissingDependency {
	out := missingAt(dir, ".")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if isWalkableDir(e) && e.Name()[0] != '.' {
			out = append(out, missingAt(filepath.Join(dir, e.Name()), e.Name())...)
		}
	}
	return out
}

// missingAt is what the manifests in dir ask for and dir lacks; rel names dir in the answer.
func missingAt(dir, rel string) []MissingDependency {
	var out []MissingDependency
	for _, m := range manifests {
		if pathExists(filepath.Join(dir, m.dir)) || !listsDependencies(filepath.Join(dir, m.file), m.keys) {
			continue
		}
		out = append(out, MissingDependency{Path: rel, Manager: m.manager, Dir: m.dir, Command: m.command})
	}
	return out
}

// listsDependencies reports whether the JSON manifest at path has a non-empty object under any of
// keys. A manifest that is missing or is not JSON lists none.
func listsDependencies(path string, keys []string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	for _, k := range keys {
		var deps map[string]json.RawMessage
		if json.Unmarshal(doc[k], &deps) == nil && len(deps) > 0 {
			return true
		}
	}
	return false
}

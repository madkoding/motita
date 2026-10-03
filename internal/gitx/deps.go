package gitx

import (
	"os"
	"path/filepath"
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
func LinkDependencyDirs(projectDir, worktree string) {
	visited := 0
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		visited++
		for _, name := range dependencyDirs {
			src := filepath.Join(projectDir, rel, name)
			dst := filepath.Join(worktree, rel, name)
			// Nothing to share, or something is already there: both are "leave it alone". A
			// link that cannot be made is left alone too - see above.
			if !isReadableDir(src) || pathExists(dst) {
				continue
			}
			_ = os.Symlink(src, dst)
		}
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
			// DirEntry reports a symlink as a symlink, so a link made above is never entered.
			if !e.IsDir() || e.Name() == ".git" || isDependencyDir(e.Name()) {
				continue
			}
			walk(filepath.Join(rel, e.Name()), depth+1)
		}
	}
	walk("", 0)
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

package gitx

import (
	"os"
	"path/filepath"
)

// resolve is the canonical form of a path for comparison: absolute, and with
// symlinks followed when that is possible.
//
// Both steps DEGRADE rather than fail. A path that cannot be made absolute
// (there is no working directory to be relative to) or whose symlinks cannot be
// resolved (it does not exist yet) is compared as it was written: refusing to
// compare two paths is a worse answer than comparing them literally, because
// samePath's only caller is a guard that would then reject a worktree that is
// perfectly valid.
//
// Symlinks are resolved because a home directory or /tmp is commonly reached
// through one: a comparison on the written path would call a project and its
// own repository root two different places.
func resolve(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// samePath reports whether two paths name the same directory.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return resolve(a) == resolve(b)
}

// prepareParent creates the directory a worktree will live in.
//
// `git worktree add` creates the final directory but NOT its parents, and its
// failure for a missing parent reads like a git problem rather than the missing
// folder it is.
func prepareParent(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

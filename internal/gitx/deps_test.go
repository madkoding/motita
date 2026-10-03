package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNestedDependencyDirectoriesAreShared: a monorepo keeps its dependencies next to each
// package. Only the root used to be linked, so `web/node_modules` - this repository's own
// layout - was missing from every session, and so was the web gate's toolchain.
func TestNestedDependencyDirectoriesAreShared(t *testing.T) {
	project, wt := t.TempDir(), t.TempDir()
	for _, dir := range []string{"web", "packages/api", "a/b/c/d/e", ".git/hooks"} {
		if err := os.MkdirAll(filepath.Join(wt, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dep := range []string{"web/node_modules", "packages/api/.venv", "a/b/c/d/e/node_modules",
		"node_modules/inner/node_modules", ".git/hooks/node_modules"} {
		if err := os.MkdirAll(filepath.Join(project, dep), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	LinkDependencyDirs(project, wt)

	for _, want := range []string{"node_modules", "web/node_modules", "packages/api/.venv"} {
		if info, err := os.Lstat(filepath.Join(wt, want)); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s must be linked into the checkout: %v", want, err)
		}
	}
	// Past the depth bound, inside .git, and inside a linked dependency: never.
	for _, not := range []string{"a/b/c/d/e/node_modules", ".git/hooks/node_modules"} {
		if _, err := os.Lstat(filepath.Join(wt, not)); err == nil {
			t.Errorf("%s must not be linked", not)
		}
	}
}

// TestARealDependencyDirectoryInTheCheckoutIsNotWalked: a checkout that installed its own
// node_modules keeps it, and the walk never descends into it looking for more to link.
func TestARealDependencyDirectoryInTheCheckoutIsNotWalked(t *testing.T) {
	project, wt := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "node_modules", "pkg", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	LinkDependencyDirs(project, wt)
	if _, err := os.Lstat(filepath.Join(wt, "node_modules", "pkg", "node_modules")); err == nil {
		t.Error("the walk must not enter a dependency directory")
	}
}

// TestTheDependencyWalkIsBounded: a huge checkout stops being walked at the visit bound, and a
// checkout that cannot be read is left alone.
func TestTheDependencyWalkIsBounded(t *testing.T) {
	project, wt := t.TempDir(), t.TempDir()
	for _, dir := range []string{"one", "two"} {
		os.MkdirAll(filepath.Join(wt, dir), 0o755)
		os.MkdirAll(filepath.Join(project, dir, "node_modules"), 0o755)
	}
	old := dependencyDirsMaxVisits
	dependencyDirsMaxVisits = 2
	defer func() { dependencyDirsMaxVisits = old }()
	LinkDependencyDirs(project, wt)
	if _, err := os.Lstat(filepath.Join(wt, "one", "node_modules")); err != nil {
		t.Errorf("the first directory is within the bound: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wt, "two", "node_modules")); err == nil {
		t.Error("the walk must stop at the bound")
	}
	// A checkout that does not exist: nothing to walk, and no panic.
	LinkDependencyDirs(project, filepath.Join(wt, "missing"))
}

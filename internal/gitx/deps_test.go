package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDependencyLinksAreNeitherChangesNorCommitted: git reads a link as a file, so the directory
// rule `node_modules/` does not hide the link LinkDependencyDirs makes. A session with no work
// reported one change, and the background agent's CommitAll committed a link to a host path.
func TestDependencyLinksAreNeitherChangesNorCommitted(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "package.json"), "{}\n")
	write(t, filepath.Join(repo, ".gitignore"), "node_modules/\n")
	write(t, filepath.Join(repo, "web", "a.txt"), "a\n")
	write(t, filepath.Join(repo, "a[b", "a.txt"), "a\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "project")
	for _, dep := range []string{"node_modules/x", "web/node_modules/x", "a[b/node_modules/x"} {
		write(t, filepath.Join(repo, dep), "dep\n")
	}
	wt := filepath.Join(filepath.Dir(repo), "worktrees", "s1")
	addWorktree(t, repo, wt, "motita/s1")
	LinkDependencyDirs(repo, wt)

	if _, err := os.Lstat(filepath.Join(wt, "web", "node_modules")); err != nil {
		t.Fatalf("the dependencies must be linked: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wt, "a[b", "node_modules")); err == nil {
		t.Error("a path git reads as a pattern cannot be ignored, so it must not be linked")
	}
	if n, err := WorkingTreeChanges(ctx, wt); err != nil || n != 0 {
		t.Errorf("a session with no work reports %d changes (%v)", n, err)
	}
	if committed, err := CommitAll(ctx, wt, "work"); err != nil || committed {
		t.Errorf("CommitAll committed the dependency links: %v %v", committed, err)
	}
	// The block only grows: a link another worktree still has stays ignored.
	if err := ignoreLinks(ctx, wt, []string{"/other/node_modules"}); err != nil {
		t.Fatal(err)
	}
	got := excludeOf(t, repo)
	for _, want := range []string{"/node_modules\n", "/web/node_modules\n", "/other/node_modules\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the exclude file lacks %q:\n%s", want, got)
		}
	}
	// Re-running the base keeps the links block.
	if err := EnsureIgnoreBase(ctx, repo); err != nil || excludeOf(t, repo) != got {
		t.Errorf("the ignore base changed the links block (%v):\n%s", err, excludeOf(t, repo))
	}
}

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

func TestMissingDependenciesNamesWhatAManifestAsksForAndTheFolderLacks(t *testing.T) {
	dir := t.TempDir()
	put := func(rel, body string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("composer.json", `{"require-dev":{"phpunit/phpunit":"^10"}}`)
	put("package.json", `{"name":"x","dependencies":{}}`)        // lists nothing: needs nothing
	put("web/package.json", `{"dependencies":{"react":"^18"}}`)  // one folder down
	put("api/package.json", `{"dependencies":{"express":"^4"}}`) // installed
	put("api/node_modules/express/index.js", "")
	put(".hidden/package.json", `{"dependencies":{"a":"1"}}`) // hidden folders are not packages
	put("broken/package.json", `{not json`)
	put("deep/inner/package.json", `{"dependencies":{"a":"1"}}`) // two folders down: not looked at

	got := map[string]MissingDependency{}
	for _, m := range MissingDependencies(dir) {
		got[m.Path+"/"+m.Dir] = m
	}
	want := map[string]string{"./vendor": "composer install", "web/node_modules": "npm install"}
	if len(got) != len(want) {
		t.Fatalf("missing: %+v", got)
	}
	for key, cmd := range want {
		if got[key].Command != cmd {
			t.Errorf("%s: %+v, want %q", key, got[key], cmd)
		}
	}
	// Installed, nothing is missing; a folder that cannot be read lists nothing.
	put("vendor/autoload.php", "")
	if m := MissingDependencies(dir); len(m) != 1 || m[0].Manager != "npm" {
		t.Errorf("after installing vendor/: %+v", m)
	}
	if m := MissingDependencies(filepath.Join(dir, "nope")); len(m) != 0 {
		t.Errorf("no folder, nothing missing: %+v", m)
	}
}

func TestIsRepoRootIsTheTopOfAWorkingTreeOnly(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(t.TempDir(), "bare.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if !IsRepoRoot(ctx, repo) {
		t.Error("the top of a repository is its root")
	}
	for _, not := range []string{sub, bare, t.TempDir(), filepath.Join(repo, "missing")} {
		if IsRepoRoot(ctx, not) {
			t.Errorf("%s is not the root of a working tree", not)
		}
	}
}

package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// The block EnsureIgnoreBase owns inside .git/info/exclude. Everything between the two lines is
// rewritten on every call; everything outside is the user's and is never touched.
const (
	ignoreBlockBegin = "# >>> motita ignore base (managed, do not edit) >>>"
	ignoreBlockEnd   = "# <<< motita ignore base <<<"
)

// ignoreBase is what a project of each ecosystem never wants to see as a change: dependency
// folders, caches and build output. A marker is a file at the ROOT of the project; the first
// column decides which set applies, and several can apply to one project (a Go service with a
// web UI has both a go.mod and a package.json).
//
// Deliberately NOT here: `vendor/`, `build/` and `dist/`. Those are tracked in plenty of
// projects, and an ignore rule for a path a project commits would hide a new file in it.
var ignoreBase = []struct {
	markers  []string
	patterns []string
}{
	{[]string{"go.mod", "go.work"}, []string{"*.test", "*.out", "coverage.out"}},
	{[]string{"package.json"}, []string{"node_modules/", ".next/", ".nuxt/", ".turbo/", ".parcel-cache/", ".vite/", "*.tsbuildinfo", "npm-debug.log*", "yarn-error.log*"}},
	{[]string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile"}, []string{"__pycache__/", "*.py[cod]", ".venv/", "venv/", ".pytest_cache/", ".mypy_cache/", ".ruff_cache/", ".tox/", "*.egg-info/"}},
	{[]string{"Cargo.toml"}, []string{"target/"}},
	{[]string{"pom.xml"}, []string{"target/", "*.class"}},
	{[]string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}, []string{".gradle/", "*.class"}},
	{[]string{"Gemfile"}, []string{".bundle/"}},
	{[]string{"composer.json"}, []string{"/vendor/"}},
	{[]string{"Podfile"}, []string{"Pods/"}},
	{[]string{"pubspec.yaml"}, []string{".dart_tool/"}},
	{[]string{"mix.exs"}, []string{"_build/", "deps/"}},
}

// ignoreEverywhere is the editor and OS litter that belongs in no project.
var ignoreEverywhere = []string{".DS_Store", "Thumbs.db", "*.swp", "*~"}

// EnsureIgnoreBase makes the project's ignore rules hold in EVERY worktree of it.
//
// `git worktree add` gives a worktree the .gitignore that is COMMITTED on its branch. Whatever the
// project keeps only in its own checkout - a .gitignore not committed yet, or edited since - does
// not travel, and a project with no complete .gitignore has nothing for a language's dependency
// folders and caches. The session then reports `node_modules/` or `__pycache__/` as changes it
// made, and its worktree looks dirty for files nobody wrote.
//
// The rules go to `.git/info/exclude` of the COMMON git directory, which every worktree of the
// repository reads, so one write covers all of them and the user's tracked files are untouched.
// It holds: the base for each ecosystem whose marker file is at the project's root, plus the root
// .gitignore when it has changes the worktrees would not see. Rules there rank below a
// .gitignore, so a project's own negations still win.
//
// It is idempotent, and a failure is returned for the caller to treat as advisory: a worktree
// with noisy status is still a worktree.
func EnsureIgnoreBase(ctx context.Context, repoDir string) error {
	common, err := noGitOr(ctx, "could not find the repository's git directory", repoDir, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoDir, common)
	}
	path := filepath.Join(common, "info", "exclude")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	block := ignoreBlock(repoDir, uncommittedGitignore(ctx, repoDir))
	updated := replaceIgnoreBlock(string(existing), block)
	if updated == string(existing) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

// uncommittedGitignore is the root .gitignore's content when it is untracked or differs from the
// commit - which is exactly when a fresh worktree would not have it - and "" otherwise.
func uncommittedGitignore(ctx context.Context, repoDir string) string {
	out, err := executeRaw(ctx, repoDir, "status", "--porcelain", "--", ".gitignore")
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(repoDir, ".gitignore"))
	if err != nil {
		return ""
	}
	return string(body)
}

// ignoreBlock renders the managed block for the project at repoDir, without duplicate lines.
func ignoreBlock(repoDir, gitignore string) string {
	seen := map[string]bool{}
	var lines []string
	add := func(l string) {
		l = strings.TrimRight(l, " \t\r")
		if l == "" || seen[l] {
			return
		}
		seen[l] = true
		lines = append(lines, l)
	}
	for _, eco := range ignoreBase {
		for _, m := range eco.markers {
			if _, err := os.Stat(filepath.Join(repoDir, m)); err == nil {
				for _, p := range eco.patterns {
					add(p)
				}
				break
			}
		}
	}
	for _, p := range ignoreEverywhere {
		add(p)
	}
	for _, l := range strings.Split(gitignore, "\n") {
		add(l)
	}
	return ignoreBlockBegin + "\n" + strings.Join(lines, "\n") + "\n" + ignoreBlockEnd + "\n"
}

// replaceIgnoreBlock puts block in text: over the managed block when there is one, at the end
// otherwise. A begin marker with no end marker is a torn write and is replaced to the end of the
// text, so the file cannot accumulate blocks.
func replaceIgnoreBlock(text, block string) string {
	start := strings.Index(text, ignoreBlockBegin)
	if start < 0 {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text + block
	}
	end := len(text)
	if i := strings.Index(text[start:], ignoreBlockEnd); i >= 0 {
		end = start + i + len(ignoreBlockEnd)
		if end < len(text) && text[end] == '\n' {
			end++
		}
	}
	return text[:start] + block + text[end:]
}

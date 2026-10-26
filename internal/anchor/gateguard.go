package anchor

// What the gate fingerprint reads beyond the gate's own files, and the test floor.
//
// A gate is not only the line that names it. `make check` includes other makefiles, `npm test`
// runs scripts/test.js, pytest reads conftest.py and pyproject.toml: rewriting any of them
// weakens the gate while the Makefile and package.json stay byte for byte the same. And the
// cheapest way to a green run is to delete the tests: `go test ./...` over a tree with no test
// files prints "no test files" and exits 0. This file covers both, read-only like the rest of
// detection.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// maxGateFile is the largest file read as part of the gate or counted for tests: a generated
// bundle is not a test, and the guard must stay cheap enough to run on every validation.
const maxGateFile = 1 << 20

// skippedDirs are not walked: they hold dependencies and tool state, not the project's tests.
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"target": true, "__pycache__": true, ".motita": true, ".tox": true,
}

// pythonConfigFiles are the files pytest (and the Python tooling around it) reads to decide what
// runs: a rewrite of any of them changes the gate.
var pythonConfigFiles = []string{"pyproject.toml", "setup.py", "setup.cfg", "pytest.ini", "tox.ini", "conftest.py"}

// makeInclude matches a makefile line that pulls another file in.
var makeInclude = regexp.MustCompile(`(?m)^[ \t]*-?(?:include|sinclude)[ \t]+(.+)$`)

// testMarker matches what makes a line a test: a Go, Python, Rust, or JS/TS test declaration.
var testMarker = regexp.MustCompile(`(?m)^\s*(?:` +
	`func Test\w|(?:async )?def test_?\w|#\[test\]|#\[tokio::test\]|(?:it|test)(?:\.\w+)?\()`)

// scriptExt are the extensions of a file a gate RUNS. Only those are followed from a gate's text:
// a source file that merely appears in a build command (`go build main.go`) is the agent's
// ordinary work and must not read as the gate changing.
var scriptExt = map[string]bool{
	".sh": true, ".bash": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".py": true,
	".rb": true, ".pl": true, ".ps1": true, ".bat": true, ".cmd": true, ".mk": true,
}

// isTestFile says whether a file name is a test file by the conventions of the toolchains the
// anchor detects.
func isTestFile(name string) bool {
	if strings.HasSuffix(name, "_test.go") {
		return true
	}
	if strings.HasSuffix(name, ".py") {
		return strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py")
	}
	for _, ext := range []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"} {
		if strings.HasSuffix(name, ".test"+ext) || strings.HasSuffix(name, ".spec"+ext) {
			return true
		}
	}
	return false
}

// TestCount is how many tests the tree declares: lines that start one, in files named like tests,
// plus Rust's inline `#[test]` modules in any source file. It is a floor, not a gate - the caller
// compares it with the count the task started with, and a drop means the run deleted or emptied
// tests. Adding tests raises it and is never a finding.
func (a *Anchor) TestCount() int {
	n := 0
	_ = filepath.WalkDir(a.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != a.dir && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isTestFile(d.Name()) && !strings.HasSuffix(d.Name(), ".rs") {
			return nil
		}
		if body, ok := readLimited(p); ok {
			n += len(testMarker.FindAllStringIndex(body, -1))
		}
		return nil
	})
	return n
}

// readLimited reads a file up to maxGateFile bytes.
func readLimited(path string) (string, bool) {
	f, err := os.Open(path) // #nosec G304 -- path is inside the anchor's directory, read-only.
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxGateFile))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// referencedFiles are the project files the gate's text names: every makefile it includes
// (recursively, bounded), and every token of a gate text that is a file inside the project - the
// `scripts/test.js` of `node scripts/test.js`. The result is sorted and without duplicates so the
// fingerprint does not depend on the order the text mentions them in.
func (a *Anchor) referencedFiles(texts []string) []string {
	seen := map[string]bool{}
	var queue []string
	add := func(rel string) {
		rel = filepath.Clean(strings.TrimPrefix(rel, "./"))
		if seen[rel] || !a.insideProject(rel) {
			return
		}
		seen[rel] = true
		queue = append(queue, rel)
	}
	for _, t := range texts {
		scanReferences(t, add)
	}
	// The files found are scanned too, but only for includes and the scripts they call: a bounded
	// number of rounds keeps a cycle or a huge tree from running away.
	for i := 0; i < len(queue) && i < 64; i++ {
		if body, ok := readLimited(filepath.Join(a.dir, queue[i])); ok {
			scanReferences(body, add)
		}
	}
	files := make([]string, 0, len(seen))
	for f := range seen {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// scanReferences calls add for every makefile text includes and every token that names a script.
func scanReferences(text string, add func(string)) {
	for _, m := range makeInclude.FindAllStringSubmatch(text, -1) {
		for _, f := range strings.Fields(m[1]) {
			add(f)
		}
	}
	for _, tok := range strings.Fields(text) {
		tok = strings.Trim(tok, `"'();&|<>`)
		if scriptExt[strings.ToLower(filepath.Ext(tok))] {
			add(tok)
		}
	}
}

// insideProject says whether rel is a regular file inside the anchor's directory.
func (a *Anchor) insideProject(rel string) bool {
	if rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	info, err := os.Stat(filepath.Join(a.dir, rel))
	return err == nil && info.Mode().IsRegular()
}

// writeExtraFingerprint adds to h the gate's referenced files, the Python configuration and every
// conftest.py in the tree.
func (a *Anchor) writeExtraFingerprint(h io.Writer, texts []string) {
	files := a.referencedFiles(texts)
	files = append(files, pythonConfigFiles...)
	_ = filepath.WalkDir(a.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != a.dir && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "conftest.py" {
			// p is under a.dir by construction, so Rel cannot fail.
			rel, _ := filepath.Rel(a.dir, p)
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	prev := ""
	for _, name := range files {
		if name == prev {
			continue
		}
		prev = name
		body, ok := readIfPresent(a.dir, name)
		fmt.Fprintf(h, "ref %s %t %d\n%s\n", name, ok, len(body), body)
	}
}

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// THE STATE OF THE WORKING TREE, as one comparable string.
//
// Reported from a real session: the agent read files for thirteen rounds, wrote nothing, then
// said "done" - and the validation PASSED, because a project's own lint, typecheck and tests
// are green on a tree nobody touched. The verdict said "3 checks passed" about a request that
// asked for a new feature, and the run closed as complete with zero files changed.
//
// Two decisions in the loop need to know whether the tree really changed, and both used to
// GUESS from the text of a command: whether a kept read is still valid, and whether a claim of
// "done" is credible. A fingerprint answers both by looking at the files instead of at the
// words that were meant to change them.

// fingerprintMaxFiles bounds the walk. A tree bigger than this is not fingerprinted (the
// answer is "unknown"), which every caller treats conservatively: a kept read is dropped
// and a claim of "done" is not second-guessed. Never a wrong answer, only a slower one. It is a
// variable so a test can lower it instead of building sixty thousand files.
var fingerprintMaxFiles = 60000

// Two questions are asked of the tree, and they need different answers.
//
//   - "Can a file I READ have changed?" (the kept reads) - every file counts, including the
//     lock files and the build output, because a model may well `cat dist/app.js` or read
//     package-lock.json after installing. Only what nobody reads is left out: dependencies, VCS
//     metadata, tool caches.
//   - "Did the WORK change anything?" (a claim of done) - the output of the toolchain does not
//     count. `npm install` rewrites the lock file and a build rewrites dist/, and neither is the
//     user's request. Adding a dependency still shows: the manifest is a source file.

// alwaysSkipDirs holds content nobody reads and every tool rewrites.
var alwaysSkipDirs = map[string]bool{
	".git": true, ".motita": true, "node_modules": true, ".next": true, ".nuxt": true,
	".svelte-kit": true, ".turbo": true, ".cache": true, ".parcel-cache": true,
	"__pycache__": true, ".venv": true, "venv": true, ".mypy_cache": true,
	".pytest_cache": true, ".tox": true, ".gradle": true, ".idea": true, ".dart_tool": true,
	".terraform": true,
}

// outputDirs are where a build or a test run writes. They count for the reads, not for the work.
var outputDirs = map[string]bool{
	"dist": true, "build": true, "out": true, "target": true, "coverage": true, "vendor": true,
}

// lockFiles are rewritten by an install as a side effect. They count for the reads, not for the work.
var lockFiles = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "bun.lockb": true,
	"go.sum": true, "Cargo.lock": true, "poetry.lock": true, "composer.lock": true,
	"Gemfile.lock": true, "Pipfile.lock": true, "uv.lock": true, ".DS_Store": true,
	"tsconfig.tsbuildinfo": true,
}

// treeFingerprint returns a digest of every file the work could have written under dir, and
// whether it could be computed at all. It reads names, sizes and modification times, never
// content: cheap enough to take every round on a large project. sourcesOnly leaves out what a
// build or an install writes on its own (see above).
func treeFingerprint(dir string, sourcesOnly bool) (string, bool) {
	all, sources, ok := treeFingerprints(dir)
	if sourcesOnly {
		return sources, ok
	}
	return all, ok
}

// treeFingerprints takes both fingerprints of dir in ONE walk: every file (the kept reads' answer)
// and the sources only (the work's). A round used to walk the tree once for each, and the walk is
// what a fingerprint costs.
func treeFingerprints(dir string) (all, sources string, ok bool) {
	if strings.TrimSpace(dir) == "" {
		return "", "", false
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", "", false
	}
	w := &treeWalk{dir: dir}
	err := filepath.WalkDir(dir, w.visit)
	if err != nil || w.tooMany {
		return "", "", false
	}
	return digest(w.lines), digest(w.sources), true
}

// digest is the fingerprint of a walk's lines.
func digest(lines []string) string {
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// treeWalk is one walk of treeFingerprints: a line per file it counts, the lines of the files that
// are sources, the output directories it went into, and whether there were too many to count.
type treeWalk struct {
	dir     string
	lines   []string
	sources []string
	outputs []string
	tooMany bool
}

// visit is the WalkDir callback of the walk.
func (w *treeWalk) visit(p string, d fs.DirEntry, err error) error {
	if err != nil {
		// A file that vanished mid-walk is not a failure of the walk.
		return nil
	}
	name := d.Name()
	if d.IsDir() {
		return w.enterDir(p, name)
	}
	// A file that cannot be stat'ed (it vanished between the listing and now) simply is not
	// part of the fingerprint.
	if info, ierr := d.Info(); ierr == nil {
		rel, _ := filepath.Rel(w.dir, p)
		line := rel + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" +
			strconv.FormatInt(info.ModTime().UnixNano(), 10)
		w.lines = append(w.lines, line)
		if !lockFiles[name] && !w.inOutput(p) {
			w.sources = append(w.sources, line)
		}
	}
	return w.limit()
}

// enterDir says whether the walk goes into a directory, and notes the output directories.
func (w *treeWalk) enterDir(p, name string) error {
	if p == w.dir {
		return nil
	}
	if alwaysSkipDirs[name] {
		return fs.SkipDir
	}
	if outputDirs[name] {
		w.outputs = append(w.outputs, p+string(filepath.Separator))
	}
	return nil
}

// inOutput says a file is inside an output directory, whose files are not sources.
func (w *treeWalk) inOutput(p string) bool {
	for _, o := range w.outputs {
		if strings.HasPrefix(p, o) {
			return true
		}
	}
	return false
}

// limit stops the walk once it has counted more files than a fingerprint takes.
func (w *treeWalk) limit() error {
	if len(w.lines) > fingerprintMaxFiles {
		w.tooMany = true
		return fs.SkipAll
	}
	return nil
}

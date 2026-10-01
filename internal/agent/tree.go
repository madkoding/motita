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
	if strings.TrimSpace(dir) == "" {
		return "", false
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", false
	}
	var lines []string
	tooMany := false
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A file that vanished mid-walk is not a failure of the walk.
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && (alwaysSkipDirs[name] || (sourcesOnly && outputDirs[name])) {
				return fs.SkipDir
			}
			return nil
		}
		if sourcesOnly && lockFiles[name] {
			return nil
		}
		// A file that cannot be stat'ed (it vanished between the listing and now) simply is not
		// part of the fingerprint.
		if info, ierr := d.Info(); ierr == nil {
			rel, _ := filepath.Rel(dir, p)
			lines = append(lines, rel+"\x00"+strconv.FormatInt(info.Size(), 10)+"\x00"+strconv.FormatInt(info.ModTime().UnixNano(), 10))
			if len(lines) > fingerprintMaxFiles {
				tooMany = true
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil || tooMany {
		return "", false
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), true
}

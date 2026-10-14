package plan

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// searchNatively makes search_in_files search in Go instead of running grep. Windows has no
// grep, so on Windows the tool that every plan starts with answered "could not run" to every
// search. It is a variable so the Go search is exercised on the platform the tests run on.
var searchNatively = runtime.GOOS == "windows"

// maxSearchFileBytes is the largest file the Go search reads. A larger one is a data file or a
// build product, and reading it whole to find a line would cost more than the answer is worth.
const maxSearchFileBytes = 8 << 20

// searchReadFile and searchWalkDir are the two calls whose failure a test cannot arrange as root,
// which reads every file and walks every directory whatever its mode.
var (
	searchReadFile = os.ReadFile
	searchWalkDir  = filepath.WalkDir
)

// searchFiles searches the text files under path for pattern and answers the way `grep -R -n`
// does: `file:line:text` for a tree, `line:text` for a single file, exit 0 with a match, 1
// without one, and 2 when something could not be read and nothing matched.
//
// pattern is a Go regular expression, which reads the extended syntax grep -E takes, or a
// literal string. Binary files (a NUL in the first 8000 bytes, grep's own test) are skipped,
// and the walk stops once the output is past what the tool returns.
func searchFiles(ctx context.Context, pattern, path string, literal bool) (string, int, error) {
	if literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", 2, fmt.Errorf("invalid pattern: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 2, err
	}

	var out strings.Builder
	matched, failed := false, false
	searchOne := func(file, prefix string) {
		data, ok, err := readSearchable(file)
		if err != nil {
			fmt.Fprintf(&out, "search_in_files: %s: %v\n", file, err)
			failed = true
			return
		}
		if !ok {
			return
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				fmt.Fprintf(&out, "%s%d:%s\n", prefix, i+1, strings.TrimSuffix(line, "\r"))
				matched = true
			}
		}
	}

	if !info.IsDir() {
		searchOne(path, "")
	} else {
		err = searchWalkDir(path, func(file string, d fs.DirEntry, err error) error {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if out.Len() > maxToolOutputBytes {
				return fs.SkipAll
			}
			if err != nil {
				fmt.Fprintf(&out, "search_in_files: %s: %v\n", file, err)
				failed = true
				return nil
			}
			if d.Type().IsRegular() {
				searchOne(file, file+":")
			}
			return nil
		})
		if err != nil {
			return out.String(), 2, err
		}
	}

	switch {
	case matched:
		return out.String(), 0, nil
	case failed:
		return out.String(), 2, nil
	default:
		return out.String(), 1, nil
	}
}

// readSearchable reads a file, and reports ok=false for one the search should not look into:
// too large, or binary.
func readSearchable(file string) ([]byte, bool, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, false, err
	}
	if info.Size() > maxSearchFileBytes {
		return nil, false, nil
	}
	data, err := searchReadFile(file)
	if err != nil {
		return nil, false, err
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return nil, false, nil
	}
	return data, true, nil
}

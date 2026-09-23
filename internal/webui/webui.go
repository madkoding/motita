// Package webui is the interface the gateway serves to a browser.
//
// It is a package of its own, and embedded in the binary rather than read from disk, for three
// reasons that all point the same way: there is no file to lose next to a binary that was copied
// somewhere, no working directory to get wrong, and no path a request could name. The whole
// interface is inside the executable, and the executable is the thing that was tested.
//
// The page is served WITHOUT a token, like a login page: it holds no secret, and it is the only
// way a browser can get one. Everything it talks to is on the same origin, which is why no proxy
// and no CORS are involved anywhere.
package webui

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed assets
var assets embed.FS

// readAsset is the ONE filesystem call in this package, held in a variable so that a test can
// make it fail.
//
// The failure it makes reachable is otherwise UNREACHABLE: go:embed refuses to compile when a
// listed file is missing, so a read of a name that is in the table cannot fail in a correct
// build. That is exactly the shape the coverage gate rejects - a branch nobody has ever run is a
// branch nobody knows the behaviour of - so the call is a seam, and the test drives it.
var readAsset = func(name string) ([]byte, error) { return fs.ReadFile(assets, "assets/"+name) }

// file is one entry of the page.
type file struct {
	name  string // the name inside assets/
	ctype string
}

// files is the ONE list of what the page is made of. The router registers exactly these names,
// the tests walk them, and the size budget measures them - so adding a file to the page cannot
// leave one of those three behind.
var files = map[string]file{
	"/":        {"index.html", "text/html; charset=utf-8"},
	"/app.css": {"app.css", "text/css; charset=utf-8"},
	"/app.js":  {"app.js", "text/javascript; charset=utf-8"},
}

// Names returns the paths this package serves, sorted. Sorted rather than map order because a
// caller that registers them should do it in the same order every run.
func Names() []string {
	out := make([]string, 0, len(files))
	for n := range files {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Content returns one page file and the Content-Type to serve it with.
//
// An unknown name is an error, never a fallback: serving index.html for every unknown path turns
// a typo in a <link> into a page that renders, and nobody notices until it matters.
func Content(name string) ([]byte, string, error) {
	f, ok := files[name]
	if !ok {
		return nil, "", fmt.Errorf("the page has no file %q", name)
	}
	body, err := readAsset(f.name)
	if err != nil {
		// Unreachable in a correct build: go:embed fails the COMPILE if a listed file is
		// missing. Returning the error keeps the failure at the caller instead of panicking in
		// a request handler.
		return nil, "", fmt.Errorf("reading %s: %w", f.name, err)
	}
	return body, f.ctype, nil
}

// Size is how many bytes of page the binary carries. It exists so a test can put a ceiling on
// it: every byte here becomes a byte of the executable, and the size gate has 1.2 MB of margin
// on windows/amd64.
func Size() (int, error) {
	total := 0
	for _, f := range files {
		body, err := readAsset(f.name)
		if err != nil {
			return 0, fmt.Errorf("reading %s: %w", f.name, err)
		}
		total += len(body)
	}
	return total, nil
}

// IsPage reports whether a Content-Type is the page itself, for callers that need to treat the
// document differently from its assets.
func IsPage(ctype string) bool { return strings.HasPrefix(ctype, "text/html") }

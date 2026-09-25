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
	"path"
	"sort"
	"strings"
)

//go:embed assets
var assets embed.FS

// readAsset is the ONE filesystem call for reading a file, held in a variable so that a test can
// make it fail.
//
// The failure it makes reachable is otherwise UNREACHABLE: go:embed refuses to compile when a
// listed file is missing, so a read of a name that is in the table cannot fail in a correct
// build. That is exactly the shape the coverage gate rejects - a branch nobody has ever run is a
// branch nobody knows the behaviour of - so the call is a seam, and the test drives it.
var readAsset = func(name string) ([]byte, error) { return fs.ReadFile(assets, "assets/"+name) }

// discover walks the embedded filesystem and returns the list of served files. Like readAsset, it
// is a seam: the walk cannot fail on an embed.FS in a correct build, so the variable lets a test
// drive the failure branch without a real filesystem error.
var discover = func() ([]file, error) {
	var out []file
	err := walkDir(assets, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == ".gitkeep" {
			return nil
		}
		rel := strings.TrimPrefix(p, "assets/")
		urlPath := "/" + rel
		if rel == "index.html" {
			urlPath = "/"
		}
		out = append(out, file{path: urlPath, name: rel, ctype: contentType(rel)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// walkDir is the one call to fs.WalkDir, held in a variable so a test can make it fail. Like
// readAsset, the failure it makes reachable is UNREACHABLE on an embed.FS in a correct build:
// the walk only errors on a broken embed, which go:embed refuses to produce. The seam is what
// lets the coverage gate see the error path through discover's callback and its return.
var walkDir = fs.WalkDir

// file is one entry of the page.
type file struct {
	path  string // the URL path the browser requests
	name  string // the name inside assets/
	ctype string
}

// contentType returns the Content-Type for an asset based on its extension.
func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".webmanifest":
		return "application/manifest+json"
	case ".svg":
		return "image/svg+xml"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".ttf":
		return "font/ttf"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	}
	return "application/octet-stream"
}

// files is the route table, built once at package load from the embedded filesystem. The list is
// discovered, not hardcoded: adding a file to assets/ makes it served without touching this
// table, and the router and tests pick it up from Names().
var files = mustDiscover()

// mustDiscover builds the route table from the embedded filesystem. It panics on failure because
// a binary whose own assets cannot be listed is not usable; the discover seam is what makes that
// failure reachable in a test.
func mustDiscover() map[string]file {
	entries, err := discover()
	if err != nil {
		panic(fmt.Sprintf("webui: discover: %v", err))
	}
	m := make(map[string]file, len(entries))
	for _, f := range entries {
		m[f.path] = f
	}
	return m
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

package webui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// assetsMissingFromDisk decides whether TestThePageNamesAssetsThatAreActuallyThere checks the
// page or skips it, so a bug in it is silent in both directions: too eager and the real check
// never runs, too lax and a checkout without a build fails the gate. It is pinned here against
// a temporary asset tree, so the test depends neither on the frontend build nor on the tracked
// index.html.
func TestAssetsMissingFromDisk(t *testing.T) {
	dir := t.TempDir()
	// The helper resolves references against ./assets, the same relative path the embed uses,
	// so the test runs from a directory that has one.
	for _, name := range []string{"index-abc123.js", "index-def456.css", "chunks/lazy-789.js"} {
		path := filepath.Join(dir, "assets", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte("/* built */"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Chdir(dir)

	cases := []struct {
		name string
		html string
		want []string
	}{
		{
			name: "every reference is on disk",
			html: `<script type="module" src="/index-abc123.js"></script><link rel="stylesheet" href="/index-def456.css">`,
			want: nil,
		},
		{
			name: "a reference the build did not write",
			html: `<script type="module" src="/index-abc123.js"></script><script type="module" src="/index-000000.js"></script>`,
			want: []string{"/index-000000.js"},
		},
		{
			name: "a chunk under a subdirectory",
			html: `<link rel="modulepreload" href="/chunks/lazy-789.js">`,
			want: nil,
		},
		{
			name: "a chunk under a subdirectory that is gone",
			html: `<link rel="modulepreload" href="/chunks/lazy-000.js">`,
			want: []string{"/chunks/lazy-000.js"},
		},
		{
			name: "a page that names no assets",
			html: `<div id="root"></div>`,
			want: nil,
		},
		{
			name: "an absolute URL is not this build's business",
			html: `<script src="https://cdn.example/x.js"></script>`,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := assetsMissingFromDisk(tc.html)
			sort.Strings(got)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("assetsMissingFromDisk(%q) = %v, want %v", tc.html, got, tc.want)
			}
		})
	}
}

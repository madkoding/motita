package codeindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitignoreRules(t *testing.T) {
	lines := []string{
		"# comment", "", "*.log", "/build-out", "tmp/", "docs/*.md", "!docs/keep.md", "**/gen", "src/**",
		"[abc].txt", "[!x].dat", "file?.txt", "bad[",
	}
	ig := ignores{"": parseIgnore(strings.Join(lines, "\n"))}
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"server.log", false, true},
		{"deep/er/server.log", false, true},
		{"build-out", true, true},
		{"sub/build-out", true, false},
		{"tmp", true, true},
		{"tmp", false, false},
		{"docs/a.md", false, true},
		{"docs/keep.md", false, false},
		{"docs/sub/a.md", false, false},
		{"x/y/gen", true, true},
		{"gen", true, true},
		{"src/anything/deep.go", false, true},
		{"a.txt", false, true},
		{"d.txt", false, false},
		{"y.dat", false, true},
		{"x.dat", false, false},
		{"file1.txt", false, true},
		{"file12.txt", false, false},
		{"main.go", false, false},
	}
	for _, tc := range cases {
		if got := ig.matched(tc.rel, tc.isDir); got != tc.want {
			t.Errorf("matched(%q, dir=%v) = %v, want %v", tc.rel, tc.isDir, got, tc.want)
		}
	}
}

func TestGitignoreDropsLinesThatDoNotCompileAndEmptyOnes(t *testing.T) {
	if got := parseIgnore("[z-a]\n/\n!\n"); len(got) != 0 {
		t.Errorf("rules = %d, want none", len(got))
	}
}

func TestNestedGitignoreAppliesBelowItsFolder(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".gitignore", "*.gen.go\n")
	write(t, root, "a/.gitignore", "local.txt\n!keep.gen.go\n")
	write(t, root, "a/local.txt", "needle")
	write(t, root, "a/x.gen.go", "needle")
	write(t, root, "a/keep.gen.go", "needle")
	write(t, root, "b/local.txt", "needle")
	write(t, root, "b/ok.go", "needle")
	files, _, err := scan(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.rel)
	}
	want := []string{"a/keep.gen.go", "b/local.txt", "b/ok.go"}
	if len(got) != len(want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("files = %v, want %v", got, want)
		}
	}
}

func TestIgnoredFolderIsNotEntered(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".gitignore", "secret/\n")
	write(t, root, "secret/a.txt", "needle")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	ix, _ := Build(context.Background(), root, Options{})
	if got := ix.Search("needle", 5); len(got) != 0 {
		t.Errorf("an ignored folder was indexed: %+v", got)
	}
}

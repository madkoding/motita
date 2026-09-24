package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The README's headline claim is that there are no external dependencies: "go.mod has no
// require line. There is no go.sum, nothing to vendor, nothing to patch." An operator picks
// this tool partly BECAUSE of that sentence, and it is the kind of claim that dies quietly —
// one import of a helper library is all it takes, and nothing else in the suite would notice.
//
// So it is checked the same way the floor's claims are: against the real file, with the
// parser exercised in both directions so the check cannot rot into a no-op that always passes.
func TestTheREADMEsNoDependenciesClaimHolds(t *testing.T) {
	root := repoRoot(t)

	mod := readFile(t, filepath.Join(root, "go.mod"))
	requires := requiredModules(mod)
	if len(requires) != 0 {
		t.Errorf("README says go.mod has no require line; it requires %d: %v", len(requires), requires)
	}

	if _, err := os.Stat(filepath.Join(root, "go.sum")); err == nil {
		t.Error("README says there is no go.sum, and one exists: something was pulled in")
	} else if !os.IsNotExist(err) {
		t.Fatalf("cannot tell whether go.sum exists: %v", err)
	}

	t.Logf("holds: %d require directives, no go.sum — the claim is still true", len(requires))
}

// The parser is the whole gate, so it is pinned against the shapes a go.mod actually takes.
// Without this, a rewrite that silently returned nothing would leave the test above passing
// forever while a dependency sat in the file.
func TestTheRequireParserSeesEveryShape(t *testing.T) {
	cases := []struct {
		name string
		mod  string
		want []string
	}{
		{
			name: "this repository, which has none",
			mod:  "module github.com/madkoding/motita\n\ngo 1.23\n",
			want: nil,
		},
		{
			name: "a single-line require",
			mod:  "module m\n\ngo 1.23\n\nrequire github.com/x/y v1.2.3\n",
			want: []string{"github.com/x/y v1.2.3"},
		},
		{
			name: "a require block, the shape a toolchain writes",
			mod:  "module m\n\ngo 1.23\n\nrequire (\n\tgithub.com/x/y v1.2.3\n\tgithub.com/a/b v0.0.1 // indirect\n)\n",
			want: []string{"github.com/x/y v1.2.3", "github.com/a/b v0.0.1 // indirect"},
		},
		{
			// A commented-out require is not a dependency, and counting it would make the
			// gate lie in the other direction.
			name: "a require that is only a comment",
			mod:  "module m\n\ngo 1.23\n\n// require github.com/x/y v1.2.3\n",
			want: nil,
		},
		{
			name: "a replaced module is still not a require",
			mod:  "module m\n\ngo 1.23\n\nreplace github.com/x/y => ../local\n",
			want: nil,
		},
	}

	for _, c := range cases {
		got := requiredModules(c.mod)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %q, want %q", c.name, got[i], c.want[i])
			}
		}
	}
}

// requiredModules returns the module paths named by every require directive, whether it is a
// one-line require or a block. Blank lines and comments inside a block are skipped.
func requiredModules(mod string) []string {
	var out []string
	inBlock := false
	for _, raw := range strings.Split(mod, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case inBlock && line == ")":
			inBlock = false
		case inBlock:
			if line == "" || strings.HasPrefix(line, "//") {
				continue
			}
			out = append(out, line)
		case line == "require (":
			inBlock = true
		case strings.HasPrefix(line, "require "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "require ")))
		}
	}
	return out
}

// repoRoot walks up from the test's working directory — the package directory, not the
// repository root — until it finds the go.mod that marks the root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot get the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("walked to the filesystem root without finding go.mod")
		}
		dir = parent
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return string(b)
}

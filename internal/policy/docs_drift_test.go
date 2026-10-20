package policy

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every count of platforms in the docs is the number of PLATFORMS in the Makefile, which is what
// `make dist` and the CI build matrix produce.
func TestTheDocsCountEveryPlatform(t *testing.T) {
	root := repoRoot(t)
	platforms := makefilePlatforms(t, root)
	want := len(platforms)

	words := map[string]int{"nine": 9, "ten": 10, "eleven": 11}

	docs := []string{"README.md", "CONTRIBUTING.md", "docs/GUIDE.md", "docs/REFERENCE.md",
		"docs/assets/readme/stats.svg", "docs/assets/readme/build.py", "site/index.html"}
	checked := 0
	for _, doc := range docs {
		for _, s := range platformCounts(readFile(t, filepath.Join(root, doc))) {
			n, err := strconv.Atoi(s)
			if err != nil {
				n = words[strings.ToLower(s)]
			}
			checked++
			if n != want {
				t.Errorf("%s says %s platforms; the Makefile builds %d", doc, s, want)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no platform count found in any doc, so nothing was checked")
	}

	// The size table in REFERENCE.md lists one binary per platform.
	ref := readFile(t, filepath.Join(root, "docs/REFERENCE.md"))
	for _, p := range platforms {
		name := "dist/motita-" + strings.Replace(p, "/", "-", 1)
		if strings.HasPrefix(p, "windows/") {
			name += ".exe"
		}
		if !strings.Contains(ref, "`"+name+"`") {
			t.Errorf("docs/REFERENCE.md has no row for %s in the binaries table", name)
		}
	}
}

var (
	platformCountRe = regexp.MustCompile(`(?i)\b(\d+|nine|ten|eleven)\s+(?:supported\s+)?` +
		`(?:platforms|targets|binaries|platform/architecture)\b`)
	// The landing page writes the number and the word in two spans.
	platformStatRe = regexp.MustCompile(`(?s)class="n">(\d+)</span\s*><span[^>]*class="l"\s*>platforms`)
)

// platformCounts is every count of platforms a doc states, as written: a number or a word.
func platformCounts(body string) []string {
	var found []string
	for _, m := range platformCountRe.FindAllStringSubmatch(body, -1) {
		found = append(found, m[1])
	}
	for _, m := range platformStatRe.FindAllStringSubmatch(body, -1) {
		found = append(found, m[1])
	}
	return found
}

func makefilePlatforms(t *testing.T, root string) []string {
	t.Helper()
	mk := readFile(t, filepath.Join(root, "Makefile"))
	m := regexp.MustCompile(`(?m)^PLATFORMS\s*:=((?:[^\n]*\\\n)*[^\n]*)`).FindStringSubmatch(mk)
	if m == nil {
		t.Fatal("PLATFORMS is gone from the Makefile; change this test with it")
	}
	return strings.Fields(strings.ReplaceAll(m[1], "\\", " "))
}

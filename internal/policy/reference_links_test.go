package policy

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// docs/REFERENCE.md is read on GitHub, where a relative link resolves against docs/. Its copy in
// site/ is byte-identical, so a link that leaves docs/ has to be absolute to work in both.
func TestTheReferenceLinksResolve(t *testing.T) {
	root := repoRoot(t)
	ref := readFile(t, filepath.Join(root, "docs/REFERENCE.md"))

	linkRe := regexp.MustCompile(`\]\(([^)\s]+)\)`)
	checked := 0
	for _, m := range linkRe.FindAllStringSubmatch(ref, -1) {
		target := refPath(m[1])
		if isExternalRef(target) || strings.HasPrefix(m[1], "#") {
			continue
		}
		checked++
		if _, err := os.Stat(filepath.Join(root, "docs", target)); err != nil {
			t.Errorf("docs/REFERENCE.md links %q, which does not exist under docs/", m[1])
		}
	}
	if checked == 0 {
		t.Fatal("no relative link found, so nothing was checked")
	}

	cases, err := filepath.Glob(filepath.Join(root, "configs/cases/*.yaml"))
	if err != nil || len(cases) == 0 {
		t.Fatalf("no use cases found in configs/cases: %v", err)
	}
	m := regexp.MustCompile(`configs/\s+example configuration \+ (\d+) use cases`).FindStringSubmatch(ref)
	if m == nil {
		t.Fatal("the configs/ line of the project layout is gone; change this test with it")
	}
	if m[1] != strconv.Itoa(len(cases)) {
		t.Errorf("docs/REFERENCE.md says %s use cases; configs/cases holds %d", m[1], len(cases))
	}
}

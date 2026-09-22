package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The published site lives in site/ and is uploaded on its own, so it cannot link to the
// files in docs/ — Pages only ever sees what is inside site/. That is why the reference
// documents exist twice, and a copy is exactly the kind of thing that drifts: nobody edits
// the second one, and the version a visitor reads quietly stops matching the repository.
//
// It had already drifted once by the time this test was written (a size table edited in
// docs/ and not in site/). Rather than trust care, the copies are checked here.
//
// The fix, if this fails, is a copy — not an edit:
//
//	cp docs/REFERENCE.md site/REFERENCE.md
func TestThePublishedDocCopiesMatchTheirSource(t *testing.T) {
	root := repoRoot(t)

	// Every document the site needs locally, because it links to it by relative path.
	pairs := []struct {
		source string
		copy   string
		why    string
	}{
		{"docs/REFERENCE.md", "site/REFERENCE.md", `linked from site/index.html as href="REFERENCE.md"`},
		{"docs/TUI-DESIGN-REVIEW.md", "site/TUI-DESIGN-REVIEW.md", `linked from site/index.html as href="TUI-DESIGN-REVIEW.md"`},
	}

	for _, p := range pairs {
		want := readFile(t, filepath.Join(root, p.source))
		got, err := os.ReadFile(filepath.Join(root, p.copy))
		if err != nil {
			t.Errorf("%s must exist and match %s (%s): %v", p.copy, p.source, p.why, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s has drifted from %s (%s).\nCopy it over:  cp %s %s",
				p.copy, p.source, p.why, p.source, p.copy)
			continue
		}
		t.Logf("in sync: %s == %s (%d bytes)", p.copy, p.source, len(want))
	}
}

// And the other half of the same concern: if the site links a document, that document has
// to be inside site/ or the link is dead in production while working perfectly in a local
// checkout that still has docs/ next to it. The Pages workflow already guards this; checking
// it here means a broken link fails in `make check` too, before a push, instead of only in
// the deploy job afterwards.
//
// Pages are checked as a set rather than individually, because they are built differently:
// index.html is hand-written and links its documents by relative path, while
// architecture.html is self-contained and builds every link in JavaScript against absolute
// URLs. Requiring local references from each page would fail one of them for the sin of
// being built the other way.
func TestEveryLocalSiteLinkResolves(t *testing.T) {
	site := filepath.Join(repoRoot(t), "site")
	pages, err := filepath.Glob(filepath.Join(site, "*.html"))
	if err != nil {
		t.Fatalf("cannot list the site pages: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("no pages found in site/: this check is not looking at the site")
	}

	total := 0
	for _, page := range pages {
		body := readFile(t, page)
		refs := localRefs(body)
		total += len(refs)
		for _, ref := range refs {
			if _, err := os.Stat(filepath.Join(site, ref)); err != nil {
				t.Errorf("%s links %q, which does not exist inside site/: it will be dead once only site/ is published",
					filepath.Base(page), ref)
			}
		}
		t.Logf("%-22s %d local references, all resolve", filepath.Base(page), len(refs))
	}

	// If the scan silently stopped matching, every page would pass for the wrong reason.
	// index.html is the page that does link local files, so it must contribute some.
	if total == 0 {
		t.Error("no local references found anywhere in site/, so nothing was actually checked")
	}
}

// localRefs returns the href/src values meant to resolve inside site/: relative paths only,
// with anchors, absolute URLs and data: URIs left out. Fragments are stripped so that
// "REFERENCE.md#section" is checked as "REFERENCE.md".
func localRefs(html string) []string {
	var refs []string
	seen := map[string]bool{}

	for _, attr := range []string{`href="`, `src="`} {
		for rest := html; ; {
			i := strings.Index(rest, attr)
			if i < 0 {
				break
			}
			rest = rest[i+len(attr):]
			end := strings.Index(rest, `"`)
			if end < 0 {
				break
			}
			val := rest[:end]
			rest = rest[end:]

			if path, _, _ := strings.Cut(val, "#"); path != "" {
				val = path
			}
			if val == "" || seen[val] || isExternalRef(val) {
				continue
			}
			seen[val] = true
			refs = append(refs, val)
		}
	}
	return refs
}

func isExternalRef(ref string) bool {
	for _, p := range []string{"http://", "https://", "mailto:", "data:", "//", "#"} {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return false
}

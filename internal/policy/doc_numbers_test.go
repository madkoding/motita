package policy

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The landing page and docs/GUIDE.md (the README's long form) both quote numbers at
// a reader who is deciding whether to trust the project: how many packages ship, how
// many tests there are, how big the binary is. Every one of those is produced by a
// command, and a table edited by hand drifts the moment the code moves.
//
// Measured on this repository: the README spent several releases claiming
// "20 of 20" packages when 30 ship, "1,763 test functions across 110 files" when
// there are 3,141 across 202, and "6.9 - 7.7 MB per binary" when the released
// binaries are 17.1 - 18.1 MiB. The size row predates the web interface being
// compiled into the binary at all, which is why it is wrong by more than double.
// Nothing on the page said so, and a reader cannot tell.
//
// This is deliberately a CHECK of the prose, not a generator: rewriting the
// documents from a test would make the numbers true by construction and hide a
// real divergence, and a number a reader can verify by hand has to stay
// hand-written to be worth anything.
//
// Tolerances, and why they are not a loophole:
//
//   - Package counts are asserted EXACTLY. A package appears or disappears when
//     somebody decides to, which is precisely the moment the coverage claim and
//     the "checked one by one" sentence change meaning.
//   - Test counts and line counts carry a tolerance because they move in EVERY
//     commit. An exact gate there would fail unrelated pull requests and teach
//     people to edit the number without reading it, which is worse than a
//     tolerance that still catches a claim that has drifted into being
//     misleading - the 1,763 above is 44% below the truth, and that fails.
//   - The binary size cannot be derived without building all nine targets, so it
//     is bounded instead: the unit must be MiB, the range must be ordered, the
//     top must stay under the ceiling the size gate enforces, and the bottom
//     must clear the assets every binary carries. That is what catches the real
//     defects found here (a "MB" that is really a MiB, and a range starting
//     below the embedded web interface).
func TestTheDocumentsQuoteTheNumbersTheTreeActuallyHas(t *testing.T) {
	root := repoRoot(t)

	dirs := shippingPackageDirs(t, root)
	withTests := 0
	testFiles, testFuncs, testLines, srcLines := 0, 0, 0, 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		hasTest := false
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", filepath.Join(dir, e.Name()), err)
			}
			n := newlineCount(body)
			if strings.HasSuffix(e.Name(), "_test.go") {
				hasTest = true
				testFiles++
				testLines += n
				testFuncs += countTestFuncs(body)
				continue
			}
			srcLines += n
		}
		if hasTest {
			withTests++
		}
	}

	t.Logf("measured: %d packages (%d with tests), %d test functions in %d files, %d lines of Go vs %d of test",
		len(dirs), withTests, testFuncs, testFiles, srcLines, testLines)
	if len(dirs) == 0 || testFiles == 0 {
		t.Fatal("nothing was measured, so this check would pass by accident")
	}

	guide := readDoc(t, filepath.Join(root, "docs", "GUIDE.md"))
	checkPackageClaim(t, guide, "docs/GUIDE.md", len(dirs), withTests)
	checkTestFuncClaim(t, guide, "docs/GUIDE.md", testFuncs, testFiles)
	checkLineCountClaims(t, guide, "docs/GUIDE.md", srcLines, testLines)
	checkBinarySizeClaim(t, guide, "docs/GUIDE.md")

	site := readDoc(t, filepath.Join(root, "site/index.html"))
	checkPackageClaim(t, site, "site/index.html", len(dirs), withTests)
	checkBinarySizeClaim(t, site, "site/index.html")
}

// tolerance is the fraction a churning count may drift before the document is
// called wrong. Generous enough that ordinary work does not trip it, tight enough
// that the drift this test was written for fails loudly.
const tolerance = 0.15

// The coverage claim is the one a reader is most likely to check by hand, so both
// documents write it as "<N> of <M>" against the shipping packages, and both halves
// are asserted: "100% of everything" with a wrong denominator is still a false claim.
// The two documents spell the scope differently - Markdown backticks in the README,
// <code> tags on the page - so the pattern spans to the scope rather than matching it.
var pkgClaimRe = regexp.MustCompile(`(?s)(\d+) of (\d+).{0,160}?internal/\.\.\.`)

func checkPackageClaim(t *testing.T, doc, name string, total, withTests int) {
	t.Helper()
	m := pkgClaimRe.FindStringSubmatch(doc)
	if m == nil {
		t.Errorf("%s: the \"<N> of <M>\" package claim is gone; if the wording changed, change this test with it", name)
		return
	}
	got, want := atoi(t, m[1]), atoi(t, m[2])
	bad := false
	if want != total {
		t.Errorf("%s is WRONG about how many packages ship: says %d, the tree has %d", name, want, total)
		bad = true
	}
	if got != withTests {
		t.Errorf("%s is WRONG about how many of them have tests: says %d, %d do", name, got, withTests)
		bad = true
	}
	if !bad {
		t.Logf("%s: %d of %d packages, matches the tree", name, got, want)
	}
}

var funcClaimRe = regexp.MustCompile(`([\d,]+)\s+across\s+([\d,]+)\s+files`)

func checkTestFuncClaim(t *testing.T, doc, name string, funcs, files int) {
	t.Helper()
	m := funcClaimRe.FindStringSubmatch(doc)
	if m == nil {
		t.Errorf("%s: the \"<N> across <M> files\" test claim is gone; if the wording changed, change this test with it", name)
		return
	}
	gotF := atoi(t, strings.ReplaceAll(m[1], ",", ""))
	gotN := atoi(t, strings.ReplaceAll(m[2], ",", ""))
	bad := false
	if !closeEnough(gotF, funcs) {
		t.Errorf("%s is WRONG about the test count: says %d, the tree has %d (%.0f%% off, tolerance %.0f%%)",
			name, gotF, funcs, offBy(gotF, funcs)*100, tolerance*100)
		bad = true
	}
	if !closeEnough(gotN, files) {
		t.Errorf("%s is WRONG about the test file count: says %d, the tree has %d", name, gotN, files)
		bad = true
	}
	if !bad {
		t.Logf("%s: %d test functions across %d files, matches the tree", name, gotF, gotN)
	}
}

var lineClaimRe = regexp.MustCompile(`([\d,]+)\s+lines of Go\s*[·|\-*]?\s*([\d,]+)\s+lines of test`)

func checkLineCountClaims(t *testing.T, doc, name string, srcLines, testLines int) {
	t.Helper()
	m := lineClaimRe.FindStringSubmatch(doc)
	if m == nil {
		// A document may legitimately drop the row. Say so rather than pass
		// silently, so a removed row is visible in the log.
		t.Logf("%s: no line-count claim to check", name)
		return
	}
	gotS := atoi(t, strings.ReplaceAll(m[1], ",", ""))
	gotT := atoi(t, strings.ReplaceAll(m[2], ",", ""))
	bad := false
	if !closeEnough(gotS, srcLines) {
		t.Errorf("%s is WRONG about the Go line count: says %d, the tree has %d", name, gotS, srcLines)
		bad = true
	}
	if !closeEnough(gotT, testLines) {
		t.Errorf("%s is WRONG about the test line count: says %d, the tree has %d", name, gotT, testLines)
		bad = true
	}
	if !bad {
		t.Logf("%s: %d lines of Go vs %d of test, matches the tree", name, gotS, gotT)
	}
}

// The binary size is the one number a document CANNOT derive from the tree, and the
// one most likely to rot: it moves with the toolchain and with what is embedded. It
// is bounded rather than asserted, and the bound is what catches the defects found
// here: a "MB" where the gate measures bytes (24 MB is not 24 MiB), and a range that
// starts BELOW the embedded web interface every binary already carries.
var sizeClaimRe = regexp.MustCompile(`([\d.]+)\s*[–-]\s*([\d.]+)\s*(?:&nbsp;)?(MiB|MB)`)

const sizeCeilingMiB = 24.0

func checkBinarySizeClaim(t *testing.T, doc, name string) {
	t.Helper()
	m := sizeClaimRe.FindStringSubmatch(doc)
	if m == nil {
		t.Errorf("%s: the binary-size range is gone; if the wording changed, change this test with it", name)
		return
	}
	lo, errLo := strconv.ParseFloat(m[1], 64)
	hi, errHi := strconv.ParseFloat(m[2], 64)
	if errLo != nil || errHi != nil || lo <= 0 || hi < lo {
		t.Errorf("%s quotes a size range that is not one: %s – %s %s", name, m[1], m[2], m[3])
		return
	}
	if m[3] == "MB" {
		t.Errorf("%s writes the unit as MB, but the size gate and the release assets both measure "+
			"bytes (MiB): %.1f MB is %.1f MiB, and the ceiling is %.0f MiB", name, hi, hi*1000*1000/(1024*1024), sizeCeilingMiB)
	}
	if hi >= sizeCeilingMiB {
		t.Errorf("%s advertises binaries up to %.1f %s, but the gate fails at %.0f MiB — a document "+
			"must not advertise a size the project would refuse to ship", name, hi, m[3], sizeCeilingMiB)
	}
	if floor := embeddedAssetBytes(t, name); floor > 0 && lo*1024*1024 <= float64(floor) {
		t.Errorf("%s says the smallest binary is %.1f %s, which is under the %d bytes (%.1f MiB) of web "+
			"assets compiled into every binary", name, lo, m[3], floor, float64(floor)/(1024*1024))
	}
	t.Logf("%s: claims %.1f – %.1f %s per binary (ceiling %.0f MiB, assets floor %.1f MiB)",
		name, lo, hi, m[3], sizeCeilingMiB, float64(embeddedAssetBytes(t, name))/(1024*1024))
}

// embeddedAssetBytes sums the web assets that are compiled into EVERY binary, which
// is the only part of the size a test can bound without building one. It is a floor,
// not the binary size: the Go code and the runtime sit on top of it.
func embeddedAssetBytes(t *testing.T, name string) int {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "internal/webui/assets")
	total := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		total += int(info.Size())
		return nil
	})
	if err != nil {
		t.Logf("could not measure the embedded assets for %s: %v", name, err)
		return 0
	}
	return total
}

// shippingPackageDirs asks the toolchain for the directories of `./internal/...` and
// `./cmd/...` — the exact scope every claim in these documents is written against —
// so the numbers here are the ones a reader reproduces with the same command.
func shippingPackageDirs(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", "./internal/...", "./cmd/...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list ./internal/... ./cmd/...: %v", err)
	}
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			dirs = append(dirs, line)
		}
	}
	return dirs
}

func countTestFuncs(body []byte) int {
	n := 0
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "func Test") && strings.Contains(line, "(") {
			n++
		}
	}
	return n
}

// newlineCount is what `wc -l` prints, so a reader checking a document by hand
// gets the same number this test compared against.
func newlineCount(body []byte) int { return strings.Count(string(body), "\n") }

func closeEnough(doc, real int) bool {
	if real == 0 {
		return doc == 0
	}
	diff := float64(doc - real)
	if diff < 0 {
		diff = -diff
	}
	return diff/float64(real) <= tolerance
}

func offBy(doc, real int) float64 {
	if real == 0 {
		return 0
	}
	d := float64(doc - real)
	if d < 0 {
		d = -d
	}
	return d / float64(real)
}

func readDoc(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s must exist: %v", path, err)
	}
	return string(body)
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("not a number: %q", s)
	}
	return n
}

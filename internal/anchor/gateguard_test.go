package anchor

// Tests for what the gate fingerprint reads beyond the gate's own files, the test floor, the
// heuristics that refuse a gate that checks nothing, and the PASS that ran no tests.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// TestFingerprintFollowsWhatTheGateRuns: rewriting a makefile the Makefile includes, a script
// package.json calls, or the Python configuration weakens the gate with Makefile and package.json
// untouched, so each must change the fingerprint. A source file that only appears in a build
// command must not.
func TestFingerprintFollowsWhatTheGateRuns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "include mk/rules.mk\n-include missing.mk\ncheck:\n\t@echo gate\n\tgo build main.go\n")
	writeFile(t, dir, "mk/rules.mk", "include mk/more.mk\n")
	writeFile(t, dir, "mk/more.mk", "X=1\n")
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "package.json", `{"scripts":{"test":"node ./scripts/test.js \"quoted\""}}`)
	writeFile(t, dir, "scripts/test.js", "process.exit(1)\n")
	writeFile(t, dir, "conftest.py", "")
	writeFile(t, dir, "sub/conftest.py", "")
	writeFile(t, dir, "node_modules/x/conftest.py", "")
	fp := func() string { return autoAnchor(dir).GateFingerprint() }

	start := fp()
	writeFile(t, dir, "main.go", "package main\n// edited\n")
	if fp() != start {
		t.Error("a source file named by a build command is ordinary work, not a gate change")
	}
	writeFile(t, dir, "node_modules/x/conftest.py", "changed")
	if fp() != start {
		t.Error("dependencies are not part of the gate")
	}
	for _, change := range []struct{ name, body string }{
		{"mk/rules.mk", "include mk/more.mk\nY=1\n"},
		{"mk/more.mk", "X=2\n"},
		{"scripts/test.js", "process.exit(0)\n"},
		{"conftest.py", "import os\n"},
		{"sub/conftest.py", "import os\n"},
		{"pyproject.toml", "[tool.pytest.ini_options]\n"},
	} {
		before := fp()
		writeFile(t, dir, change.name, change.body)
		if fp() == before {
			t.Errorf("%s must change the fingerprint", change.name)
		}
	}
}

// TestReferencedFilesStayInsideTheProject: a token that points outside the directory, at the
// directory itself or at a missing file is not read.
func TestReferencedFilesStayInsideTheProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.sh", "")
	got := autoAnchor(dir).referencedFiles([]string{"../out.sh /abs.sh .. . ok.sh ./ok.sh gone.sh sub.sh"})
	if len(got) != 1 || got[0] != "ok.sh" {
		t.Fatalf("expected only ok.sh once, got %v", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := autoAnchor(dir).referencedFiles([]string{"sub.sh"}); len(got) != 0 {
		t.Errorf("a directory is not a script: %v", got)
	}
}

// TestTestCountDropsWhenTestsAreDeletedOrEmptied: adding tests raises the count; deleting or
// emptying them lowers it.
func TestTestCountDropsWhenTestsAreDeletedOrEmptied(t *testing.T) {
	dir := t.TempDir()
	a := autoAnchor(dir)
	if a.TestCount() != 0 {
		t.Fatal("an empty tree has no tests")
	}
	writeFile(t, dir, "a_test.go", "package a\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) {}\n")
	writeFile(t, dir, "tests/test_x.py", "def test_a():\n    pass\nasync def test_b():\n    pass\n")
	writeFile(t, dir, "web/x.test.ts", "it('a', () => {})\ntest.each([1])('b', () => {})\n")
	writeFile(t, dir, "src/lib.rs", "#[test]\nfn a() {}\n")
	writeFile(t, dir, "src/plain.go", "func TestNotAFile() {}\n")
	writeFile(t, dir, "node_modules/p/y_test.go", "func TestIgnored() {}\n")
	writeFile(t, dir, "big_test.go", strings.Repeat("// pad\n", maxGateFile/7+10)+"func TestPastTheLimit() {}\n")
	full := a.TestCount()
	if full != 7 {
		t.Fatalf("expected 7 tests, got %d", full)
	}
	writeFile(t, dir, "a_test.go", "package a\n")
	if a.TestCount() >= full {
		t.Error("emptying a test file must lower the count")
	}
	if err := os.Remove(filepath.Join(dir, "tests/test_x.py")); err != nil {
		t.Fatal(err)
	}
	if a.TestCount() >= full-2 {
		t.Error("deleting a test file must lower the count")
	}
}

// TestTestCountSurvivesUnreadableEntries: a directory that cannot be listed or a test file that
// cannot be read is skipped, not fatal.
func TestTestCountSurvivesUnreadableEntries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok_test.go", "func TestOk() {}\n")
	if err := os.Mkdir(filepath.Join(dir, "dir_test.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := autoAnchor(dir).TestCount(); got != 1 {
		t.Errorf("expected 1, got %d", got)
	}
	if got := autoAnchor(filepath.Join(dir, "missing")).TestCount(); got != 0 {
		t.Errorf("a missing directory has no tests, got %d", got)
	}
	if got := autoAnchor(filepath.Join(dir, "missing")).GateFingerprint(); got == "" {
		t.Error("a missing directory still has a fingerprint")
	}
	if _, ok := readLimited(filepath.Join(dir, "missing")); ok {
		t.Error("a missing file cannot be read")
	}
	if _, ok := readLimited(filepath.Join(dir, "dir_test.go")); ok {
		t.Error("a directory cannot be read as a file")
	}
}

// TestTrivialGatesAreNotGates: a target or script that does nothing is not declared, so the next
// convention speaks for the project.
func TestTrivialGatesAreNotGates(t *testing.T) {
	for _, body := range []string{
		"check: ; true\n",
		"check:\n",
		"check:\n\t@true\n\t-:\n",
		"check:\n\n# c\n\texit 0\n",
		"check:\n\ttrue\nother:\n\techo x\n",
		".PHONY: check\ncheck:\n\ttrue && true\n",
	} {
		dir := t.TempDir()
		writeFile(t, dir, "Makefile", body)
		writeFile(t, dir, "go.mod", "module x\n")
		checks := autoAnchor(dir).detectChecks()
		if len(checks) != 1 || checks[0].Name != "go test" {
			t.Errorf("%q must not count as the gate, got %+v", body, checks)
		}
	}
	for _, body := range []string{
		"check: dep\n\t@true\n", "check: ; echo hi\n", "check:\n\t@true\n\tmake lint\n", "check:\n\techo\n#x\nlint:\n",
	} {
		dir := t.TempDir()
		writeFile(t, dir, "Makefile", body)
		if checks := autoAnchor(dir).detectChecks(); len(checks) != 1 || checks[0].Name != "check" {
			t.Errorf("%q is a real target, got %+v", body, checks)
		}
	}
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"scripts":{"lint":"","test":"exit 0","typecheck":"tsc && true","build":"x"}}`)
	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 || checks[0].Name != "npm typecheck" {
		t.Errorf("only the script that checks something is a gate, got %+v", checks)
	}
}

// TestPytestNeedsEvidence: a pyproject.toml or setup.py that never mentions pytest is not a pytest
// project; any of the signs below is.
func TestPytestNeedsEvidence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"x\"\n")
	writeFile(t, dir, "setup.py", "from setuptools import setup\n")
	if checks := autoAnchor(dir).detectChecks(); len(checks) != 0 {
		t.Errorf("no evidence of pytest, got %+v", checks)
	}
	for _, f := range []struct{ name, body string }{
		{"pytest.ini", ""}, {"conftest.py", ""}, {"tox.ini", ""},
		{"requirements-dev.txt", "PyTest>=7\n"}, {"setup.cfg", "[tool:pytest]\n"},
	} {
		d := t.TempDir()
		writeFile(t, d, "pyproject.toml", "[project]\n")
		writeFile(t, d, f.name, f.body)
		if checks := autoAnchor(d).detectChecks(); len(checks) != 1 || checks[0].Name != "pytest" {
			t.Errorf("%s is evidence of pytest, got %+v", f.name, checks)
		}
	}
}

// TestAPassThatRanNoTestsIsUnverified: `go test ./...` over a tree with no test files exits 0, and
// a green nobody earned is reported as such.
func TestAPassThatRanNoTestsIsUnverified(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/x\n\ngo 1.21\n")
	writeFile(t, dir, "x.go", "package x\n")
	res := autoAnchor(dir).Validate(context.Background())
	if !res.Pass || !res.Unverified || !strings.Contains(res.Reason, "no tests") {
		t.Fatalf("expected an unverified pass, got %+v", res)
	}
	writeFile(t, dir, "x_test.go", "package x\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	if res := autoAnchor(dir).Validate(context.Background()); !res.Pass || res.Unverified {
		t.Fatalf("a tree with tests is verified, got %+v", res)
	}
}

// TestVacuousOnlyForBareToolchainChecks: a project's own gate or an explicit command is never
// second-guessed by counting tests.
func TestVacuousOnlyForBareToolchainChecks(t *testing.T) {
	dir := t.TempDir()
	bare := []config.Check{{Name: "go test"}, {Name: "cargo test"}, {Name: "pytest"}}
	if !autoAnchor(dir).vacuous(bare) {
		t.Error("bare toolchain checks over an empty tree are vacuous")
	}
	if autoAnchor(dir).vacuous([]config.Check{{Name: "go test"}, {Name: "check"}}) {
		t.Error("a project-written check is not vacuous")
	}
	if New(config.Anchor{Kind: "command"}, dir, nil).vacuous(bare) {
		t.Error("only kind=auto is judged")
	}
}

// TestTruncateKeepsValidUTF8: a cut inside a multi-byte character backs off to its start.
func TestTruncateKeepsValidUTF8(t *testing.T) {
	if got := truncate("ab", 5); got != "ab" {
		t.Errorf("short input is unchanged, got %q", got)
	}
	got := truncate("aéé", 2) // 'é' is two bytes: byte 2 is the middle of the first one
	if got != "a..." {
		t.Errorf("expected the cut to back off to a rune boundary, got %q", got)
	}
	if got := truncate("abc", 0); got != "..." {
		t.Errorf("got %q", got)
	}
}

// HasGate and TestsIn answer for a bare folder, with no configuration or anchor built by the caller.
func TestHasGateAndTestsInReadTheFolder(t *testing.T) {
	dir := t.TempDir()
	if HasGate(dir) || TestsIn(dir) != 0 {
		t.Fatal("an empty folder declares neither a gate nor tests")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	test := []byte("package x\nfunc TestA(t *testing.T) {}\n")
	if err := os.WriteFile(filepath.Join(dir, "a_test.go"), test, 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasGate(dir) || TestsIn(dir) != 1 {
		t.Fatalf("HasGate %v, TestsIn %d", HasGate(dir), TestsIn(dir))
	}
}

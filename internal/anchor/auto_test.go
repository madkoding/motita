package anchor

// Tests for anchor.kind=auto: the anchor reads the gate the PROJECT declares.
//
// The defect these pin is a hardcoded one. A configuration carrying `npm run lint`
// validates a Node project and is simply wrong in a Go repository, so the person
// working on two projects has to edit the file on every switch - and a stale gate
// does not fail safe: the anchor refuses PASS and the task cannot finish.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// writeFile puts a file in dir, failing the test if it cannot.
func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// autoCfg is the configuration kind=auto needs: no command, because the command
// comes from the project.
func autoCfg() config.Anchor {
	return config.Anchor{Kind: "auto"}
}

// autoAnchor builds an auto anchor over dir.
func autoAnchor(dir string) *Anchor {
	return New(autoCfg(), dir, nil)
}

// TestAutoUsesTheDeclaredGateFile: a project can name its own gate, one command per
// line, and that wins over every convention. This is what makes the detection
// genuinely agnostic rather than a list of languages somebody has to extend.
func TestAutoUsesTheDeclaredGateFile(t *testing.T) {
	dir := t.TempDir()
	// A Makefile that WOULD be detected, to prove the declared file wins over it.
	writeFile(t, dir, "Makefile", "check:\n\t@echo wrong-gate\n")
	writeFile(t, dir, ".motita/anchor", "# the project's real gate\necho declared-gate\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 {
		t.Fatalf("expected 1 declared check, got %d: %+v", len(checks), checks)
	}
	if checks[0].Name != "declared" {
		t.Errorf("the declared gate should win over the Makefile, got name %q", checks[0].Name)
	}
	if got := strings.Join(checks[0].Args, " "); !strings.Contains(got, "declared-gate") {
		t.Errorf("the declared command was not used: %q", got)
	}
}

// TestAutoSkipsCommentsAndBlanksInTheDeclaredFile: the file is documentation too, so
// a comment or a blank line must not become a check.
func TestAutoSkipsCommentsAndBlanksInTheDeclaredFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".motita/anchor", "# a comment\n\n   \necho one\necho two\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks (the comments and blanks skipped), got %d: %+v", len(checks), checks)
	}
	// Each line is a check of its own, with a name of its own: the baseline tells them apart by it.
	if checks[0].Name != "declared" || checks[1].Name != "declared 2" {
		t.Errorf("names = %q, %q; want \"declared\", \"declared 2\"", checks[0].Name, checks[1].Name)
	}
}

// TestAutoPrefersTheMakefileCheck: `check` is the conventional name for the whole
// gate, so it wins over `test` when a project has both.
func TestAutoPrefersTheMakefileCheck(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "test:\n\t@true\ncheck:\n\t@true\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 || checks[0].Name != "check" {
		t.Fatalf("expected the `check` target, got %+v", checks)
	}
}

// TestAutoFallsBackToMakeTest: a Makefile with only `test` still gives a gate.
func TestAutoFallsBackToMakeTest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "test:\n\t@true\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 || checks[0].Name != "test" {
		t.Fatalf("expected the `test` target, got %+v", checks)
	}
}

// TestAutoDoesNotReadAVariableAsATarget: `check := something` begins with a name and
// a colon, so a naive pattern reads a variable as a target and the anchor runs
// `make check` on a Makefile that does not define it.
func TestAutoDoesNotReadAVariableAsATarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "check := not-a-target\nall:\n\t@true\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 0 {
		t.Fatalf("a variable named check is not a target, got %+v", checks)
	}
}

// TestAutoDetectsGo: a go.mod means the toolchain's own test command.
func TestAutoDetectsGo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/x\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 || checks[0].Command != "go" {
		t.Fatalf("expected a go check, got %+v", checks)
	}
}

// TestAutoDetectsNpmScriptsThatAreGates: lint, typecheck and test are the scripts
// that mean "a gate". `build` is deliberately excluded - a project can have a build
// needing credentials or five minutes of compilation, and a validator that cannot
// run is a task that cannot finish.
func TestAutoDetectsNpmScriptsThatAreGates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json",
		`{"scripts":{"build":"next build","lint":"eslint .","typecheck":"tsc --noEmit","test":"vitest run"}}`)

	checks := autoAnchor(dir).detectChecks()
	names := map[string]bool{}
	for _, c := range checks {
		names[c.Name] = true
	}
	if !names["npm lint"] || !names["npm typecheck"] || !names["npm test"] {
		t.Errorf("expected lint, typecheck and test, got %+v", checks)
	}
	if names["npm build"] {
		t.Errorf("build must not be a gate: it can need credentials or minutes, got %+v", checks)
	}
}

// TestAutoIgnoresAnNpmScriptAbsent: a package.json without a lint script must not
// produce a check that runs a script nobody declared.
func TestAutoIgnoresAnNpmScriptAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"scripts":{"dev":"next dev"}}`)

	if checks := autoAnchor(dir).detectChecks(); len(checks) != 0 {
		t.Fatalf("no gate script means no checks, got %+v", checks)
	}
}

// TestAutoDetectsCargo.
func TestAutoDetectsCargo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"x\"\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 || checks[0].Command != "cargo" {
		t.Fatalf("expected a cargo check, got %+v", checks)
	}
}

// TestAutoDetectsPython: pytest is proposed for a project declaring itself with
// pyproject.toml, and it is the last convention because it is the loosest.
func TestAutoDetectsPython(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"x\"\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 || checks[0].Command != "pytest" {
		t.Fatalf("expected a pytest check, got %+v", checks)
	}
}

// TestAutoPrefersMakeOverTheLanguages: a repository that is both Go and Node runs
// the umbrella its Makefile declares, not two suites nobody asked for. The order is
// what keeps the gate bounded and predictable.
func TestAutoPrefersMakeOverTheLanguages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "check:\n\t@true\n")
	writeFile(t, dir, "go.mod", "module example.com/x\n")
	writeFile(t, dir, "package.json", `{"scripts":{"test":"vitest run"}}`)

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 || checks[0].Command != "make" {
		t.Fatalf("the Makefile's umbrella should win, got %+v", checks)
	}
}

// TestAutoWithNoGateFAILSAndSaysSo: the critical one. A directory with no gate must
// NOT produce an empty check list that leaves Pass true - that is an optimistic PASS
// over a project nobody checked, the exact failure the anchor exists to refuse.
func TestAutoWithNoGateFAILSAndSaysSo(t *testing.T) {
	dir := t.TempDir()

	res := autoAnchor(dir).Validate(context.Background())
	if res.Pass {
		t.Fatal("a directory with no gate must NOT PASS: an empty list gave an optimistic PASS")
	}
	// The reason must name what was looked for, so the reader can fix it without
	// reading this package's source.
	for _, want := range []string{".motita/anchor", "Makefile", "go.mod", "package.json"} {
		if !strings.Contains(res.Reason, want) {
			t.Errorf("the reason should name %q so it is actionable, got: %s", want, res.Reason)
		}
	}
}

// TestAutoRunsTheDetectedGateForReal: the detection must actually reach a command.
// A directory whose declared gate exits non-zero is a FAIL, and one that exits zero
// is a PASS - both measured through Validate, not through the check list.
func TestAutoRunsTheDetectedGateForReal(t *testing.T) {
	pass := t.TempDir()
	writeFile(t, pass, ".motita/anchor", "echo the-gate-ran\n")
	if res := autoAnchor(pass).Validate(context.Background()); !res.Pass {
		t.Fatalf("a declared gate that exits 0 must PASS, reason: %s", res.Reason)
	} else if len(res.Checks) != 1 || !strings.Contains(res.Checks[0].Output, "the-gate-ran") {
		t.Errorf("the declared gate did not run: %+v", res.Checks)
	}

	fail := t.TempDir()
	writeFile(t, fail, ".motita/anchor", "echo broken && exit 3\n")
	res := autoAnchor(fail).Validate(context.Background())
	if res.Pass {
		t.Fatal("a declared gate that exits 3 must FAIL")
	}
	if res.Checks[0].Exit != 3 {
		t.Errorf("recorded exit = %d, want 3", res.Checks[0].Exit)
	}
}

// TestAutoIsIdempotent: detection only reads, so asking twice over one directory
// gives the same answer, and validating does not change the directory. This is the
// property that lets it run on every attempt without a side effect to reason about.
func TestAutoIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "check:\n\t@true\n")
	writeFile(t, dir, ".motita/anchor", "echo stable\n")

	before := snapshotDir(t, dir)
	first := autoAnchor(dir).detectChecks()
	second := autoAnchor(dir).detectChecks()
	if len(first) != len(second) || first[0].Name != second[0].Name {
		t.Fatalf("detection is not stable: %+v vs %+v", first, second)
	}
	// Validate must not write anything either.
	autoAnchor(dir).Validate(context.Background())
	after := snapshotDir(t, dir)
	if before != after {
		t.Errorf("detection or validation changed the directory:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestAutoUsesTheConfiguredTimeoutWhenGiven: the configured timeout still applies,
// so a project whose gate is slow can raise it instead of being killed at the
// default.
func TestAutoUsesTheConfiguredTimeoutWhenGiven(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".motita/anchor", "echo x\n")

	cfg := autoCfg()
	cfg.Timeout = 45 * time.Second
	checks := New(cfg, dir, nil).detectChecks()
	if len(checks) != 1 || checks[0].Timeout != 45*time.Second {
		t.Fatalf("the configured timeout should apply, got %+v", checks)
	}
}

// TestAutoDefaultTimeoutAllowsARealGate: a whole test suite plus a build takes
// longer than the 120s an explicit check gets, and a gate killed at 120s is reported
// as a broken one - which is how a validator teaches its user to stop believing it.
func TestAutoDefaultTimeoutAllowsARealGate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".motita/anchor", "echo x\n")

	checks := autoAnchor(dir).detectChecks()
	if checks[0].Timeout < 5*time.Minute {
		t.Errorf("the default auto timeout must fit a real gate, got %s", checks[0].Timeout)
	}
}

// TestAutoUnreadableFileIsNotFatal: an unreadable file means this convention does not
// speak for the project, and detection moves on. Reporting an error would make one
// bad file a reason the agent could not work at all.
func TestAutoUnreadableFileIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	// A DIRECTORY where package.json is expected: reading it fails, and detection
	// must still find the Go gate behind it.
	if err := os.MkdirAll(filepath.Join(dir, "package.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "go.mod", "module example.com/x\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 || checks[0].Command != "go" {
		t.Fatalf("an unreadable file must not stop detection, got %+v", checks)
	}
}

// TestAutoRejectsAMalformedPackageJSON: a package.json that does not parse cannot
// declare scripts, so no check is proposed from it.
func TestAutoRejectsAMalformedPackageJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{not json")

	if checks := autoAnchor(dir).detectChecks(); len(checks) != 0 {
		t.Fatalf("a malformed package.json must not produce checks, got %+v", checks)
	}
}

// TestAutoDetectsSetupPy: the loosest declaration still counts.
func TestAutoDetectsSetupPy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "setup.py", "from setuptools import setup\n")

	checks := autoAnchor(dir).detectChecks()
	if len(checks) != 1 || checks[0].Command != "pytest" {
		t.Fatalf("expected pytest from setup.py, got %+v", checks)
	}
}

// snapshotDir renders the files under dir with their sizes, so a write during
// detection shows up as a difference.
func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if info.IsDir() {
			sb.WriteString(rel + "/\n")
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sb.WriteString(rel + " " + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// Planned is what the model is TOLD it will be measured against: the commands Validate would run,
// without running them. With no gate it is empty, which is the signal to say so.
func TestPlannedNamesTheGateWithoutRunningIt(t *testing.T) {
	dir := t.TempDir()
	if got := autoAnchor(dir).Planned(); len(got) != 0 {
		t.Fatalf("a project with no gate plans nothing, got %+v", got)
	}
	writeFile(t, dir, "package.json", `{"scripts":{"lint":"eslint .","test":"vitest run"}}`)
	got := autoAnchor(dir).Planned()
	if len(got) != 2 || got[0].Command != "npm" || strings.Join(got[0].Args, " ") != "run lint" || got[1].Name != "npm test" {
		t.Fatalf("planned = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err == nil {
		t.Error("planning must not run anything")
	}
}

// TestGateFingerprint: the fingerprint changes with what defines the gate - a new .motita/anchor,
// a Makefile's recipe, package.json's scripts - and with nothing else: not a dependency added to
// package.json, not the directory the same tree is in. A configured anchor has none.
func TestGateFingerprint(t *testing.T) {
	dir := t.TempDir()
	fp := func() string { return autoAnchor(dir).GateFingerprint() }
	writeFile(t, dir, "Makefile", "check:\n\tgo test ./...\n")
	writeFile(t, dir, "package.json", `{"scripts":{"test":"jest"},"dependencies":{"a":"1"}}`)
	start := fp()
	if start == "" || fp() != start {
		t.Fatalf("the fingerprint must be stable: %q", start)
	}
	other := t.TempDir()
	writeFile(t, other, "Makefile", "check:\n\tgo test ./...\n")
	writeFile(t, other, "package.json", `{"scripts":{"test":"jest"},"dependencies":{"a":"1"}}`)
	if autoAnchor(other).GateFingerprint() != start {
		t.Error("the same tree in another directory must read the same")
	}

	writeFile(t, dir, "package.json", `{"scripts":{"test":"jest"},"dependencies":{"a":"2","b":"1"}}`)
	if fp() != start {
		t.Error("a dependency change is not a gate change")
	}
	for _, change := range []struct{ name, body string }{
		{"package.json", `{"scripts":{"test":"exit 0"}}`},
		{"package.json", `not json`},
		{"Makefile", "check:\n\ttrue\n"},
		{".motita/anchor", "true\n"},
	} {
		before := fp()
		writeFile(t, dir, change.name, change.body)
		if fp() == before {
			t.Errorf("%s = %q must change the fingerprint", change.name, change.body)
		}
	}
	if New(config.Anchor{Kind: "command", Command: "true"}, dir, nil).GateFingerprint() != "" {
		t.Error("a configured anchor has no fingerprint")
	}
}

package agent

// FINISHING A REQUEST, on any project.
//
// Reported from a real session (a request to add a "new record" section to an admin panel of a
// Next.js project): 14 rounds, ~5 minutes, and the run closed as COMPLETE having written no file.
// The log shows the four defects below, each of which is pinned by the tests in this file.
//
//  1. Thirteen rounds of reading and no writing, then "done" - and the anchor PASSED, because the
//     project's own lint, typecheck and tests are green on a tree nobody touched.
//  2. The same files were read again and again under different spellings (`cat -n f`,
//     `sed -n '120,760p' f`, `cat f`): the memory recognised only the identical text, and dropped
//     every kept read whenever a command merely LOOKED like a write.
//  3. A read-only `for ... do head ...; done` was put to the user as a question, twice, on a run
//     with nobody at the keyboard (85 s and 14 s stalled).
//  4. The first gate refusal was "npm: command not found" - a missing node_modules, nothing to do
//     with the change - and it was charged to max_retries.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/config"
)

// --- 1. a claim of "done" over a tree nobody touched -----------------------------------------

// TestDoneAfterOnlyReadingIsQuestionedOnce: the model reads for several rounds and claims done.
// It is told, in words, that no file changed - once - and the anchor is not spent on that.
func TestDoneAfterOnlyReadingIsQuestionedOnce(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		switch {
		case round <= 3:
			return step(false, fmt.Sprintf("echo reading-%d", round))
		case round == 4:
			return step(true) // claims done having written nothing
		case round == 5:
			return step(false, "printf 'export const x = 1\\n' > feature.ts")
		}
		return step(true)
	}}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if !strings.Contains(s.executes[4], "NO FILE in the working directory has changed") {
		t.Errorf("the round after an unbacked claim must be told nothing changed:\n%s", s.executes[4])
	}
	if result.Attempts != 6 {
		t.Errorf("rounds = %d, want 6 (3 reads, the questioned claim, the write, the real claim)", result.Attempts)
	}
}

// TestDoneAfterOnlyReadingIsAcceptedWhenNothingHadToChange: an investigation legitimately changes
// nothing. The model says so by claiming done again, and that claim goes through: the guard asks,
// it does not forbid.
func TestDoneAfterOnlyReadingIsAcceptedWhenNothingHadToChange(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round <= 3 {
			return step(false, fmt.Sprintf("echo looking-%d", round))
		}
		return step(true)
	}}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("an answer-only task must still finish: err=%v reason=%s", err, result.Reason)
	}
	if result.Attempts != 5 {
		t.Errorf("rounds = %d, want 5 (3 reads, one questioned claim, the confirmed claim)", result.Attempts)
	}
}

// TestAQuickDoneIsNotSecondGuessed: a run that claims done straight away is not asked anything.
func TestAQuickDoneIsNotSecondGuessed(t *testing.T) {
	s := &scriptServer{execute: func(int, string) string { return step(true) }}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass || result.Attempts != 1 {
		t.Fatalf("err=%v pass=%v rounds=%d", err, result.Pass, result.Attempts)
	}
}

// TestALongReadingStreakIsToldToWrite: after readOnlyWarnAt rounds with no file changed, the next
// round's prompt says how long it has been reading.
func TestALongReadingStreakIsToldToWrite(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		if round <= readOnlyWarnAt+1 {
			return step(false, fmt.Sprintf("echo step-%d", round))
		}
		return step(true)
	}}
	if _, _, err := runScript(t, s, "exit 0", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.executes[readOnlyWarnAt-1], "have only READ") {
		t.Errorf("the warning came too early:\n%s", s.executes[readOnlyWarnAt-1])
	}
	if !strings.Contains(s.executes[readOnlyWarnAt], "rounds in a row have only READ") {
		t.Errorf("after %d reading rounds the prompt must say so:\n%s", readOnlyWarnAt, s.executes[readOnlyWarnAt])
	}
}

// TestAWriteResetsTheReadingStreak: a round that changes a source file starts the count again.
func TestAWriteResetsTheReadingStreak(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		switch {
		case round == readOnlyWarnAt:
			return step(false, "printf x > src.ts")
		case round <= readOnlyWarnAt+2:
			return step(false, fmt.Sprintf("echo r%d", round))
		}
		return step(true)
	}}
	if _, _, err := runScript(t, s, "exit 0", nil); err != nil {
		t.Fatal(err)
	}
	for i, p := range s.executes {
		if strings.Contains(p, "have only READ") {
			t.Errorf("round %d was warned although a file changed at round %d", i+1, readOnlyWarnAt)
		}
	}
}

// --- 2. the memory recognises what it already holds ------------------------------------------

func TestFileReadRecognisesPlainReads(t *testing.T) {
	cases := []struct {
		cmd      string
		path     string
		from, to int
		ok       bool
	}{
		{"cat a/b.ts", "a/b.ts", 1, wholeFile, true},
		{"cat -n ./a/b.ts", "a/b.ts", 1, wholeFile, true},
		{"cat -n 'app/api/admin/[...path]/route.js'", "", 0, 0, false}, // glob characters: not a plain path
		{"sed -n '120,760p' f.tsx", "f.tsx", 120, 760, true},
		{"sed -n 5p f", "f", 5, 5, true},
		{"head -70 f", "f", 1, 70, true},
		{"head -n 20 f", "f", 1, 20, true},
		{"head f", "f", 1, 10, true},
		{"cat a b", "", 0, 0, false},
		{"cat a | wc -l", "", 0, 0, false},
		{"sed -i 's/a/b/' f", "", 0, 0, false},
		{"sed -n 9,3p f", "", 0, 0, false},
		{"grep -n x f", "", 0, 0, false},
		{"cat", "", 0, 0, false},
		{"head -n x f", "", 0, 0, false},
		{"head -0 f", "", 0, 0, false},
		{"cat -x f", "", 0, 0, false},
		{"head -n 5 a b", "", 0, 0, false},   // two files
		{"sed -n 1,5p a b", "", 0, 0, false}, // two files
		{"sed -n 1,5d f", "", 0, 0, false},   // not a print
		{"sed -n xp f", "", 0, 0, false},     // not a line number
		{"sed -n 0,5p f", "", 0, 0, false},   // lines start at 1
	}
	for _, c := range cases {
		path, from, to, ok := fileRead(c.cmd)
		if ok != c.ok || path != c.path || (ok && (from != c.from || to != c.to)) {
			t.Errorf("fileRead(%q) = %q %d %d %v, want %q %d %d %v", c.cmd, path, from, to, ok, c.path, c.from, c.to, c.ok)
		}
	}
}

// TestADifferentSpellingOfAHeldReadIsAnsweredFromMemory: `cat -n f` was read; then
// `sed -n '10,50p' f` and `cat f` ask for what is already held.
func TestADifferentSpellingOfAHeldReadIsAnsweredFromMemory(t *testing.T) {
	var m workMemory
	m.remember("cat -n src/app.tsx", 2, "1\ta\n2\tb\n3\tc\n")
	for _, cmd := range []string{"cat src/app.tsx", "sed -n '2,3p' src/app.tsx", "head -2 ./src/app.tsx", "cat  -n   src/app.tsx"} {
		if _, ok := m.recall(cmd); !ok {
			t.Errorf("%q asks for what is already held and must be answered from memory", cmd)
		}
	}
	for _, cmd := range []string{"cat src/other.tsx", "grep -n a src/app.tsx", "sed -n '2,3p' src/other.tsx"} {
		if _, ok := m.recall(cmd); ok {
			t.Errorf("%q is not held and must run", cmd)
		}
	}
}

// TestARangeDoesNotAnswerAWiderRange: `sed -n '1,100p'` of a long file says nothing about line 300.
func TestARangeDoesNotAnswerAWiderRange(t *testing.T) {
	var m workMemory
	long := strings.Repeat("line\n", 100)
	m.remember("sed -n '1,100p' big.go", 1, long)
	if _, ok := m.recall("sed -n '50,90p' big.go"); !ok {
		t.Error("lines 50-90 are inside 1-100")
	}
	for _, cmd := range []string{"sed -n '90,300p' big.go", "cat big.go"} {
		if _, ok := m.recall(cmd); ok {
			t.Errorf("%q reaches past what was read and must run", cmd)
		}
	}
}

// TestAShortRangeThatHitTheEndOfTheFileHoldsTheWholeFile: `sed -n '1,400p'` on a 3-line file got
// everything, so a later `cat f` is answered.
func TestAShortRangeThatHitTheEndOfTheFileHoldsTheWholeFile(t *testing.T) {
	var m workMemory
	m.remember("sed -n '1,400p' small.go", 1, "a\nb\nc\n")
	if _, ok := m.recall("cat small.go"); !ok {
		t.Error("the range returned fewer lines than asked for, so it reached the end of the file")
	}
}

// TestAReadThePromptNoLongerShowsIsNotAnsweredFromMemory: the memory is capped; a read that was
// pushed out of the prompt must be run again, not promised to the model as "already read".
func TestAReadThePromptNoLongerShowsIsNotAnsweredFromMemory(t *testing.T) {
	var m workMemory
	m.remember("cat old.txt", 1, "OLD")
	for i := 0; i < 5; i++ {
		m.remember(fmt.Sprintf("cat f%d", i), 2+i, strings.Repeat("x", evidenceEach))
	}
	if _, ok := m.recall("cat old.txt"); ok {
		t.Error("the oldest read no longer fits in the prompt and must not be promised")
	}
	if _, ok := m.recall("cat f4"); !ok {
		t.Error("the newest read is shown and must be answered")
	}
}

// TestSyncKeepsReadsWhenNothingChanged and TestSyncDropsReadsWhenAFileChanged: the reads are
// dropped because the FILES differ, not because a command looked like it could write.
func TestSyncKeepsReadsWhenNothingChanged(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644)
	var m workMemory
	m.sync(dir) // first look: nothing to compare with, reads are dropped (there are none)
	m.remember("cat a.txt", 1, "one")
	if changed := m.sync(dir); changed {
		t.Error("nothing changed, the reads must be kept")
	}
	if _, ok := m.recall("cat a.txt"); !ok {
		t.Error("the read was dropped although no file changed")
	}
}

func TestSyncDropsReadsWhenAFileChanged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("one"), 0o644)
	var m workMemory
	m.sync(dir)
	m.remember("cat a.txt", 1, "one")
	os.WriteFile(p, []byte("changed!"), 0o644)
	if !m.sync(dir) {
		t.Error("a file changed, the reads must be dropped")
	}
	if _, ok := m.recall("cat a.txt"); ok {
		t.Error("a stale read survived a change")
	}
}

func TestSyncWithoutATreeDropsTheReads(t *testing.T) {
	var m workMemory
	m.remember("cat a", 1, "x")
	if !m.sync("") {
		t.Error("with no tree to look at the reads cannot be trusted")
	}
}

// --- fingerprint ----------------------------------------------------------------------------

func TestTreeFingerprintSeesSourcesAndIgnoresToolOutput(t *testing.T) {
	dir := t.TempDir()
	must := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("src/a.ts", "a")
	before, ok := treeFingerprint(dir, true)
	if !ok {
		t.Fatal("no fingerprint")
	}
	// What an install or a build writes is not the user's request.
	must("node_modules/x/index.js", "dep")
	must("package-lock.json", "{}")
	must("dist/bundle.js", "out")
	must(".git/HEAD", "ref")
	if now, _ := treeFingerprint(dir, true); now != before {
		t.Error("tool output changed the sources fingerprint")
	}
	// ...but for the reads, a lock file and a build output are files a model may read.
	if a, _ := treeFingerprint(dir, false); a == before {
		t.Error("the reads fingerprint must see the lock file and the build output")
	}
	must("src/b.ts", "new")
	if now, _ := treeFingerprint(dir, true); now == before {
		t.Error("a new source file must change the fingerprint")
	}
}

func TestTreeFingerprintRefusesWhatItCannotRead(t *testing.T) {
	if _, ok := treeFingerprint("", true); ok {
		t.Error("no directory, no fingerprint")
	}
	if _, ok := treeFingerprint(filepath.Join(t.TempDir(), "missing"), true); ok {
		t.Error("a missing directory has no fingerprint")
	}
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, ok := treeFingerprint(f, true); ok {
		t.Error("a file is not a tree")
	}
}

// --- 4. a missing tool is not a failure of the change ----------------------------------------

func TestMissingToolingIsRecognisedOnlyWhenEveryFailureIsOne(t *testing.T) {
	missing := anchor.CheckLog{Name: "npm test", Pass: false, Output: "sh: 1: vitest: not found"}
	real := anchor.CheckLog{Name: "npm lint", Pass: false, Output: "src/a.ts:3 error no-unused-vars"}
	green := anchor.CheckLog{Name: "npm build", Pass: true}
	if !missingTooling(anchor.Result{Checks: []anchor.CheckLog{missing, green}}) {
		t.Error("a check that failed only because a tool is missing must be recognised")
	}
	if missingTooling(anchor.Result{Checks: []anchor.CheckLog{missing, real}}) {
		t.Error("one genuine failure makes the refusal about the code")
	}
	if missingTooling(anchor.Result{Checks: []anchor.CheckLog{green}}) || missingTooling(anchor.Result{}) {
		t.Error("nothing failed, so nothing is missing")
	}
	for _, out := range []string{"Error: Cannot find module 'zod'", "ModuleNotFoundError: No module named 'x'",
		"bash: eslint: command not found", "exec: \"cargo\": executable file not found in $PATH"} {
		if !missingTooling(anchor.Result{Checks: []anchor.CheckLog{{Pass: false, Output: out}}}) {
			t.Errorf("%q must be recognised as a missing tool", out)
		}
	}
	if !missingTooling(anchor.Result{Checks: []anchor.CheckLog{{Pass: false, Error: "exec: not found"}}}) {
		t.Error("the error text counts too")
	}
}

// TestAMissingToolIsNotChargedToTheRetries: the gate fails twice for a missing tool, then the tool
// "appears" and it passes. With max_retries=1 that would have been fatal on the second refusal.
func TestAMissingToolIsNotChargedToTheRetries(t *testing.T) {
	// Round 1 and 2 claim done with the tool missing (two exempt refusals); round 3 installs it
	// in the same round it claims done, so the gate that follows passes.
	s := &scriptServer{execute: func(round int, _ string) string {
		if round <= 2 {
			return step(true, "true")
		}
		return step(true, "touch installed")
	}}
	script := `[ -f installed ] || { echo "sh: 1: vitest: not found"; exit 127; }`
	_, result, err := runScript(t, s, script, func(c *config.Config) { c.Agent.MaxRetries = 1 })
	if err != nil || !result.Pass {
		t.Fatalf("a missing tool must not exhaust the retries: err=%v reason=%s", err, result.Reason)
	}
	if !strings.Contains(s.executes[1], "NOT INSTALLED") {
		t.Errorf("the model must be told the refusal is about a missing tool:\n%s", s.executes[1])
	}
}

// TestAMissingToolThatNeverInstallsStillEnds: the exemption is bounded.
func TestAMissingToolThatNeverInstallsStillEnds(t *testing.T) {
	s := &scriptServer{execute: func(int, string) string { return step(true, "true") }}
	_, result, err := runScript(t, s, `echo "x: command not found"; exit 127`, func(c *config.Config) { c.Agent.MaxRetries = 1 })
	if err == nil || result.Pass {
		t.Fatalf("a tool that never appears must still end the run: err=%v pass=%v", err, result.Pass)
	}
	if result.Attempts > maxToolingRetries+2+1 {
		t.Errorf("rounds = %d: the exemption is not bounded", result.Attempts)
	}
}

// A tree over the cap has no fingerprint, and a directory the walk cannot enter does not make the
// fingerprint fail: what cannot be read is simply not part of it.
func TestTreeFingerprintCapAndUnreadableDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644)
	}
	defer func(old int) { fingerprintMaxFiles = old }(fingerprintMaxFiles)
	fingerprintMaxFiles = 2
	if _, ok := treeFingerprint(dir, true); ok {
		t.Error("a tree over the cap must have no fingerprint")
	}
	fingerprintMaxFiles = 60000

	locked := filepath.Join(dir, "locked")
	os.Mkdir(locked, 0o755)
	os.WriteFile(filepath.Join(locked, "x"), []byte("x"), 0o644)
	if err := os.Chmod(locked, 0); err != nil {
		t.Skip("cannot remove permissions here")
	}
	defer os.Chmod(locked, 0o755)
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("permissions are not enforced here (running as root?)")
	}
	if _, ok := treeFingerprint(dir, true); !ok {
		t.Error("a directory the walk cannot enter must not make the fingerprint fail")
	}
}

// --- the whole route, with the policy on and nobody to ask -----------------------------------

// TestARequestIsFinishedWithThePolicyOnAndNobodyToAsk replays the route of the real session with
// the production policy: read, write files with heredocs, install the dependencies, claim done.
// Every command below is ordinary work; none may stop the run on a question it cannot have
// answered (agent.approver is nil here, so an Ask is a refusal), and the run ends with the files
// on disk and a PASS.
func TestARequestIsFinishedWithThePolicyOnAndNobodyToAsk(t *testing.T) {
	s := &scriptServer{execute: func(round int, _ string) string {
		switch round {
		case 1:
			return step(false, "ls -la && for f in src/*.txt; do echo \"=== $f ===\"; head -20 \"$f\"; done")
		case 2:
			return step(false, "mkdir -p lib && cat > lib/feature.txt << 'EOF'\nexport const feature = 1\nline with $HOME and `ticks`\nEOF\n"+
				"cat > lib/feature.check.txt << 'EOF'\nfeature check\nEOF")
		case 3:
			return step(false, "(npm ci 2>&1 || npm install 2>&1) | tail -5; test -f lib/feature.txt && echo present")
		}
		return step(true)
	}}
	fx, result, err := runScript(t, s, "test -f lib/feature.txt && grep -q 'export const feature' lib/feature.txt", func(c *config.Config) {
		c.Agent.Policy = config.Policy{Enforce: true}
	})
	if err != nil || !result.Pass {
		t.Fatalf("err=%v pass=%v reason=%s", err, result.Pass, result.Reason)
	}
	got, rerr := os.ReadFile(filepath.Join(fx.dir, "lib", "feature.txt"))
	if rerr != nil || !strings.Contains(string(got), "line with $HOME and `ticks`") {
		t.Fatalf("the heredoc must write its body verbatim: %q (%v)", got, rerr)
	}
	for i, prompt := range s.executes {
		if strings.Contains(prompt, "needs your approval") || strings.Contains(prompt, "nobody to ask") {
			t.Errorf("round %d was stopped on a question nobody could answer:\n%s", i+1, prompt)
		}
	}
}

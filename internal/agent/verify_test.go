package agent

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

func TestVerificationKeysRecognisesChecksOnAnyProject(t *testing.T) {
	cases := map[string][]string{
		"npm test":                                    {"|npm test"},
		"npm run lint":                                {"|npm lint"},
		"npm run test:unit -- --run":                  {"|npm test:unit"},
		"cd server && npm test 2>&1 | tail -60":       {"server|npm test"},
		"npx vitest run components/a.test.tsx":        {"|vitest"},
		"npx --no-install tsc --noEmit":               {"|tsc"},
		"CI=1 pnpm exec jest":                         {"|jest"},
		"go test ./... -run X":                        {"|go test"},
		"cargo test -q":                               {"|cargo test"},
		"pytest -q tests/":                            {"|pytest"},
		"python3 -m pytest":                           {"|python3 -m pytest"},
		"make check":                                  {"|make check"},
		"node --test server/test/":                    {"|node --test"},
		"mvn -q test":                                 {"|mvn -q test"},
		"./gradlew test":                              {"|./gradlew test"},
		"bash scripts/run-tests.sh":                   {"|bash scripts/run-tests.sh"},
		"mkdir -p a && cat > a/b.ts << 'EOF'\nx\nEOF": nil,
		"ls -la && git status":                        nil,
		"sed -n '1,40p' package.json":                 nil,
		"npm install zod":                             nil,
		"npm ci":                                      nil,
		"cd a;; npm test ; ;":                         {"a|npm test"}, // empty segments are skipped
		"(cd a && npm test) && (cd b && npm test)":    {"a|npm test", "b|npm test"},
		// The real session: a search whose PATTERN names a runner is not a run of it.
		`cd /w && grep -rnE '"running: |running: ' internal | grep -v _test; grep -n '"test\|vitest\|testing-library' web/package.json`: nil,
		`grep -c "a|make check|b" Makefile`:                            nil,
		`echo "x" | grep -q "y\" | go test" || npm test`:               {"|npm test"},
		`printf %s \| && npm test`:                                     {"|npm test"},
		"echo 'unclosed | npm test":                                    nil,
		`echo "unclosed | npm test`:                                    nil,
		`echo "trailing\`:                                              nil,
		"cat > Makefile <<'EOF'\ncheck:\n\tmake test\nEOF\nmake check": {"|make check"},
		"cat > a.md <<-EOF\nnpm test\n\tEOF\nnpm run lint":             {"|npm lint"},
		"cat > a.md << \"END\"\nnpm test\nEND":                         nil,
		"cat > a.md <<EOF; true\nnpm test\nEOF\ngo test ./...":         {"|go test"},
		"cat > a.md <<'EOF\nnpm test":                                  nil, // an unclosed heredoc word starts no body
		"cat <<EOF\nnpm test":                                          nil, // a body with no terminator runs to the end
		"cat <<<'npm test' && echo $((1 << 2))\nnpm test":              {"|npm test"},
	}
	for cmd, want := range cases {
		got := verificationKeys(cmd)
		if len(got) != len(want) {
			t.Errorf("verificationKeys(%q) = %v, want %v", cmd, got, want)
			continue
		}
		for i := range want {
			// The key is "dir|phrase"; only the dir and the leading words are pinned.
			if !strings.HasPrefix(got[i], strings.SplitN(want[i], "|", 2)[0]+"|") {
				t.Errorf("verificationKeys(%q)[%d] = %q, want dir of %q", cmd, i, got[i], want[i])
			}
		}
	}
}

func TestCheckFailedSeesThroughAPipe(t *testing.T) {
	cases := []struct {
		name         string
		exit         int
		err          error
		out          string
		failed, mask bool
	}{
		{"plain pass", 0, nil, "Test Files  3 passed (3)\n Tests  12 passed (12)", false, false},
		{"zero failures is not a failure", 0, nil, "0 failed, 45 passed\nFound 0 errors.", false, false},
		{"non-zero exit", 1, nil, "boom", true, false},
		{"could not run", 0, errors.New("x"), "", true, false},
		{"node test runner", 0, nil, "# tests 45\n# pass 23\n# fail 22\nnot ok 3 - x", true, true},
		{"vitest", 0, nil, " Test Files  1 failed | 2 passed (3)", true, true},
		{"jest count", 0, nil, "Tests: 2 failed, 10 passed", true, true},
		{"go", 0, nil, "--- FAIL: TestX (0.00s)\nFAIL\tpkg", true, true},
		{"tsc", 0, nil, "a.ts(3,1): error TS2322: bad", true, true},
		{"module missing", 0, nil, "Error: Cannot find module 'x'", true, true},
		{"npm err", 0, nil, "npm ERR! code ELIFECYCLE", true, true},
		{"python", 0, nil, "Traceback (most recent call last):\n  File", true, true},
		{"command not found", 0, nil, "sh: 1: vitest: command not found", true, true},
		// dash, the /bin/sh of Debian and Ubuntu, says it shorter; behind `| tail` it was missed.
		{"dash not found", 0, nil, "sh: 1: node: not found", true, true},
		{"not found in prose", 0, nil, "page not found: 0 failed", false, false},
	}
	for _, c := range cases {
		f, m := checkFailed(c.exit, c.err, c.out)
		if f != c.failed || m != c.mask {
			t.Errorf("%s: failed=%v masked=%v, want %v %v", c.name, f, m, c.failed, c.mask)
		}
	}
}

func TestAPassClosesAFailureAndANewFailureReopensIt(t *testing.T) {
	m := &workMemory{}
	m.noteVerification("cd server && npm test 2>&1 | tail -60", 4, 0, nil, "# fail 22")
	m.noteVerification("npm run lint", 4, 0, nil, "ok")
	if open := m.openFailures(); len(open) != 1 || open[0].command != "cd server && npm test 2>&1 | tail -60" || !open[0].masked {
		t.Fatalf("open = %+v", open)
	}
	if got := m.failedInRound(4); len(got) != 1 {
		t.Errorf("failedInRound(4) = %+v", got)
	}
	if got := m.failedInRound(5); len(got) != 0 {
		t.Errorf("failedInRound(5) = %+v", got)
	}
	// A pass of the SAME check in the same directory closes it; a pass elsewhere does not.
	m.noteVerification("npm test", 5, 0, nil, "ok")
	if len(m.openFailures()) != 1 {
		t.Error("a pass in another directory must not close the failure")
	}
	m.noteVerification("cd server && npm test", 6, 0, nil, "# pass 45")
	if len(m.openFailures()) != 0 {
		t.Errorf("a later pass must close it: %+v", m.openFailures())
	}
	m.noteVerification("cd server && npm test", 7, 1, nil, "boom")
	if len(m.openFailures()) != 1 {
		t.Error("a new failure must reopen it")
	}
	// A command that checks nothing records nothing, and a long tail is cut.
	m2 := &workMemory{}
	m2.noteVerification("ls -la", 1, 1, nil, "x")
	m2.noteVerification("npm test", 1, 1, nil, strings.Repeat("y", 5000))
	if len(m2.verif) != 1 || len(m2.openFailures()[0].tail) > verifyTailChars+5 {
		t.Errorf("verif = %d records", len(m2.verif))
	}
}

func TestOpenFailuresAreOneRecordPerCommandOldestFirst(t *testing.T) {
	m := &workMemory{}
	m.noteVerification("npm run lint && npm test", 3, 1, nil, "x") // one command, two keys
	m.noteVerification("go test ./...", 2, 1, nil, "y")
	got := m.openFailures()
	if len(got) != 2 || got[0].round != 2 || got[1].command != "npm run lint && npm test" {
		t.Errorf("openFailures = %+v", got)
	}
}

func TestTheNotesTellTheModelWhatAFailedCheckMeans(t *testing.T) {
	note := failedCheckNote([]verifRecord{{command: "cd server && npm test | tail", masked: true}, {command: "npx tsc"}})
	for _, want := range []string{"A CHECK FAILED", "cd server && npm test | tail", "PIPE's", "exit=$?", "information, not a stopping point",
		"WITHOUT your change"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	first := unresolvedCheckChallenge([]verifRecord{{command: "npm test", round: 4, exit: 1, tail: "a\nb", masked: true}}, 1)
	again := unresolvedCheckChallenge([]verifRecord{{command: "npm test", round: 4, exit: 1, tail: "a"}}, 2)
	for _, want := range []string{"STILL FAILING", "FIX it", "PROVE it is not yours", "CHANGE the way you check", "to demonstrate, not to assume", "    a\n    b", "PIPE's"} {
		if !strings.Contains(first, want) {
			t.Errorf("first challenge lacks %q:\n%s", want, first)
		}
	}
	if !strings.Contains(again, "AGAIN") || strings.Contains(again, "PIPE's") {
		t.Errorf("second challenge:\n%s", again)
	}
}

// --- the loop --------------------------------------------------------------------------------

// nodeTest writes a real node:test file. `node --test` is a check the loop recognises and a local
// tool the production policy allows, so the run below executes under the same rules as a real one.
// It fails while fixed.flag does not exist, with the count the runner prints.
const nodeTest = `printf 'const t=require("node:test");const fs=require("node:fs");\n` +
	`t("the feature works",()=>{ if(!fs.existsSync("fixed.flag")) throw new Error("not fixed"); });\n` +
	`t("another",()=>{});\n' > feature.test.js; `

// TestDoneOverAFailingCheckIsSentBackToIt replays the real session: the run writes the change, runs
// the tests through `| tail`, sees them fail, calls them pre-existing and claims done. The anchor
// would pass (it runs only the project's own gate). The run is sent back TWICE; the third claim,
// after it fixed the check, is the one that goes through.
func TestDoneOverAFailingCheckIsSentBackToIt(t *testing.T) {
	requireSandboxNode(t)
	run := "node --test feature.test.js 2>&1 | tail -30"
	s := &scriptServer{execute: func(round int, prompt string) string {
		switch round {
		case 1:
			return step(false, "printf 'export const a = 1\\n' > feature.ts", nodeTest+"true")
		case 2: // a failing check behind tail: its exit status is tail's
			return step(false, run)
		case 3:
			return step(true) // "they are pre-existing": no comparison, no fix
		case 4:
			return step(true) // claims again without doing anything about it
		case 5:
			return step(false, "echo fixed > fixed.flag", run)
		}
		return step(true)
	}}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if len(s.executes) != 6 {
		t.Fatalf("rounds = %d, want 6 (write, failing check, two claims sent back, the fix, the claim)", len(s.executes))
	}
	if !strings.Contains(s.executes[2], "A CHECK FAILED") || !strings.Contains(s.executes[2], "PIPE's") {
		t.Errorf("the round after the failing check must be told it failed, and why the status lied:\n%s", s.executes[2])
	}
	if !strings.Contains(s.executes[3], "STILL FAILING") || !strings.Contains(s.executes[3], "PROVE it is not yours") {
		t.Errorf("the first claim over a failing check must be challenged:\n%s", s.executes[3])
	}
	if !strings.Contains(s.executes[4], "AGAIN") {
		t.Errorf("the second claim must be challenged harder:\n%s", s.executes[4])
	}
	// The fix round ran the check green, so the claim after it was not sent back: there are six
	// rounds and not seven. (The earlier challenges stay in the history the last prompt shows.)
	if result.Attempts != 6 {
		t.Errorf("once the check passes the claim goes through: rounds = %d", result.Attempts)
	}
}

// TestAChallengedRunThatCannotFixTheCheckStillEnds: the challenge is bounded. A run that truly
// cannot fix a check (a service that is not here) is sent back maxVerifyChallenges times and the
// next claim goes to the anchor, instead of looping for ever.
func TestAChallengedRunThatCannotFixTheCheckStillEnds(t *testing.T) {
	needSandboxTool(t, "node")
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(false, nodeTest+"node --test feature.test.js 2>&1 | tail -30")
		}
		return step(true)
	}}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass {
		t.Fatalf("err=%v reason=%s", err, result.Reason)
	}
	if want := 1 + maxVerifyChallenges + 1; result.Attempts != want {
		t.Errorf("rounds = %d, want %d (the check, %d sent back, the claim that goes through)", result.Attempts, want, maxVerifyChallenges)
	}
}

// TestAPassingCheckIsNotChallenged: a run whose checks are green is not slowed down at all.
func TestAPassingCheckIsNotChallenged(t *testing.T) {
	requireSandboxNode(t)
	s := &scriptServer{execute: func(round int, _ string) string {
		if round == 1 {
			return step(false, nodeTest+"echo fixed > fixed.flag", "node --test feature.test.js 2>&1 | tail -30")
		}
		return step(true)
	}}
	_, result, err := runScript(t, s, "exit 0", nil)
	if err != nil || !result.Pass || result.Attempts != 2 {
		t.Fatalf("err=%v pass=%v rounds=%d reason=%s", err, result.Pass, result.Attempts, result.Reason)
	}
	for i, p := range s.executes {
		if strings.Contains(p, "STILL FAILING") {
			t.Errorf("round %d was challenged over a green check", i+1)
		}
	}
}

// TestThePromptNamesTheGateThatWillDecide: with anchor.kind=auto the model used to be told that no
// validation was configured, about a project whose gate was npm lint/typecheck/test.
func TestThePromptNamesTheGateThatWillDecide(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	auto := config.Anchor{Kind: "auto"}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"lint":"eslint .","typecheck":"tsc --noEmit","test":"vitest run"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e := mount(t, srv, auto, func(c *config.Config) { c.Agent.WorkspaceDir = dir })
	rules := e.agent.describeRules()
	for _, want := range []string{"npm run lint", "npm run typecheck", "npm run test", "BEFORE claiming done", "must pass"} {
		if !strings.Contains(rules, want) {
			t.Errorf("rules lack %q:\n%s", want, rules)
		}
	}

	empty := t.TempDir()
	e2 := mount(t, srv, auto, func(c *config.Config) { c.Agent.WorkspaceDir = empty })
	if r := e2.agent.describeRules(); !strings.Contains(r, "declares NO gate") || !strings.Contains(r, ".motita/anchor") {
		t.Errorf("a project without a gate must be told how to declare one:\n%s", r)
	}
}

// needSandboxTool skips a test that runs a real tool the way the sandbox finds it: by the system
// PATH, not the one of the process running the tests. On a machine without it the policy now asks
// before running the missing program, which is the behaviour under test elsewhere.
func needSandboxTool(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		found := false
		for _, dir := range []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
				found = true
				break
			}
		}
		if !found {
			t.Skipf("%s is not on the sandbox PATH of this machine", name)
		}
	}
}

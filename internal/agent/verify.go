package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// THE CHECKS THE RUN RAN ITSELF, and whether it is allowed to walk away from one that failed.
//
// Reported from a real session: a run added an endpoint and its screen, ran the server's tests,
// saw 22 of 45 fail, decided the failures were "pre-existing" without comparing, never tested the
// endpoint it had written, and claimed done. Every check in that run was piped into `| tail`, so
// the exit status the loop saw was tail's; and the anchor, which only runs what the project
// declares, was green. Nothing in the loop knew a check had failed and stayed failed.
//
// A failing check is information, not a stopping point. This file lets the loop know which checks
// the run executed, which of them last failed, and so ask - before a claim of done is taken to the
// anchor - what the run did about it.

const (
	// maxVerifyChallenges bounds how many times a claim of done is sent back over a failed check
	// in one run. A run that really cannot fix one (a service that is not here) still ends: the
	// fourth claim goes through, and what was left unproven is in the final report.
	maxVerifyChallenges = 3
	// verifyTailChars is how much of a failing check's output is handed back to the model.
	verifyTailChars = 1500
)

// verifyPhrases are the commands that CHECK something, whatever the project: a test runner, a
// linter, a type checker, a build. They are matched on the start of one shell segment, after the
// wrappers (`npx`, `pnpm exec`, `time`, `VAR=x`) are removed.
var verifyPhrases = []*regexp.Regexp{
	regexp.MustCompile(`^(npm|yarn|pnpm|bun)( run| run-script)? (test|lint|typecheck|type-check|check|build|e2e|verify)(:\S+)?\b`),
	regexp.MustCompile(`^(vitest|jest|mocha|ava|tap|playwright test|cypress run|tsc|eslint|biome (check|lint|ci)|prettier (--check|-c)|svelte-check|vue-tsc|next (build|lint)|vite build|nuxt (build|typecheck)|ng (test|lint|build)|stylelint|knip)\b`),
	regexp.MustCompile(`^(go (test|vet|build)|cargo (test|check|clippy|build|nextest)|dotnet (test|build)|deno (test|lint|check)|mvn (-\S+ )*(test|verify|package)|(\./)?gradlew? (test|check|build)|ctest|zig (build|test))\b`),
	regexp.MustCompile(`^(pytest|py\.test|tox|nox|mypy|ruff( check)?|flake8|pylint|pyright|black --check|python3? -m (pytest|unittest|mypy|ruff)|uv run (pytest|mypy|ruff)|poetry run (pytest|mypy|ruff))\b`),
	regexp.MustCompile(`^(make|gmake|ninja|cmake --build) ?(test|tests|check|lint|build|verify|ci|all)?\b`),
	regexp.MustCompile(`^(node|bun|deno) --test\b`),
	regexp.MustCompile(`^(bundle exec )?(rspec|rake (test|spec))\b`),
	regexp.MustCompile(`^(phpunit|composer (test|check)|vendor/bin/phpunit)\b`),
	regexp.MustCompile(`^(sh|bash) \S*(test|check|verify|lint)\S*\.sh\b`),
}

// verifyWrappers are dropped from the front of a segment to reach the command itself.
var verifyWrappers = regexp.MustCompile(`^((npx|pnpm exec|pnpm dlx|yarn dlx|bunx|time|env|nice|timeout \d+\S*)( --no-install| -y| --yes)? +|[A-Za-z_][A-Za-z0-9_]*=\S* +)+`)

// verifyFailure is what a failing check prints, in the runners that print one. It is read from the
// OUTPUT because the exit status cannot be trusted: a check piped into `tail` exits with tail's
// status. It asks for a NON-ZERO count, so "0 failed" and "Found 0 errors" do not match.
var verifyFailure = regexp.MustCompile(`(?im)(^\s*(not ok\b|FAIL\b|FAILED\b|✗|✖|×)|^--- FAIL|\b[1-9]\d*\s+(failed|failing|errors?)\b|#\s*fail\s+[1-9]|npm ERR!|error TS\d+|Test Files\s+.*\b[1-9]\d*\s+failed|^panic:|Traceback \(most recent call last\)|AssertionError|Assertion failed|^E\s{2,}|Cannot find module|command not found)`)

// verifRecord is the last run of one check.
type verifRecord struct {
	command string
	round   int
	failed  bool
	// masked is set when the output says it failed but the exit status says it did not: the status
	// was a pipe's, not the check's.
	masked bool
	exit   int
	tail   string
}

// verificationKeys returns one key per check a command line runs: the directory it runs in and
// the check's own words (`server|npm test`). A line that checks nothing returns nothing. The
// directory is part of the key because `cd server && npm test` and `npm test` are two checks, and
// a pass of one must not close the failure of the other.
func verificationKeys(command string) []string {
	var keys []string
	cwd := ""
	seen := map[string]bool{}
	for _, seg := range splitSegments(command) {
		seg = strings.TrimSpace(strings.Trim(strings.TrimSpace(seg), "()"))
		if seg == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(seg, "cd "); ok {
			cwd = strings.Trim(strings.TrimSpace(rest), `'"`)
			continue
		}
		seg = strings.ToLower(seg)
		seg = verifyWrappers.ReplaceAllString(seg, "")
		for _, re := range verifyPhrases {
			if m := re.FindString(seg); m != "" {
				// `npm run lint` and `npm lint` are the same check: only the script-runner words are
				// dropped, and only as a whole word, so `run-tests.sh` keeps its name.
				words := strings.Fields(m)
				kept := words[:0]
				for _, w := range words {
					if w != "run" && w != "run-script" {
						kept = append(kept, w)
					}
				}
				k := cwd + "|" + strings.Join(kept, " ")
				if !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
				break
			}
		}
	}
	return keys
}

// splitSegments cuts a command line at the separators of the shell: `&&`, `||`, `;`, `|` and
// newlines. Quotes are not tracked: a separator inside a quoted word only yields a segment that
// matches no check.
func splitSegments(command string) []string {
	r := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	return strings.Split(r.Replace(command), "\n")
}

// checkFailed decides whether one execution of a check failed: a non-zero status, a run that could
// not start, or an output that reports a failure. The last is what sees through `| tail`.
func checkFailed(exit int, runErr error, output string) (failed, masked bool) {
	if exit != 0 || runErr != nil {
		return true, false
	}
	if verifyFailure.MatchString(output) {
		return true, true
	}
	return false, false
}

// noteVerification records one execution. A later run of the same check replaces the earlier one,
// so a pass closes a failure and a new failure reopens it.
func (m *workMemory) noteVerification(command string, round, exit int, runErr error, output string) {
	keys := verificationKeys(command)
	if len(keys) == 0 {
		return
	}
	failed, masked := checkFailed(exit, runErr, output)
	if m.verif == nil {
		m.verif = map[string]verifRecord{}
	}
	tail := strings.TrimSpace(output)
	if len(tail) > verifyTailChars {
		tail = "..." + tail[len(tail)-verifyTailChars:]
	}
	for _, k := range keys {
		m.verif[k] = verifRecord{command: command, round: round, failed: failed, masked: masked, exit: exit, tail: tail}
	}
}

// openFailures are the checks whose LAST run failed, oldest first, one record per command.
func (m *workMemory) openFailures() []verifRecord {
	var out []verifRecord
	seen := map[string]bool{}
	keys := make([]string, 0, len(m.verif))
	for k := range m.verif {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return m.verif[keys[i]].round < m.verif[keys[j]].round || (m.verif[keys[i]].round == m.verif[keys[j]].round && keys[i] < keys[j])
	})
	for _, k := range keys {
		r := m.verif[k]
		if r.failed && !seen[r.command] {
			seen[r.command] = true
			out = append(out, r)
		}
	}
	return out
}

// failedInRound are the checks that failed in this very round, for the note the model reads next.
func (m *workMemory) failedInRound(round int) []verifRecord {
	var out []verifRecord
	for _, r := range m.openFailures() {
		if r.round == round {
			out = append(out, r)
		}
	}
	return out
}

// maskedHint tells the model its exit status was not the check's, and how to get one that is.
const maskedHint = "Its exit status was the PIPE's, not the check's (`| tail` and `| head` report their own). " +
	"Run it as `CMD > /tmp/check.out 2>&1; echo \"exit=$?\"; tail -40 /tmp/check.out` to see both."

// failedCheckNote is appended to a round in which a check failed and the run is going on: it says
// what the failure means and what to do, so the next round starts from the failure and not from a
// new idea.
func failedCheckNote(failed []verifRecord) string {
	var b strings.Builder
	for _, r := range failed {
		fmt.Fprintf(&b, "\n!! A CHECK FAILED: `%s`.", collapse(truncate(r.command, 160)))
		if r.masked {
			b.WriteString(" " + maskedHint)
		}
	}
	b.WriteString("\nA failing check is information, not a stopping point. Read the failure above, find its cause " +
		"(your change, or the environment) and fix it, then run the same check again. Do not move on to something " +
		"else, and do not call a failure \"pre-existing\" until you have run the same check WITHOUT your change and " +
		"compared the counts.")
	return b.String()
}

// unresolvedCheckChallenge is what a claim of done is answered with while a check the run itself
// ran is still failing. It is firmer the second time, and never charges the anchor or max_retries.
func unresolvedCheckChallenge(open []verifRecord, attempt int) string {
	var b strings.Builder
	if attempt == 1 {
		b.WriteString("You reported \"done\": true, but a check you ran yourself is STILL FAILING and no later run of it passed:\n")
	} else {
		b.WriteString("You reported \"done\": true AGAIN, and the same kind of check is still failing with nothing shown about it:\n")
	}
	for _, r := range open {
		fmt.Fprintf(&b, "- `%s` (round %d, exit %d)", collapse(truncate(r.command, 200)), r.round, r.exit)
		if r.masked {
			b.WriteString(" - " + maskedHint)
		}
		b.WriteString("\n  last output:\n" + indentLines(truncate(r.tail, verifyTailChars), "    ") + "\n")
	}
	b.WriteString("\nDo ONE of these in this round, then claim done again:\n" +
		"1. FIX it: read the failure, change what it names, run the check again until it passes.\n" +
		"2. PROVE it is not yours: run the SAME check with your change set aside (git stash, or a clean `git worktree` " +
		"of HEAD) and put both failure counts in \"notes\". Then verify YOUR change by a way the broken part cannot " +
		"affect (a test that does not need it, a direct call of the function or endpoint you wrote) and run that.\n" +
		"3. CHANGE the way you check. If this way fails for reasons unrelated to your code, find another way to the same " +
		"proof (a smaller test, an in-memory fake, a script that calls the code). Only after two different ways have " +
		"failed, say in \"notes\" exactly what is unproven and why.\n" +
		"\"It was already failing\" is a claim to demonstrate, not to assume.")
	return b.String()
}

// indentLines prefixes every line of s.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

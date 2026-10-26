package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A CHECK THAT COULD NOT RUN IS NOT A CHECK THAT PASSED.
//
// When the machine cannot run a project's check (the dependencies are not installed and there is no
// network to fetch them), the output says so: "command not found", "Failed opening required
// vendor/autoload.php", "curl error 6". That is a third state next to pass and fail, and it has to
// reach the person as "not validated" with its cause. This file tells it apart from a failure of
// the code, keeps it from being "fixed" with a stand-in (see fakecheck.go), and stamps it on the
// report if the run ends over it.

// blockedPattern is what a check prints when it could not run at all: a missing tool, missing
// dependencies, or a network that cannot fetch them.
var blockedPattern = regexp.MustCompile(`(?im)(command not found|executable file not found|` +
	`(^|\s)\S+: (not found|No such file or directory)$|Cannot find module|No module named|` +
	`vendor/autoload\.php|Could not open input file|curl error \d+|Could not resolve host|` +
	`Temporary failure in name resolution|getaddrinfo|ENOTFOUND|EAI_AGAIN|ECONNREFUSED|` +
	`Network is unreachable|Connection timed out|dial tcp .*: (i/o timeout|no such host)|` +
	`Unable to locate package|could not connect to server|"no new privileges" flag is set)`)

// installCommand is a dependency install, by package manager.
var installCommand = regexp.MustCompile(`^(composer|npm|pnpm|yarn|bun|pip3?|poetry|uv|bundle)\s+` +
	`(install|update|ci|i|add|sync)\b`)

// noteInstall records a dependency install. It is not a check, but one that failed because the
// registry cannot be reached is the reason every check after it cannot run, and it is not
// forgotten until the same package manager installs again.
func (m *workMemory) noteInstall(command string, round, exit int, runErr error, output string) {
	for _, seg := range splitSegments(command) {
		seg = verifyWrappers.ReplaceAllString(strings.ToLower(strings.TrimSpace(seg)), "")
		tool := installCommand.FindStringSubmatch(seg)
		if tool == nil {
			continue
		}
		if m.installs == nil {
			m.installs = map[string]verifRecord{}
		}
		failed, masked := checkFailed(exit, runErr, output)
		tail := strings.TrimSpace(output)
		if len(tail) > verifyTailChars {
			tail = "..." + tail[len(tail)-verifyTailChars:]
		}
		m.installs[tool[1]] = verifRecord{command: command, round: round, failed: failed, masked: masked,
			exit: exit, tail: tail}
	}
}

// blockedChecks are the checks, and the dependency installs, whose last run failed because they
// could not run.
func (m *workMemory) blockedChecks() []verifRecord {
	out := blockedOf(m.openFailures())
	tools := make([]string, 0, len(m.installs))
	for tool := range m.installs {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		if r := m.installs[tool]; r.failed {
			out = append(out, blockedOf([]verifRecord{r})...)
		}
	}
	return out
}

// blockedCause is the line of a check's output that says why it could not run.
func blockedCause(r verifRecord) string {
	if loc := blockedPattern.FindStringIndex(r.tail); loc != nil {
		start := strings.LastIndex(r.tail[:loc[0]], "\n") + 1
		end := strings.Index(r.tail[loc[1]:], "\n")
		if end < 0 {
			end = len(r.tail)
		} else {
			end += loc[1]
		}
		return truncate(collapse(r.tail[start:end]), 200)
	}
	return truncate(collapse(r.tail), 200)
}

// blockedOf keeps the records that could not run.
func blockedOf(in []verifRecord) []verifRecord {
	var out []verifRecord
	for _, r := range in {
		if blockedPattern.MatchString(r.tail) {
			out = append(out, r)
		}
	}
	return out
}

// blockedNote tells the model, over a claim of done, that a check could not run and what is and is
// not allowed about that.
func blockedNote(blocked []verifRecord) string {
	if len(blocked) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThese checks did NOT FAIL on your code: they COULD NOT RUN, which is a third state, " +
		"neither passed nor failed:\n")
	for _, r := range blocked {
		fmt.Fprintf(&b, "- `%s`: %s\n", collapse(truncate(r.command, 160)), blockedCause(r))
	}
	b.WriteString("Do not stand in for them: no executable that exits 0, no edit of a test/lint/build script, " +
		"no \"done\" resting on a check that never ran. Install what is missing the way the project does " +
		"(composer install, npm ci, pip install -r ...) and run the check again. If the environment will not " +
		"allow it (no network, a tool that cannot be installed), STOP trying to get around it: report " +
		"\"done\": true with \"notes\" that start with \"NOT VALIDATED:\", name the check and the cause, and " +
		"say what you did verify another way, if anything. The result is then \"not validated\", and that " +
		"is an honest result. Never present it as resolved because the commands and patches are \"prepared\": " +
		"if something must be changed outside this session (a service unit, a setting, a file you cannot " +
		"write), the notes name that exact change and where to make it, and say plainly that the work is " +
		"NOT applied.")
	return b.String()
}

// unvalidatedBlock is handed to the synthesis, so the answer says what could not be validated.
func unvalidatedBlock(blocked []verifRecord) string {
	if len(blocked) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## CHECKS THAT COULD NOT RUN (NOT VALIDATED)\n")
	for _, r := range blocked {
		fmt.Fprintf(&b, "- `%s` could not run: %s\n", collapse(truncate(r.command, 160)), blockedCause(r))
	}
	b.WriteString("Say in the summary that these were NOT validated and why. List each in \"verification\" " +
		"with result \"blocked\", and never as \"pass\". The status is \"partial\", not \"done\".\n")
	return b.String()
}

// markUnvalidated makes the report say what the run could not validate, whatever the model wrote:
// the status cannot be "done", and every check that could not run is listed as blocked.
func (r *Report) markUnvalidated(blocked []verifRecord) {
	if len(blocked) == 0 {
		return
	}
	if r.Status == "done" {
		r.Status = "partial"
	}
	for _, b := range blocked {
		if !r.relabelListed(b) {
			r.Verification = append(r.Verification, ReportCheck{
				Check: collapse(truncate(b.command, 160)), Result: "blocked",
				Evidence: "could not run: " + blockedCause(b),
			})
		}
	}
}

// relabelListed finds the entries of the report that name a blocked check and turns a pass or a
// skip over it into "blocked". It reports whether the report listed the check at all.
func (r *Report) relabelListed(b verifRecord) bool {
	listed := false
	command := strings.ToLower(b.command)
	short := strings.ToLower(truncate(collapse(b.command), 40))
	for i, v := range r.Verification {
		check := strings.ToLower(v.Check)
		if !strings.Contains(check, short) && !strings.Contains(command, check) {
			continue
		}
		listed = true
		if v.Result == "pass" || v.Result == "skipped" {
			r.Verification[i].Result = "blocked"
		}
	}
	return listed
}

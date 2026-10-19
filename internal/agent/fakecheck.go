package agent

import (
	"path"
	"regexp"
	"strings"
)

// NO FAKE CHECKS: a green result has to come from the check itself.
//
// Reported from a real test of a Laravel project: vendor/bin/phpunit was missing, so the run wrote
// a "phpunit" that only did exit(0) and pointed the composer "test" script at it. `composer test`
// then printed success without a single test having run, and the work was one step from being
// reported as validated. Making the exit status zero is not the job; the job is to learn whether
// the software works, and when the machine cannot tell (no dependencies, no network) the honest
// answer is "not validated", with the cause.
//
// This file refuses the two moves that fabricate a pass, before they touch the disk:
//
//   - writing into a directory the package manager owns (vendor/, node_modules/, a virtualenv's
//     bin/): that is where the project's tools come from, and they are installed, never authored;
//   - writing a check that cannot fail: an executable that only exits 0, or a test/lint/build
//     script of a manifest or Makefile that is `exit 0`, `true`, `:` or an `echo`.
//
// A refusal reaches the model like a policy refusal and says what to do instead.

// fakeCheckHelp ends every refusal: the way out of a missing tool is never a stand-in for it.
const fakeCheckHelp = " Install the project's real dependencies with its package manager and run its own " +
	"check (php artisan test, composer test, npm test, pytest, go test...). If that cannot run here " +
	"(no network, a missing tool), STOP and say the check could not run and why; the result is then " +
	"\"not validated\", never a pass."

// dependencyDirs are the directories a package manager fills.
var dependencyDirs = map[string]bool{
	"vendor": true, "node_modules": true, ".venv": true, "venv": true, "site-packages": true,
}

// stubBody is a whole file that does nothing but succeed, once comments, a shebang and an opening
// tag are removed: `exit(0);`, `exit 0`, `process.exit(0)`, `sys.exit(0)`, `true`.
var stubBody = regexp.MustCompile(`(?i)^(exit\s*\(?\s*0?\s*\)?|(process|sys|os|Deno)\.exit\s*\(\s*0?\s*\)|` +
	`return\s+0|true|:)\s*;?$`)

// manifests are the files whose scripts say how a project is checked.
var manifests = map[string]bool{
	"composer.json": true, "package.json": true, "makefile": true, "gnumakefile": true, "justfile": true,
	"taskfile.yml": true, "pyproject.toml": true, "tox.ini": true, "noxfile.py": true, "setup.cfg": true,
}

// stubScript is a check script whose command cannot fail: "test": "exit 0", "lint": ["true"].
var stubScript = regexp.MustCompile(`(?i)"(test|tests|check|lint|build|verify|typecheck|ci|phpunit)"\s*:\s*` +
	`\[?\s*"\s*(exit\s*0?|true|:|echo\b[^"]*|php\s+-r\s+['\\"]*\s*exit\s*\(\s*0?\s*\)[^"]*)\s*"`)

// stubTarget is a Makefile/justfile recipe whose only command cannot fail.
var stubTarget = regexp.MustCompile(`(?im)^(test|tests|check|lint|build|verify|ci)\s*:[^\n]*\n(?:[ \t]+[@-]*\s*` +
	`(exit\s*0?|true|:|echo\b[^\n]*)\s*(?:\n|$))`)

// shellWriteToDeps is a shell line that writes where the package manager installs.
var shellWriteToDeps = regexp.MustCompile(`(?i)(>>?|\btee\b(\s+-a)?|\bcp\b[^|;&]*|\bmv\b[^|;&]*|\bln\b[^|;&]*|` +
	`\bchmod\b[^|;&]*|\binstall\b[^|;&]*)\s*['"]?\S*(vendor/bin|node_modules/\.bin|\.venv/bin|venv/bin|vendor/autoload\.php)`)

// shellStubScript is a shell line that edits a manifest to make a check succeed without running.
var shellStubScript = regexp.MustCompile(`(?i)(composer\s+config\s+(--\S+\s+)*scripts\.\S+\s+['"]?(exit\s*0?|true|:)['"]?\s*$|` +
	`npm\s+pkg\s+set\s+scripts\.\S+=['"]?(exit\s*0?|true|:)['"]?\s*$|npm\s+set-script\s+\S+\s+['"]?(exit\s*0?|true|:)['"]?\s*$)`)

// fakeCheckFile says why a file written by write_file/edit_file is a fabricated check, or "".
// body is the whole new content for write_file and the replacement text for edit_file.
func fakeCheckFile(kind, rel, body string) string {
	clean := path.Clean(strings.ReplaceAll(strings.TrimSpace(rel), "\\", "/"))
	for _, seg := range strings.Split(clean, "/") {
		if dependencyDirs[seg] {
			return "writing into " + seg + "/ is refused: that directory is filled by the package " +
				"manager, and a file made by hand there is a stand-in for a tool, not the tool." + fakeCheckHelp
		}
	}
	if kind == "write_file" && isStubExecutable(body) {
		return clean + " would only exit 0: an executable that cannot fail is not a check, it hides that " +
			"nothing ran." + fakeCheckHelp
	}
	if manifests[strings.ToLower(path.Base(clean))] && (stubScript.MatchString(body) || stubTarget.MatchString(body)) {
		return "the check script written to " + clean + " cannot fail (exit 0, true, an echo): " +
			"a test, lint or build script that never runs the tool reports a pass that did not happen." +
			fakeCheckHelp
	}
	return ""
}

// isStubExecutable reports a file that does nothing but succeed.
func isStubExecutable(body string) bool {
	var code []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "", strings.HasPrefix(line, "#"), strings.HasPrefix(line, "//"),
			strings.HasPrefix(line, "<?php"), strings.HasPrefix(line, "set -"):
		default:
			code = append(code, line)
		}
	}
	return len(code) == 1 && stubBody.MatchString(code[0])
}

// fakeCheckCommand says why a shell line fabricates a check, or "".
func fakeCheckCommand(command string) string {
	for _, seg := range splitSegments(command) {
		seg = strings.TrimSpace(seg)
		if shellStubScript.MatchString(seg) {
			return "that line sets a check script to something that cannot fail." + fakeCheckHelp
		}
	}
	if shellWriteToDeps.MatchString(command) {
		return "that line writes into the directory the package manager fills (vendor/, node_modules/, " +
			"a virtualenv's bin/): a tool made by hand there is a stand-in for the real one." + fakeCheckHelp
	}
	// A heredoc or printf that rewrites a manifest, with a script that cannot fail in it.
	lower := strings.ToLower(command)
	for m := range manifests {
		if strings.Contains(lower, m) && (stubScript.MatchString(command) || stubTarget.MatchString(command)) &&
			(strings.Contains(command, ">") || strings.Contains(lower, "sed ") || strings.Contains(lower, "tee ")) {
			return "that line rewrites " + m + " with a check script that cannot fail." + fakeCheckHelp
		}
	}
	return ""
}

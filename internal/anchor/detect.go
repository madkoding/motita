package anchor

// Detection of the gate a project declares.
//
// A gate hardcoded into the configuration is a gate that is wrong for every other
// project: `npm run lint` says nothing in a Go repository, and `make check` says
// nothing in one with no Makefile. Somebody who works on several projects would have
// to edit the configuration each time they switch, and forgetting is not neutral -
// the check fails, the anchor refuses PASS, and the run is stuck with a task it
// cannot finish. That is the defect this file removes: the anchor reads the project
// it is standing in and runs the gate THAT project declares.
//
// It is agnostic in the only sense that matters: nothing here names a project, a
// path or a language that the project itself does not declare. A project that wants
// to be explicit drops a `.motita/anchor` file and no convention is consulted.
//
// It is idempotent by construction: detection only READS, so running it twice over
// one directory produces the same checks, and running the checks does not change the
// directory. Nothing is cached and nothing is written, which is also why it can run
// on every validation without a side effect to reason about.
//
// The order is: what the project SAYS first, then what its files IMPLY. The first
// convention that matches wins, so the gate stays bounded and predictable - a
// repository that is both Go and Node runs the umbrella its Makefile declares, not
// two suites nobody asked for.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
)

// declaredGateFile is where a project names its own gate, one command per line.
//
// It exists for the projects no convention covers, and it is the reason the
// detection is genuinely agnostic rather than a list of languages somebody has to
// extend: a project with an unusual build names it here instead of waiting for a
// release. Blank lines and lines starting with `#` are ignored, so the file can
// carry its own explanation.
const declaredGateFile = ".motita/anchor"

// autoCheckTimeout is how long a detected check gets when the configuration names
// no timeout.
//
// It is far longer than the 120s an explicit check gets, and the difference is
// measured rather than generous: a real gate is a whole test suite plus a build, and
// `make check` on the repository this was written in runs fmt-check, vet and the
// tests. A check killed at 120s reports a timeout, which the anchor reads as FAIL,
// so a working gate would be reported as a broken one - the exact way a validator
// teaches its user to stop believing it.
const autoCheckTimeout = 10 * time.Minute

// detectChecks returns the gate the project in the anchor's directory declares, or
// nothing when it declares none.
//
// An empty result is NOT a PASS and never becomes one: Validate turns it into a FAIL
// that names what was looked for. The alternative - treating "I found no gate" as
// "nothing to check" - is the optimistic PASS this whole architecture exists to
// refuse.
func (a *Anchor) detectChecks() []config.Check {
	timeout := a.cfg.Timeout
	if timeout <= 0 {
		timeout = autoCheckTimeout
	}
	if a.checkTimeout > timeout {
		timeout = a.checkTimeout
	}
	if checks := a.declaredChecks(timeout); len(checks) > 0 {
		return checks
	}
	if checks := a.makefileChecks(timeout); len(checks) > 0 {
		return checks
	}
	if checks := a.goChecks(timeout); len(checks) > 0 {
		return checks
	}
	if checks := a.npmChecks(timeout); len(checks) > 0 {
		return checks
	}
	if checks := a.cargoChecks(timeout); len(checks) > 0 {
		return checks
	}
	return a.pythonChecks(timeout)
}

// declaredChecks runs what the project wrote in .motita/anchor.
//
// Each command goes through `sh -c` (PowerShell on Windows), because a gate is often two commands joined by
// `&&` and a checker that cannot express that pushes the user back into writing a
// wrapper script for every project.
func (a *Anchor) declaredChecks(timeout time.Duration) []config.Check {
	body, ok := readIfPresent(a.dir, declaredGateFile)
	if !ok {
		return nil
	}
	var out []config.Check
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Each line gets a name of its own: the baseline tells checks apart by name, and two
		// lines both called "declared" would share one verdict.
		name := "declared"
		if len(out) > 0 {
			name = fmt.Sprintf("declared %d", len(out)+1)
		}
		out = append(out, config.Check{
			Name:       name,
			Command:    declaredShell,
			Args:       execx.ShellArgs(declaredShell, line),
			Timeout:    timeout,
			ExpectExit: 0,
		})
	}
	return out
}

// makeTarget matches a name that begins a Makefile line, with the rest of the line
// captured so the caller can decide whether it is a real target.
//
// Go's regexp has NO lookahead, and the obvious workaround backtracks: in
// `check := x` the `[ 	]*` before the colon can give back the space and leave the
// `=` to be captured as "something that is not =". The decision is therefore made in
// makefileChecks, where the captured remainder is checked for a leading `=` - an
// assignment is not a target, and `make check` on a Makefile that only assigns a
// variable named check is a command that does not exist.
var makeTarget = regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+)[ 	]*:[ 	]*(.*)$`)

// makefileChecks uses the project's own Makefile.
//
// `check` is preferred over `test` because it is the conventional name for "the
// whole gate" (in the repository this was written in, `check` runs fmt-check, vet
// AND test), and a project that has one has said which is the fuller check.
func (a *Anchor) makefileChecks(timeout time.Duration) []config.Check {
	makefile, body, ok := a.makefile()
	if !ok {
		return nil
	}
	targets := map[string]bool{}
	for _, m := range makeTarget.FindAllStringSubmatchIndex(body, -1) {
		name, rest := body[m[2]:m[3]], body[m[4]:m[5]]
		// `FOO := bar` and `FOO = bar` both begin with a name; neither is a target.
		if strings.HasPrefix(strings.TrimSpace(rest), "=") {
			continue
		}
		// A target that checks nothing (`check: ; true`, or no recipe and no prerequisites) is not
		// a gate: declaring it passes anything, and the next convention speaks for the project.
		if trivialTarget(rest, body[m[5]:]) {
			continue
		}
		targets[name] = true
	}
	for _, name := range []string{"check", "test"} {
		if targets[name] {
			checks := []config.Check{{
				Name:    name,
				Command: "make",
				// `-f <makefile>` names the file the target was read from. Without it `make`
				// reads whichever of GNUmakefile, makefile and Makefile comes FIRST, so an agent
				// that writes a GNUmakefile beside a Makefile makes `make check` run its target,
				// not the one detection saw - a false PASS. The file is named so the gate that
				// runs is the gate that was read.
				Args:       []string{"-f", makefile, name},
				Timeout:    timeout,
				ExpectExit: 0,
			}}
			// `check` is the fuller gate and is trusted to hold the coverage too; a project that only
			// has `test` and names a coverage target has said it wants a minimum, so it is part of the gate.
			if name == "test" {
				checks = append(checks, a.coverageTargets(makefile, targets, timeout)...)
			}
			return checks
		}
	}
	return nil
}

// coverageTargets are the coverage targets of a makefile, which fail under the project's own minimum.
func (a *Anchor) coverageTargets(makefile string, targets map[string]bool, timeout time.Duration) []config.Check {
	var out []config.Check
	for _, name := range []string{"cover", "coverage"} {
		if targets[name] {
			out = append(out, config.Check{
				Name: name, Command: "make", Args: []string{"-f", makefile, name},
				Timeout: timeout, ExpectExit: 0,
			})
		}
	}
	return out
}

// trivialCommand says whether a command line does nothing that could fail: empty, `true`, `:`,
// `exit 0`, or a few of them chained. A script like that is not a gate, it is a way to pass.
func trivialCommand(cmd string) bool {
	for _, part := range strings.FieldsFunc(cmd, func(r rune) bool { return r == ';' || r == '&' }) {
		part = strings.TrimLeft(strings.TrimSpace(part), "@-+ \t")
		switch part {
		case "", "true", ":", "exit 0", "exit":
		default:
			return false
		}
	}
	return true
}

// trivialTarget says whether a makefile target does nothing: no prerequisites and no recipe that
// could fail. rest is what follows the colon on the target's line, after the line's end the
// remaining body, whose tab-indented lines are the recipe.
func trivialTarget(rest, after string) bool {
	prereq, inline, hasInline := strings.Cut(rest, ";")
	if strings.TrimSpace(prereq) != "" {
		return false
	}
	if hasInline && !trivialCommand(inline) {
		return false
	}
	for _, line := range strings.Split(after, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] != '\t' {
			break
		}
		if !trivialCommand(line) {
			return false
		}
	}
	return true
}

// makefileNames are the files GNU make reads, in the order it prefers them: it uses the FIRST that
// exists, so a GNUmakefile shadows a makefile, which shadows a Makefile. Detection and the gate
// fingerprint both go through this order, so the file the gate runs is the one make would pick and
// a higher-priority file appearing later changes the fingerprint.
var makefileNames = []string{"GNUmakefile", "makefile", "Makefile"}

// makefile returns the makefile GNU make would read in the anchor's directory - its name and body
// - or reports that there is none. A name counts only as the directory spells it: on a
// case-insensitive file system (macOS, Windows) opening "makefile" would open a "Makefile".
func (a *Anchor) makefile() (name, body string, ok bool) {
	entries, _ := os.ReadDir(a.dir)
	present := map[string]bool{}
	for _, e := range entries {
		present[e.Name()] = true
	}
	for _, name := range makefileNames {
		if !present[name] {
			continue
		}
		if body, ok := readIfPresent(a.dir, name); ok {
			return name, body, true
		}
	}
	return "", "", false
}

// goChecks uses the toolchain's own test command.
func (a *Anchor) goChecks(timeout time.Duration) []config.Check {
	if _, ok := readIfPresent(a.dir, "go.mod"); !ok {
		return nil
	}
	return []config.Check{{
		Name:       "go test",
		Command:    "go",
		Args:       []string{"test", "./..."},
		Timeout:    timeout,
		ExpectExit: 0,
	}}
}

// npmChecks uses the scripts the project's package.json declares.
//
// The three script names are the ones that mean "a gate" rather than "a build
// step": lint, typecheck, test and the coverage script. `build` is deliberately NOT among them - a
// project can have a build that needs credentials, a network or five minutes of
// compilation, and a validator that cannot run is a run that cannot finish.
func (a *Anchor) npmChecks(timeout time.Duration) []config.Check {
	body, ok := readIfPresent(a.dir, "package.json")
	if !ok {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(body), &pkg); err != nil {
		return nil
	}
	var out []config.Check
	// test:coverage and coverage are where a project keeps its minimum: a task that lowers it must fail.
	for _, name := range []string{"lint", "typecheck", "test", "test:coverage", "coverage"} {
		if script, declared := pkg.Scripts[name]; declared && !trivialCommand(script) {
			out = append(out, config.Check{
				Name:       "npm " + name,
				Command:    "npm",
				Args:       []string{"run", name},
				Timeout:    timeout,
				ExpectExit: 0,
			})
		}
	}
	return out
}

// cargoChecks uses the Rust toolchain's own test command.
func (a *Anchor) cargoChecks(timeout time.Duration) []config.Check {
	if _, ok := readIfPresent(a.dir, "Cargo.toml"); !ok {
		return nil
	}
	return []config.Check{{
		Name:       "cargo test",
		Command:    "cargo",
		Args:       []string{"test"},
		Timeout:    timeout,
		ExpectExit: 0,
	}}
}

// pythonChecks uses pytest, for a project that declares itself with pyproject.toml
// or setup.py.
//
// It is the last convention tried because it is the loosest one: pytest is not
// declared by those files, so this proposes a command the project may not depend on.
// A project that does not have it gets a FAIL naming the missing executable, which
// is why the anchor's reason is the actionable part - and why a project with a real
// gate should say so in .motita/anchor instead of relying on this.
func (a *Anchor) pythonChecks(timeout time.Duration) []config.Check {
	if !a.declaresPytest() {
		return nil
	}
	return []config.Check{{
		Name:       "pytest",
		Command:    "pytest",
		Args:       []string{"-q"},
		Timeout:    timeout,
		ExpectExit: 0,
	}}
}

// declaresPytest says whether the project gives evidence that it uses pytest: its own
// configuration, a conftest.py, or a pyproject/setup file that names it. A pyproject.toml alone
// says "this is Python", not "run pytest", and a command the project never asked for is a gate
// that passes (or fails) for the wrong reason.
func (a *Anchor) declaresPytest() bool {
	for _, name := range []string{"pytest.ini", "conftest.py", "tox.ini"} {
		if _, ok := readIfPresent(a.dir, name); ok {
			return true
		}
	}
	for _, name := range []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "requirements-dev.txt"} {
		if body, ok := readIfPresent(a.dir, name); ok && strings.Contains(strings.ToLower(body), "pytest") {
			return true
		}
	}
	return false
}

// GateFingerprint identifies the gate kind=auto would run right now: the detected checks, and the
// content of what defines what they do - .motita/anchor, the Makefile, package.json's scripts.
// It is empty for every other kind, whose checks come from the configuration.
//
// Detection reads files the agent itself can edit, so a gate that is re-detected on every
// validation is a gate the model can rewrite (`check: ; true`, `"test": "exit 0"`, a new
// .motita/anchor) and then pass. The fingerprint is how the caller sees that the gate it is
// about to believe is not the one the task started with. The directory is not part of it, so a
// background agent's worktree of the same tree reads the same.
func (a *Anchor) GateFingerprint() string {
	if !strings.EqualFold(a.cfg.Kind, "auto") {
		return ""
	}
	h := sha256.New()
	for _, c := range a.detectChecks() {
		fmt.Fprintf(h, "check %q %q %q %d %q\n", c.Name, c.Command, c.Args, c.ExpectExit, c.ExpectOutput)
	}
	// Every makefile make could read, not only Makefile: writing a GNUmakefile or a makefile
	// beside a Makefile is how the gate was made to run a different target (make prefers them),
	// so each one's presence and content is part of what the caller compares.
	names := append([]string{declaredGateFile}, makefileNames...)
	texts := make([]string, 0, len(names)+1)
	for _, name := range names {
		body, ok := readIfPresent(a.dir, name)
		fmt.Fprintf(h, "file %s %t %d\n%s\n", name, ok, len(body), body)
		texts = append(texts, body)
	}
	// Only the scripts: a dependency added to package.json changes nothing the gate runs. The
	// .npmrc is included because it can change how `npm run` resolves and runs those scripts.
	if body, ok := readIfPresent(a.dir, "package.json"); ok {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		_ = json.Unmarshal([]byte(body), &pkg)
		scripts, _ := json.Marshal(pkg.Scripts)
		fmt.Fprintf(h, "scripts %s\n", scripts)
		texts = append(texts, string(scripts))
		npmrc, present := readIfPresent(a.dir, ".npmrc")
		fmt.Fprintf(h, "npmrc %t %d\n%s\n", present, len(npmrc), npmrc)
	}
	// What the gate runs and reads: included makefiles, scripts it calls, the Python configuration.
	a.writeExtraFingerprint(h, texts)
	return hex.EncodeToString(h.Sum(nil))
}

// readIfPresent reads a file inside the anchor's directory, or reports that it is
// not there.
//
// Every failure is "not present" and none of them is fatal: a directory used as a
// file, a permission the process does not have, a name that is simply absent - all
// of them mean this convention does not speak for the project, and detection moves
// on. Reporting an error instead would make an unreadable file somewhere in the tree
// a reason the agent could not work at all.
func readIfPresent(dir, name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	return string(data), true
}

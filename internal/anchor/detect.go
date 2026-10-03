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
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
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
// Each command goes through `sh -c`, because a gate is often two commands joined by
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
		out = append(out, config.Check{
			Name:       "declared",
			Command:    "sh",
			Args:       []string{"-c", line},
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
	body, ok := readIfPresent(a.dir, "Makefile")
	if !ok {
		return nil
	}
	targets := map[string]bool{}
	for _, m := range makeTarget.FindAllStringSubmatch(body, -1) {
		// `FOO := bar` and `FOO = bar` both begin with a name; neither is a target.
		if strings.HasPrefix(strings.TrimSpace(m[2]), "=") {
			continue
		}
		targets[m[1]] = true
	}
	for _, name := range []string{"check", "test"} {
		if targets[name] {
			return []config.Check{{
				Name:       name,
				Command:    "make",
				Args:       []string{name},
				Timeout:    timeout,
				ExpectExit: 0,
			}}
		}
	}
	return nil
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
// step": lint, typecheck and test. `build` is deliberately NOT among them - a
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
	for _, name := range []string{"lint", "typecheck", "test"} {
		if _, declared := pkg.Scripts[name]; declared {
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
	if _, ok := readIfPresent(a.dir, "pyproject.toml"); !ok {
		if _, ok := readIfPresent(a.dir, "setup.py"); !ok {
			return nil
		}
	}
	return []config.Check{{
		Name:       "pytest",
		Command:    "pytest",
		Args:       []string{"-q"},
		Timeout:    timeout,
		ExpectExit: 0,
	}}
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

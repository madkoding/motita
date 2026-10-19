// Package policy decides what MAY happen to a command the agent wants to run.
//
// It exists because two different questions were being answered by one mechanism, and
// neither answer was complete:
//
//   - plan mode is structurally read-only: a command that is not a known reader is
//     refused, and there is no shell to hide in. That is the right answer there, and it is
//     a guarantee rather than a request.
//   - but in a mode where writing is the point, "do not write" is the wrong question, and
//     the old agent had no other: outside read-only mode every line went to `sh -c`
//     unexamined, so the model could do anything the user could, silently.
//
// What this package adds is the missing middle: the verdict ASK. Some actions are ordinary
// work that a person does want done — they are not refused, they are confirmed. And
// underneath that sits a floor that no configuration, permission flag or model argument
// may lower: a handful of commands that are refused whatever the operator chose.
//
// The floor is deliberately small and boring. It is not a security product and does not
// try to be: it is the set of things whose outcome is not recoverable and whose honest
// answer, from a program running inside somebody's home directory, is never. Everything
// else is a judgement call that belongs to the user, and putting that call in front of
// them is the whole point of Ask.
package policy

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/readonly"
	"github.com/madkoding/motita/internal/semantic"
)

// Literals used more than once in this file.
const (
	categoryExternalEffect = "external-effect"
	worksInFormat          = "works in (%s)"
	tsNode                 = "ts-node"
)

// Verdict is what may be done with one command.
type Verdict int

const (
	// Allow: run it. It changes nothing, or it changes only the workspace the user pointed
	// the agent at.
	Allow Verdict = iota
	// Ask: do not run it until a person says yes. The command is not beyond the pale, it is
	// merely consequential, and the user is the one who knows whether this is the moment.
	Ask
	// Deny: do not run it and do not offer it. Either it is on the mandatory floor, or the
	// operator asked for the strict answer to anything unclassifiable.
	Deny
)

func (v Verdict) String() string {
	switch v {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	default:
		return "deny"
	}
}

// Decision is the verdict for one command, with the reason a person can act on.
type Decision struct {
	Verdict Verdict
	// Reason explains the verdict. It is shown to the user — the one making the decision —
	// and relayed to the model, which has to be able to propose something else.
	Reason string
	// Rule names the rule that produced the verdict, so a test can name what it expects
	// rather than matching prose, and so a refusal names the line of the policy that
	// refused it.
	Rule string
	// Mandatory is true when the verdict may not be relaxed: no configuration setting can
	// turn this Deny into anything else. It is what makes the floor a floor.
	Mandatory bool
}

// Mode is the operator's answer to the two questions that ARE theirs to answer.
type Mode struct {
	// Enforce confirms before a consequential action runs.
	//
	// With it off, Ask becomes Allow: the agent runs consequential commands without
	// confirming, which is the behaviour this project had before the policy existed and is
	// kept reachable because a batch run has nobody at the keyboard and an operator may
	// prefer a job that finishes over one that stops.
	//
	// It does NOT relax the mandatory floor. Nothing does.
	Enforce bool
	// Strict refuses what the policy cannot classify — an unknown program, a line that
	// needs a shell, a writer whose target is not in its arguments.
	//
	// Off (the default) means those are ASKED about instead. That is the honest default:
	// the set of programs a user's work needs is unbounded, a refusal of everything
	// unlisted makes the agent useless for the work it was pointed at, and a question costs
	// one keystroke. On is for a run where the operator would rather see a refusal than a
	// prompt.
	//
	// It does NOT reach the mandatory floor.
	Strict bool
	// Have reports whether a program is installed where the agent's commands will look for it.
	// It is optional: with nil the policy cannot tell a missing tool from a present one and says
	// nothing about it.
	Have func(name string) bool
	// PowerShell says the line runs under PowerShell (the shell on Windows). The segments of a
	// line are read with POSIX rules, and PowerShell gives meaning to text those rules call a
	// plain word: a script block in braces, `$(…)` inside double quotes, a backtick escape. A
	// line that holds any of them is not classified, so a "reader" cannot carry code past the
	// policy. See powerShellSyntax.
	PowerShell bool
	// Unconfined says nothing confines what a command reads (no Landlock: Windows, macOS, an old
	// kernel). A reader is then confirmed when it names a path outside the workspace or among the
	// user's keys, and the project's own code (a script, a build, a test) is confirmed before it
	// runs. See unconfinedDecision.
	Unconfined bool
	// TrustUnconfinedRuns keeps, with Unconfined, the project's own code (a script, a build, a test)
	// running without a question: the operator chose that (sandbox.ask_unconfined_runs: false). A
	// path outside the workspace is still asked about. Off, the default, asks.
	TrustUnconfinedRuns bool
	// FullDisk is sandbox.disk_access: full, so leaving the workspace is not asked about. See fulldisk.go.
	FullDisk bool
	// Cmd says the line runs under cmd.exe, whose expansions (`%VAR%`, `!VAR!`), escape (`^`) and
	// word division the POSIX reading does not know. Only a line of plain words is classified;
	// anything else is approved as a whole. See cmdLine.
	Cmd bool
}

// DecisionFor decides one command.
//
// command is the program as written, args are its arguments, and dir is the directory the
// command runs in — the working directory the user pointed the agent at. dir is used, and
// not decoration: the difference between "writes inside my project" and "writes outside
// it" is the difference between work and damage, and it is the only judgement here that
// needs to know where.
func (m Mode) DecisionFor(command string, args []string, dir string) Decision {
	return m.unconfinedDecision(m.fullDiskDecision(m.decisionFor(command, args, dir), args), args, dir)
}

// decisionFor is DecisionFor before what the lack of confinement changes.
func (m Mode) decisionFor(command string, args []string, dir string) Decision {
	// Leading `VAR=value` assignments are the environment, not the program: `FOO=1 ls` runs
	// `ls`. The floor scan already skips them, and classifying them as a program name made the
	// policy ask about a plain reader.
	for strings.ContainsRune(command, '=') && len(args) > 0 && !strings.HasPrefix(command, "/") {
		command, args = args[0], args[1:]
	}
	// A wrapper (`env`, `command`, `nice`, `timeout`…) is judged by the program it runs: every
	// rule below reads the program's name and arguments, and `env curl -d @~/.ssh/id_rsa …`
	// judged as `env` was a reader that ran in silence. A wrapper whose options hide what runs
	// is not classified at all.
	inner, innerArgs, why := readonly.Unwrap(command, args)
	if why != "" {
		return m.unclassified(why, "wrapper-unreadable")
	}
	if len(innerArgs) != len(args) {
		command, args = inner, innerArgs
		// The floor scan of the line knows some wrappers and not others (`timeout 5s rm -rf /`
		// names its program after a duration), so the unwrapped program meets the floor here too.
		if d, hit := mandatory(baseName(command), args, dir); hit {
			return d
		}
	}
	kind, reason := readonly.Classify(command, args)
	name := baseName(command)
	// A task works in the directory it was given. Moving the command's directory elsewhere
	// (`cd /other && git commit`, `git -C /other push`) runs the rest of the line against a
	// different tree, which no path check on the arguments can see, so it is judged first.
	if d, hit := leavesWorkspace(name, args, dir); hit {
		return d
	}
	// Two rules about the work the agent does for the user's repository, judged before the
	// generic ones because the generic ones cannot see them: a commit whose message is not
	// semantic, and the commands motita gives the agent for pull requests.
	if d, hit := commitMessageDecision(name, args); hit {
		return d
	}
	if d, hit := forgeDecision(name, args); hit {
		return d
	}
	// Installing what the project's own manifest declares, and running the project's own dev
	// tools, is the ordinary work of every project: without it the run stops to ask before
	// `npm ci` on a run with nobody to answer, and the anchor then fails on a missing module.
	if d, hit := projectToolDecision(name, args); hit {
		return d
	}
	switch kind {
	case readonly.KindReader:
		return Decision{Allow, reason, "reader", false}
	case readonly.KindMissing:
		return Decision{Deny, reason, "missing", false}
	case readonly.KindShell:
		// An interpreter is not classified by looking at it: the line it is given is data
		// until it is interpreted, and `sh -c 'ls'` and `sh -c 'rm -rf /'` have the same
		// shape. It is offered to the user rather than refused, because they can read the
		// line and decide — but it is never run unreviewed.
		return Decision{Ask, reason + ": a shell line has to be approved as a whole", "shell", false}
	case readonly.KindWriter:
		return m.writerDecision(command, name, args, dir)
	default:
		return m.unknownDecision(command, name, args, dir)
	}
}

// writerDecision decides a command classified as a writer.
func (m Mode) writerDecision(command, name string, args []string, dir string) Decision {
	// The writers whose effect reaches OUTSIDE the machine are asked about before the
	// path question, because they have no path to check: `git push`, `npm publish`,
	// `systemctl restart` and `curl -X DELETE` do not name a file, and their effect is
	// not undone by a backup.
	if reachesOutside(name, args) {
		return Decision{Ask, fmt.Sprintf("%q can reach outside this machine or change the system "+
			"as a whole, which is not something a task should do unannounced", baseName(command)),
			categoryExternalEffect, false}
	}
	if d, hit := writingForm(command, args, dir); hit {
		return d
	}
	// A known local build tool before the unclassified rule: `make`, `go build`, `python3
	// script.py`. Without this the default would ask about the ordinary work of a project,
	// which is the policy that gets switched off.
	if d, hit := localToolDecision(name, args, dir); hit {
		return d
	}
	return m.unclassified(fmt.Sprintf("%q changes the system and its target could not be "+
		"determined from the arguments", baseName(command)), "writer-unclassified")
}

// unknownDecision decides a command the classifier has no verdict for.
func (m Mode) unknownDecision(command, name string, args []string, dir string) Decision {
	if reachesOutside(name, args) {
		return Decision{Ask, fmt.Sprintf("%q reaches outside this machine, and what it does there "+
			"cannot be checked from the command line", baseName(command)), categoryExternalEffect, false}
	}
	// The same for the unknown branch: a binary whose name we do not know a verdict for
	// may still be a build tool the project needs.
	if d, hit := localToolDecision(name, args, dir); hit {
		return d
	}
	// A program that lives INSIDE the workspace is the project's own — `./scripts/deploy.sh`,
	// `./bin/mytool`. It is the same judgement the writers get: the work stays where the
	// user pointed the agent. A path outside is not this branch's to allow.
	if d, hit := projectLocalDecision(command, args, dir); hit {
		return d
	}
	return m.unclassified(fmt.Sprintf("%q is not a command this program knows, so what it "+
		"changes cannot be predicted", baseName(command)), "unclassified")
}

// commitMessageDecision refuses a `git commit` whose message is on the command line and is not a
// semantic (Conventional Commits) subject. It is a refusal and not a question: there is no
// reason to approve a message the repository's release tooling cannot read, and the reason
// carries the form to write so the next attempt is right. A commit whose message cannot be
// seen from the line (-F, an editor, --amend with no -m) is not judged here.
func commitMessageDecision(name string, args []string) (Decision, bool) {
	if name != "git" {
		return Decision{}, false
	}
	sub, rest := gitSubcommand(args)
	if sub != "commit" {
		return Decision{}, false
	}
	subject, ok := semantic.CommitSubject(rest)
	if !ok {
		return Decision{}, false
	}
	if err := semantic.LintSubject(subject); err != nil {
		return Decision{Deny, "the commit message is not semantic: " + err.Error(), "semantic-commit", false}, true
	}
	return Decision{}, false
}

// gitValueOptions are git's global options that take the next argument as their value.
var gitValueOptions = map[string]bool{
	"-C": true, "-c": true, gitDirOption: true, gitWorkTreeOption: true, "--namespace": true, "--exec-path": true,
}

// gitSubcommand splits git's arguments into its subcommand and what follows it, skipping the
// global options before the subcommand, some of which take a value (`git -C dir commit`).
func gitSubcommand(args []string) (sub string, rest []string) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case gitValueOptions[a]:
			i++
		case strings.HasPrefix(a, "-"):
			// A global option without a value: skipped.
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

// forgeDecision classifies `motita forge ...`, the commands the agent uses to open a pull request
// and read its CI. Reading the CI and listing the connections change nothing, so they run.
// Opening a pull request publishes the user's work under their name on a host they own, so
// the user is asked, the same way `git push` is.
func forgeDecision(name string, args []string) (Decision, bool) {
	if name != "motita" || len(args) == 0 || args[0] != "forge" {
		return Decision{}, false
	}
	if len(args) >= 3 && args[1] == "pr" && args[2] == "create" {
		return Decision{Ask, "this opens a pull request on the git host, under the user's account",
			categoryExternalEffect, false}, true
	}
	return Decision{Allow, "this only reads from the git host", "reader", false}, true
}

// shellBuiltins are the words a shell runs itself, which no PATH lookup can find.
var shellBuiltins = map[string]bool{
	"cd": true, "pushd": true, "popd": true, "dirs": true, "echo": true, "printf": true, "export": true,
	"set": true, "unset": true, "test": true, "[": true, "[[": true, "source": true, ".": true,
	"exit": true, "return": true, "read": true, "type": true, "command": true, "alias": true,
	"eval": true, "exec": true, "true": true, "false": true, ":": true, "pwd": true, "wait": true,
	"shift": true, "trap": true, "ulimit": true, "umask": true, "local": true, "declare": true,
	"let": true, "hash": true, "builtin": true, "jobs": true, "fg": true, "bg": true, "getopts": true,
	"break": true, "continue": true, "kill": true, "history": true, "times": true,
}

// missingTool turns a program that is not installed into a question, with the way forward in it.
//
// Running it would only fail with "command not found", and the model's usual answer to that is to
// install the tool on its own. Asking first puts that decision with the user, who can approve the
// install or do it themselves. It answers false for what it cannot judge: no way to look programs
// up, a path, a shell builtin or an assignment.
func (m Mode) missingTool(name string) (Decision, bool) {
	if m.Have == nil || unjudgeableTool(name) || m.Have(name) {
		return Decision{}, false
	}
	return Decision{Ask, fmt.Sprintf("%q is not installed. Approve to let the agent install it with the "+
		"system package manager or the project's own way, or install it yourself and run the "+
		"command again", name), "tool-missing", false}, true
}

// unjudgeableTool reports a name that no PATH lookup can answer for: none at all, a shell
// builtin, a path or an assignment.
func unjudgeableTool(name string) bool {
	return name == "" || shellBuiltins[name] || strings.ContainsAny(name, "/=$~")
}

// leavesWorkspace asks about a command that points itself at a directory outside dir: `cd` and
// `pushd`, and the flags with which a program runs somewhere else (`git -C`, `git --work-tree`,
// `make --directory`, `go -C`, `npm --prefix`, ...; see dirFlags). The task's
// workspace is where the work lives - the session's project - and a line that walks out of it is
// how a session ends up building, committing or installing in another project's checkout.
//
// A bare `cd` (home) and `cd -` (the previous directory) are outside by construction. It answers
// false for a command that stays inside, and for one with no workspace to compare against.
func leavesWorkspace(name string, args []string, dir string) (Decision, bool) {
	if dir == "" {
		return Decision{}, false
	}
	var targets []string
	switch name {
	case "cd", "pushd":
		target := "~"
		if operands := rmTargets(args); len(operands) > 0 {
			target = operands[0]
		}
		targets = []string{target}
	default:
		targets = dirFlagTargets(dirFlags[name], args)
	}
	for _, target := range targets {
		if target == "-" || firstOutside([]string{target}, dir) != "" {
			return Decision{Ask, fmt.Sprintf("%q moves to %q, which is outside the directory this task "+
				worksInFormat, name, target, dir), "leaves-workspace", false}, true
		}
	}
	return Decision{}, false
}

// longDirectoryFlag is the long spelling of -C shared by make and tar.
const longDirectoryFlag = "--directory"

// dirFlags are, per program, the options that make it work in (or on) another directory than the
// one it is started in. Each takes a value as the next argument (`-C dir`, `--directory dir`) or
// after "=" (`--directory=dir`); a short one also attached (`-Cdir`).
var dirFlags = map[string][]string{
	"git":   {"-C", gitDirOption, gitWorkTreeOption},
	"make":  {"-C", longDirectoryFlag, "-f", "--file", "--makefile"},
	"gmake": {"-C", longDirectoryFlag, "-f", "--file", "--makefile"},
	"go":    {"-C"},
	"npm":   {"--prefix"},
	"pnpm":  {"-C", "--dir"},
	"yarn":  {"--cwd"},
	"cargo": {"--manifest-path"},
	"tar":   {"-C", longDirectoryFlag},
}

// dirFlagTargets are the directories (or files) the given flags name in args, every occurrence:
// `git -C a -C b` ends up in b, and checking only the first would let the second walk out.
func dirFlagTargets(flags []string, args []string) []string {
	var out []string
	for i, a := range args {
		for _, f := range flags {
			switch {
			case a == f && i+1 < len(args):
				out = append(out, args[i+1])
			case strings.HasPrefix(a, f+"=") && len(a) > len(f)+1:
				out = append(out, a[len(f)+1:])
			case len(f) == 2 && strings.HasPrefix(a, f) && len(a) > 2 && a[2] != '=':
				out = append(out, a[2:])
			default:
				// This argument is not flag f: nothing to collect.
			}
		}
	}
	return out
}

// projectLocalDecision allows a program that lives inside the workspace and is passed explicit
// arguments, because that is the project's own tooling running on the project's own files.
//
// It is deliberately narrow in two ways. The program must be named by a PATH that resolves
// inside the workspace (a bare name is what the PATH lookup is for, and allowing it would
// allow any same-named binary anywhere). And its arguments are checked like a writer's: an
// operand pointing outside makes it a question, so `./build.sh /etc/passwd` does not ride in
// on the script's location.
func projectLocalDecision(command string, args []string, dir string) (Decision, bool) {
	if dir == "" || !strings.ContainsRune(command, filepath.Separator) {
		return Decision{}, false
	}
	targets := rmTargets(args)
	if outside := firstOutside(targets, dir); outside != "" {
		return Decision{Ask, fmt.Sprintf("%q takes %q, which is outside the directory this task "+
			worksInFormat, baseName(command), outside, dir), "project-script-arg-outside", false}, true
	}
	if !isInsideWorkspace(command, dir) {
		return Decision{}, false
	}
	return Decision{Allow, fmt.Sprintf("%q is a program inside the workspace (%s), so its work "+
		"stays where the task is", baseName(command), dir), "project-local", false}, true
}

// isInsideWorkspace reports whether a path resolves to something inside dir. It reuses the
// same resolution as the write checks, so a workspace reached through a symlink does not make
// every path inside it look like it is outside.
//
// With no workspace it answers FALSE, and that matters: firstOutside compares against dir and
// returns "" (meaning "nothing was outside") when dir is empty, so delegating the empty case
// would make every path in the system look like it lives inside "nothing". The caller guards
// this too, and a helper that lies about the empty case is exactly the kind of thing that gets
// copied somewhere the guard is missing.
func isInsideWorkspace(path, dir string) bool {
	if dir == "" {
		return false
	}
	if strings.HasPrefix(filepath.Clean(path), "~") {
		return false
	}
	return firstOutside([]string{path}, dir) == ""
}

// localTools are the programs whose ordinary use IS the work the agent was pointed at:
// building, testing and running the project in the workspace. They are named because the
// difference between them and an unknown binary is what keeps the default policy honest.
//
// The alternative — asking about every program the tables do not know — is the policy that
// gets switched off, because a coding agent runs `make` and `go build` all day. But the
// alternative that was there before was worse in the other direction: running them SILENTLY.
// Naming the ordinary ones lets the default ask about the genuinely unplaceable (an
// infrastructure tool, a binary nobody knows) without taxing the work.
//
// A name here is not a blanket Allow: `localToolDecision` still checks where an interpreter's
// script actually lives, and an inline program (`python3 -c '...'`) is treated as opaque.
var localTools = map[string]bool{
	// build systems and compilers
	"make": true, "gmake": true, "cmake": true, "meson": true, "ninja": true, "gradle": true,
	"gcc": true, "g++": true, "cc": true, "c++": true, "clang": true, "clang++": true,
	"ld": true, "as": true, "ar": true, "mvn": true, "dotnet": true, "javac": true,
	"cargo": true, "go": true, "rustc": true, "zig": true,
	// test runners and linters
	"pytest": true, "ruff": true, "black": true, "mypy": true, "flake8": true,
	"eslint": true, "prettier": true, "tsc": true, "jest": true, "vitest": true,
	"gofmt": true, "golangci-lint": true, "shfmt": true, "shellcheck": true,
	// interpreters: the script's own location is checked below
	"python": true, "python3": true, "node": true, "ruby": true, "perl": true, "php": true,
	"deno": true, "bun": true, "java": true, tsNode: true,
	// project tooling
	"west": true, "platformio": true, "idf.py": true, "poetry": true, "uv": true,
	// The tools that are also in externalPrograms, whose LOCAL VERBS are ordinary work.
	// `reachesOutside` has already returned false by the time this table is consulted, so
	// `git add` and `npm test` are here while `git push` and `npm publish` never reach it —
	// they were asked about as external effects first. Both tables are needed and the order
	// is what keeps them from contradicting each other.
	"git": true, "hg": true, "svn": true,
	"npm": true, "yarn": true, "pnpm": true, "pip": true, "pip3": true,
	"docker": true, "podman": true, "kubectl": true, "helm": true,
}

// interpreters are the local tools whose first non-flag argument is a FILE containing the
// program. That file is what the policy can look at, which is why these get the extra check.
var interpreters = map[string]bool{
	"python": true, "python3": true, "node": true, "ruby": true, "perl": true, "php": true,
	"deno": true, "bun": true, tsNode: true,
}

// inlineProgramFlags names, PER INTERPRETER, the flags that carry the PROGRAM ITSELF in the
// argument rather than a file to run: `python3 -c '...'`, `node -e '...'`, `perl -ne '...'`.
//
// It is keyed by program because the same flag means different things in different ones:
// `-c` is "run this code" for python and "compile only, do not link" for gcc. A single flat
// set would have made `gcc -c foo.c` — ordinary work — look like an opaque program.
//
// Such a line is as opaque as a shell line: the code is data until the interpreter runs it,
// and `python3 -c 'import shutil; shutil.rmtree(...)'` has the same shape as any other inline
// program. There is no file to resolve, so these fall to the unclassified rule, which asks.
var inlineProgramFlags = map[string]map[string]bool{
	"python":  {"-c": true, "-m": true},
	"python3": {"-c": true, "-m": true},
	"node":    {"-e": true, "--eval": true, "-p": true, "--print": true},
	"bun":     {"-e": true, "--eval": true},
	"deno":    {"eval": true},
	"perl":    {"-e": true, "-E": true, "-pe": true, "-ne": true, "-ple": true},
	"ruby":    {"-e": true},
	"php":     {"-r": true},
	tsNode:    {"-e": true},
}

// localToolDecision answers the programs whose ordinary use is local work.
//
// It is consulted before the unclassified rule, and it is what makes the default policy
// usable: without it, asking about everything the tables do not list would put a question in
// front of `make` and `go build`. With it, the questions land on the lines nobody can predict.
func localToolDecision(name string, args []string, dir string) (Decision, bool) {
	if !localTools[name] {
		return Decision{}, false
	}
	// A program handed to the interpreter as TEXT has no file to check, so it is not this
	// function's to allow.
	for _, a := range args {
		if inlineProgramFlags[name][a] {
			return Decision{}, false
		}
	}
	// The interpreter's script decides what runs. A script outside the workspace is not the
	// workspace's work — and it is exactly the case the earlier policy ran in silence.
	if scripts := scriptOperands(name, args); len(scripts) > 0 {
		if outside := firstOutside(scripts, dir); outside != "" {
			return Decision{Ask, fmt.Sprintf("%q runs %q, which is outside the directory this "+
				"task works in (%s)", name, outside, dir), "script-outside-workspace", false}, true
		}
	}
	return Decision{Allow, fmt.Sprintf("%q is a local build tool and its work stays in the "+
		"workspace", name), "local-tool", false}, true
}

// scriptOperands returns the file an interpreter has been handed, when it was handed one.
//
// Only interpreters have this: `make` and `gcc` take their work from the tree and their
// arguments, which the workspace rule already covers.
func scriptOperands(name string, args []string) []string {
	if !interpreters[name] {
		return nil
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return []string{a}
	}
	return nil
}

// externalPrograms are the programs whose effect is not a file: the network, a service, a
// package manager, another machine, another user's account.
//
// They get their own verdict because the workspace rule cannot see them at all. A command
// that pushes to a remote, publishes a package, or restarts a daemon has no target path to
// compare, so a policy that only checked paths would wave it through while asking about
// `touch ../notes.txt`. The list is deliberately about EFFECT rather than danger: `git` is
// here because `git push` is, not because version control is frightening.
var externalPrograms = map[string]bool{
	// version control reaching a remote or rewriting history
	"git": true, "hg": true, "svn": true,
	// the network
	"curl": true, "wget": true, "ssh": true, "scp": true, "sftp": true, "rsync": true,
	"nc": true, "netcat": true, "telnet": true, "ftp": true, "socat": true,
	// the same on Windows, where the request is a cmdlet (or its alias) or curl.exe
	"curl.exe": true, "invoke-restmethod": true, "invoke-webrequest": true, "irm": true, "iwr": true,
	// services and init. `journalctl` is deliberately NOT here: it only reads the log, and
	// it is in the reader list for exactly that reason.
	"systemctl": true, "service": true,
	"initctl": true, "launchctl": true, "sc": true,
	// package managers: they install into the system and often from the network
	"apt": true, "apt-get": true, "dpkg": true, "dnf": true, "yum": true, "pacman": true,
	"apk": true, "snap": true, "flatpak": true, "pip": true, "pip3": true, "npm": true,
	"yarn": true, "pnpm": true, "cargo": true, "gem": true, "go": true, "composer": true,
	// containers and orchestration: the effect lands somewhere else entirely
	"docker": true, "podman": true, "kubectl": true, "helm": true, "vagrant": true,
	// privileges and accounts
	"sudo": true, "su": true, "doas": true, "useradd": true, "userdel": true, "usermod": true,
	"groupadd": true, "passwd": true, "chsh": true, "visudo": true,
	// process and machine control
	"kill": true, "killall": true, "pkill": true, "crontab": true, "at": true,
	"systemd-run": true, "reboot": true, "shutdown": true, "halt": true, "poweroff": true,
	// the firewall and the filesystem as a whole
	"iptables": true, "nft": true, "ufw": true, "firewall-cmd": true,
	"mount": true, "umount": true, "swapon": true, "swapoff": true, "fdisk": true,
}

// reachesOutside reports whether a program's effect is known to leave the workspace, the
// machine, or the machine's current configuration.
//
// For the programs that have BOTH a local life and a remote one, the subcommand decides:
// `go test` and `go build` are the ordinary verbs of working in a project, while `go get`
// and `go install` reach the network; `npm test` runs a script, `npm publish` publishes under
// the user's name. Asking about the local ones would be the false alarm that gets a policy
// switched off — and the point of the policy is to be believed on the day it matters.
func reachesOutside(name string, args []string) bool {
	if !externalPrograms[name] {
		return false
	}
	// git needs the WHOLE command, not only its subcommand: `git config user.name x` writes
	// the repository's configuration while `git config --get user.name` only reads it, and the
	// two differ in their arguments, not their subcommand.
	if name == "git" {
		return gitReachesOutside(args)
	}
	if sub, ok := subcommandScoped[name]; ok {
		return sub(firstNonFlag(args))
	}
	return true
}

// gitLocalVerbs are the git subcommands whose effect stays in the working tree the task was
// given. `init` is NOT among them: it creates a repository, whose .git/config an agent then
// controls and whose own git this program later runs outside the sandbox, so creating one is a
// step the user is asked about. `config` is not here either, because reading and writing it are
// told apart by gitReachesOutside.
var gitLocalVerbs = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "branch": true, "remote": true,
	"tag": true, "describe": true, "rev-parse": true, "blame": true, "shortlog": true,
	"ls-files": true, "cat-file": true, "grep": true, "whatchanged": true, "reflog": true,
	"show-ref": true, "for-each-ref": true, "count-objects": true, "add": true, "commit": true,
	"checkout": true, "switch": true, "restore": true, "stash": true, "merge": true,
	"rebase": true, "clone": true, "worktree": true, "rm": true, "mv": true,
}

// gitReachesOutside decides git by its subcommand, and - for `config` - by whether that
// subcommand writes. Setting a config value, or `git init`, changes a repository's own
// configuration, which can name a command a later git runs as the user and outside the sandbox;
// both are asked about. Reading config (`--get`, `--list`) changes nothing and is allowed.
func gitReachesOutside(args []string) bool {
	sub, rest := gitSubcommand(args)
	if sub == "config" {
		return gitConfigWrites(rest)
	}
	if !(gitLocalVerbs[sub] || gitPlumbingReads[sub]) || gitGlobalRunsCode(args) {
		return true
	}
	return gitVerbRunsCode(sub, rest)
}

// gitConfigWrites reports whether a `git config` sets or removes a value, as opposed to reading
// one. git reads with one positional argument (the key) and writes with two (the key and the
// value); the newer subcommand forms (`git config set`, `edit`, `unset`, `rename-section`,
// `remove-section`) and the writing options (`--add`, `--unset`, `--replace-all`, …) write too.
func gitConfigWrites(rest []string) bool {
	positional := 0
	for _, a := range rest {
		switch {
		case a == "--add", a == "--unset", a == "--unset-all", a == "--replace-all",
			a == "--rename-section", a == "--remove-section", a == "--edit", a == "-e":
			return true
		case strings.HasPrefix(a, "-"):
			// Any other option only selects or formats what is read.
		case positional == 0 && gitConfigWritingVerbs[a]:
			return true
		default:
			positional++
		}
	}
	return positional >= 2
}

// gitConfigWritingVerbs are the subcommands of the newer `git config` forms that write or open
// an editor.
var gitConfigWritingVerbs = map[string]bool{
	"set": true, "unset": true, "edit": true, "rename-section": true, "remove-section": true,
}

// subcommandScoped holds the programs whose verdict depends on their subcommand. A program
// absent from this map is external whatever its arguments say.
var subcommandScoped = map[string]func(sub string) bool{
	// Version control: inspecting and committing locally is work; everything that leaves the
	// machine, rewrites history, or throws work away is not. git is judged by gitReachesOutside
	// rather than from this table, because `config` needs its whole argument list to tell a read
	// from a write.
	"hg":  func(sub string) bool { return sub != "status" && sub != "log" && sub != "diff" },
	"svn": func(sub string) bool { return sub != "status" && sub != "log" && sub != "diff" },

	// Package managers and toolchains: the reading and building verbs are local.
	"go": func(sub string) bool {
		switch sub {
		case "test", "build", "vet", "fmt", "list", "doc", "version", "env", "run", "generate":
			return false
		default:
			return true
		}
	},
	"npm": func(sub string) bool {
		switch sub {
		case "test", "run", "run-script", "exec", "ls", "list", "view", "outdated", "audit":
			return false
		default:
			return true
		}
	},
	"cargo": func(sub string) bool {
		switch sub {
		case "test", "build", "check", "clippy", "fmt", "run", "doc", "tree":
			return false
		default:
			return true
		}
	},
	"pip":  func(sub string) bool { return sub != "list" && sub != "show" && sub != "freeze" },
	"pip3": func(sub string) bool { return sub != "list" && sub != "show" && sub != "freeze" },
	"yarn": func(sub string) bool {
		switch sub {
		case "test", "run", "list", "why", "outdated":
			return false
		default:
			return true
		}
	},

	// Containers: the local inspection verbs are harmless, anything that runs or ships is not.
	"docker": func(sub string) bool {
		switch sub {
		case "ps", "images", "inspect", "logs", "version", "info":
			return false
		default:
			return true
		}
	},
	"kubectl": func(sub string) bool {
		switch sub {
		case "get", "describe", "logs", "version", "explain":
			return false
		default:
			return true
		}
	},

	// Process control: inspecting is one thing, killing is another.
	"kill":  func(sub string) bool { return true },
	"mount": func(sub string) bool { return true },
}

// firstNonFlag returns the first argument that is not an option, which is how a program's
// subcommand is found.
func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// unclassified is the shared answer for everything the policy could not place: a program
// nobody knows, a writer whose target is not in its arguments, a line the tokeniser cannot
// read at all.
//
// It is one function because it is one decision, and having it in three places is how the
// three drift apart — the shell rule would end up stricter than the unknown-program rule for
// no reason anybody could state.
//
// The default ASKS and strict REFUSES, which is what both settings have always said they do.
// Running it silently was the one answer wrong in both directions at once: nobody was told,
// and the policy could not claim to have decided anything. A line the policy cannot place is
// precisely the line whose effect nobody can predict, and the user — who can read it — is
// the one who knows whether this is the moment.
//
// This is NOT the "ask about everything" policy that gets switched off, and the difference
// is localTools: the programs whose ordinary use IS the work (`make`, `go build`, `npm test`)
// are classified as local work before they can reach here. What lands in this branch is the
// genuinely unplaceable — an interpreter handed a file, an infrastructure tool, a binary
// nobody knows — which is exactly the set a person should see before it runs.
//
// Strict is the lever for a run with nobody at the keyboard: it turns every question into a
// refusal. It is off by default because a question costs one keystroke and a refusal costs
// the task.
func (m Mode) unclassified(reason, rule string) Decision {
	if m.Strict {
		return Decision{Deny, reason + ", and this mode refuses what it cannot classify", rule, false}
	}
	return Decision{Ask, reason + ", so it needs the user's approval before it runs", rule, false}
}

// ShellLine is the answer for a line the policy could not tokenise AT ALL: an unclosed
// quote, a trailing backslash, or a substitution it will not pretend to understand. The
// line is data until something interprets it — `sh -c 'ls'` and `sh -c 'rm -rf /'` have the
// same shape — so it can only be approved as a whole.
//
// It is reached rarely, because DecideLine reads a line in SEGMENTS first: a pipe, a
// redirection or a chain no longer sends the whole line to this answer, only the piece that
// genuinely could not be read.
func (m Mode) ShellLine(reason string) Decision {
	message := fmt.Sprintf("%s: this line cannot be read as one program with arguments, so what "+
		"it will do cannot be checked", reason)
	return m.unclassified(message, "shell-line")
}

// DecideLine analyses a whole command line and returns the verdict for it.
//
// It is not the same thing as deciding one program, and using the latter for the former is
// the mistake that makes a policy useless in both directions. A line is a sequence of
// SEGMENTS joined by a pipe, a redirection or a chain, and each segment has its own answer:
// `ls` is a reader, `> /etc/passwd` writes outside the workspace, `grep x f | wc -l` is two
// readers. Judging the line by its first word would allow the second half of it, and
// refusing on the first shell character would refuse `echo hi > f.txt`, which is ordinary
// work in the directory the user pointed the agent at.
//
// The rule applied to the whole line is the WORST verdict any of its segments earned. One
// part that must be approved is enough to ask about the line, because the line runs as a
// unit and approving half of it means nothing.
func (m Mode) DecideLine(line, dir string) Decision {
	// An empty line has nothing to run. It is answered here rather than by the segment loop,
	// which correctly produces no segments for it and would otherwise leave the line
	// carrying the "only reads" Allow it started with — a line that does nothing reported
	// as an action that ran.
	if strings.TrimSpace(line) == "" {
		return Decision{Deny, "there is no command to run", "missing", false}
	}

	// The floor runs on the raw text first, so that a shell, a chain or a malformed
	// command cannot walk past it.
	if d, hit := MandatoryInLine(line, dir); hit {
		return d
	}
	line, d, hit := m.windowsLine(line, dir)
	if hit {
		return d
	}

	// The line is judged by its WORST segment, and the seed is the first segment's own
	// verdict rather than a guess about the line. A seed of "it only reads" would be a
	// claim nobody made, and it would win every comparison against an equally-permissive
	// segment (`touch f` inside the workspace) — the line would then be reported as a read
	// that changed nothing while a file was created.
	var (
		worst Decision
		found bool
	)
	// A heredoc BODY is data handed to a program, not commands of the line. Reading it as
	// commands is what refused `cat > f << 'EOF'`, the ordinary way a model writes a file: the
	// first word of the body became an "unknown program" and the whole write was asked about.
	// The body is taken out before the line is cut into segments, and what makes it dangerous
	// is judged separately below.
	main, docs := splitHeredocs(line)
	if d, hit := m.heredocVerdict(main, docs); hit {
		return m.Relaxing(d)
	}
	for _, seg := range lineSegments(main) {
		d, skip, final := m.segmentDecision(seg, dir)
		if final {
			return d
		}
		if skip {
			continue
		}
		if m.PowerShell {
			d = powerShellVerdict(d, seg.words)
		}
		if !found {
			worst, found = d, true
			continue
		}
		worst = worse(worst, d)
	}
	if !found {
		return Decision{Deny, "there is no command to run", "missing", false}
	}
	return m.Relaxing(worst)
}

// windowsLine prepares a line for the POSIX reading when a Windows shell will run it. Under
// cmd.exe a line of anything but plain words is decided here (hit, see cmdLine). Under PowerShell
// a line PowerShell reads as code is decided here, and what is left comes back with its
// backslashes turned into slashes, the floor checked again on it. Any other line comes back as
// it is.
func (m Mode) windowsLine(line, dir string) (string, Decision, bool) {
	if d, hit := m.cmdLine(line); hit {
		return line, d, true
	}
	if !m.PowerShell {
		return line, Decision{}, false
	}
	if r, hit := powerShellSyntax(line); hit {
		return line, m.Relaxing(m.ShellLine(fmt.Sprintf("PowerShell reads %q as code", string(r)))), true
	}
	// What is left uses a backslash only as a path separator, which the POSIX reading
	// below would take for an escape: `srcpp` would be judged as `srcapp`. Windows takes
	// `/` for the same separator, so the line is judged with it, floor included.
	line = strings.ReplaceAll(line, `\`, "/")
	d, hit := MandatoryInLine(line, dir)
	return line, d, hit
}

// segmentDecision decides one segment of a line. skip reports a segment that runs nothing, and
// final a verdict that decides the whole line on its own.
func (m Mode) segmentDecision(seg lineSegment, dir string) (d Decision, skip, final bool) {
	if seg.redirect {
		return m.redirectTarget(seg, dir), false, false
	}
	words, syntax := stripShellKeyword(seg.words)
	if syntax {
		// `for f in a b`, `done`, `fi`: the control syntax of a loop or a branch runs
		// nothing by itself. What it wraps is a segment of its own and is judged as one.
		return Decision{}, true, false
	}
	name, args, err := readonly.SplitCommand(strings.Join(words, " "))
	if err != nil {
		// A segment that still cannot be read: an unclosed quote, a trailing
		// backslash. It is offered whole, or refused in strict mode, with the
		// tokeniser's own reason.
		return m.unclassified(fmt.Sprintf("%s: this part of the line cannot be read as one "+
			"program with arguments", err), "line-unreadable"), false, true
	}
	if missing, hit := m.missingTool(name); hit {
		return missing, false, false
	}
	return m.Decide(name, args, dir), false, false
}

// shellKeywords are the words that shape a compound command and are not programs. A loop such
// as `for f in server/test/*.mjs; do head -70 "$f"; done` reaches the segment reader as three
// segments whose first words are `for`, `do` and `done`; taking those for unknown programs made
// every read-only loop a question to the user, and on a run with nobody at the keyboard that
// question stops the whole task. Measured on a real session: the same loop asked twice, for 85 s
// and 14 s, in a run whose every other command was a plain read.
var (
	// keywordsBeforeCommand are followed by the command they introduce, which is judged on its own.
	keywordsBeforeCommand = map[string]bool{"do": true, "then": true, "else": true, "elif": true,
		"if": true, "while": true, "until": true, "!": true, "time": true}
	// keywordsAlone carry no command: they open or close the construct, or list what it iterates.
	keywordsAlone = map[string]bool{"done": true, "fi": true, "esac": true, "in": true}
)

// stripShellKeyword removes the control word a segment starts with. It reports syntax=true when
// nothing executable is left: a bare `done`, or the header `for NAME in WORDS`, whose words are
// data (a substitution inside them is stopped earlier, by the floor scan of the raw line).
func stripShellKeyword(words []string) (rest []string, syntax bool) {
	for len(words) > 0 {
		w := words[0]
		switch {
		case keywordsAlone[w]:
			return nil, true
		case w == "for" || w == "select" || w == "case":
			return nil, true
		case keywordsBeforeCommand[w]:
			words = words[1:]
		default:
			return words, false
		}
	}
	return nil, true
}

// worse returns the more cautious of two verdicts. Deny beats Ask beats Allow, and the
// reason of the winner travels with it so the user is told the reason that actually decided.
//
// On a tie the FIRST verdict is kept: there is nothing to prefer between two Allows, and
// inventing a reason for a line would be worse than reporting the one a segment gave.
func worse(a, b Decision) Decision {
	if b.Verdict > a.Verdict {
		return b
	}
	return a
}

// redirectTarget decides the segment that follows a redirection: `> file`, `>> log`,
// `2> errors`.
//
// A redirection writes, and the policy's job is to notice when what it writes to is not the
// workspace. Two cases are worse than that and are refused outright: a device — `> /dev/sda`
// destroys a disk as thoroughly as `dd` does, and it is one keystroke — and anything that
// resolves to a system tree.
func (m Mode) redirectTarget(seg lineSegment, dir string) Decision {
	// `2>&1`, `>&2`, `-` are file-descriptor plumbing, not files.
	target := unquote(strings.TrimSpace(strings.Join(seg.words, " ")))
	if target == "" || strings.HasPrefix(target, "&") || target == "-" {
		return Decision{Allow, "the redirection does not name a file", "line-redirect-fd", false}
	}
	// An INPUT redirection reads: `grep x < file` is a reader, and the only thing that
	// could go wrong is reading something the user did not mean, which the sandbox already
	// bounds. It is not a write and must not be judged as one — `< /etc/shadow` in the
	// output would be a false alarm here, and the real read is refused elsewhere if the
	// permissions say so.
	if strings.HasPrefix(seg.op, "<") {
		return m.unconfinedDecision(Decision{Allow, fmt.Sprintf("the line reads from %q", target),
			"line-redirect-input", false}, []string{target}, dir)
	}
	if strings.HasPrefix(target, "/dev/") {
		// The null and stream devices are not files: `> /dev/null` is the most common
		// redirect in the world and it destroys nothing. Refusing it would be the kind of
		// false alarm that teaches a user to turn the policy off.
		if isHarmlessDevice(target) {
			return Decision{Allow, fmt.Sprintf("output is discarded into %q", target), "line-redirect-devnull", false}
		}
		return Decision{
			Verdict: Deny,
			Reason: fmt.Sprintf("refused: writing to the device %q destroys its contents, "+
				"and no setting turns this into an allowed action", target),
			Rule:      "mandatory-device-write",
			Mandatory: true,
		}
	}
	if isRootWriteTarget(target) {
		return Decision{
			Verdict: Deny,
			Reason: fmt.Sprintf("refused: writing to %q would overwrite a system file, "+
				"and no setting turns this into an allowed action", target),
			Rule:      "mandatory-system-write",
			Mandatory: true,
		}
	}
	if outside := firstOutside([]string{target}, dir); outside != "" {
		return m.fullDiskDecision(Decision{Ask, fmt.Sprintf("the line writes to %q, which is outside the "+
			"directory this task works in (%s)", outside, dir), "write-outside-workspace", false}, []string{target})
	}
	return Decision{Allow, fmt.Sprintf("the line writes inside the workspace (%s)", dir),
		"write-inside-workspace", false}
}

// lineSegment is one piece of a command line: the words of one command, or the target of
// one redirection.
type lineSegment struct {
	words []string
	// redirect is true when the segment came from a redirection, so its words are a
	// DESTINATION and not a program.
	redirect bool
	// op is the operator that produced a redirect segment, or the separator that produced
	// a command one.
	op string
}

// lineSegments splits a line into the pieces a shell would run or open separately: the
// COMMANDS of the line, and the REDIRECTIONS it performs.
//
// It is a character scanner rather than a regexp or a chain of splits, and that is
// deliberate, because each of the three shapes that break a naive split has to survive it:
//
//   - quotes must be preserved, so `grep -n "two words" f | wc -l` is two commands and the
//     quoted argument is still one word when the command is re-read;
//   - a redirection's target must stay attached to its operator, so `2> errors.log` is a
//     file and not a program called `errors.log`;
//   - the words AROUND a redirection belong to the same command, so `echo hi > f more` is
//     one command with two arguments and one redirection, not two commands.
//
// It never fails. Every piece of a line can be described as a segment; whether a segment is
// acceptable is the caller's decision, and leaving that to the caller is what lets the same
// line be refused in strict mode and asked about in the default one.
func lineSegments(line string) []lineSegment {
	var s segmentScanner
	for _, r := range line {
		s.feed(r)
	}
	if s.op != "" {
		s.takeTarget()
	}
	s.flushCommand("")
	return s.out
}

// segmentScanner is the state of lineSegments while it reads a line.
type segmentScanner struct {
	out       []lineSegment
	words     []string
	redirects []lineSegment
	current   strings.Builder
	quote     rune
	escaped   bool
	// op is the redirection being read, non-empty from its `>` until its target has
	// been taken. An `&` reference (`2>&1`) closes it on the spot, because its
	// destination is part of the operator and there is no file to come.
	op string
	// fdRef is true right after the `&` of `2>&1`: the descriptor that follows belongs to
	// the redirection. Left in the stream, `npm ci 2>&1` reached the command as `npm ci 1`.
	fdRef bool
}

// feed reads one character of the line.
func (s *segmentScanner) feed(r rune) {
	if s.fdRef {
		s.fdRef = false
		if (r >= '0' && r <= '9') || r == '-' {
			return
		}
	}
	switch {
	case s.escaped:
		s.current.WriteRune(r)
		s.escaped = false
	case r == '\\' && s.quote != '\'':
		s.escaped = true
	case s.quote != 0:
		s.quoted(r)
	case r == '\'' || r == '"':
		s.current.WriteRune(r)
		s.quote = r
	case r == '>':
		s.redirectOutput(r)
	case r == '<':
		s.flushWord()
		s.op += string(r)
	case r == '&' && s.op != "":
		s.descriptorReference()
	case r == ' ' || r == '\t':
		s.blank()
	case isBreakRune(r):
		if s.op != "" {
			s.takeTarget()
		}
		s.flushCommand(string(r))
	default:
		s.current.WriteRune(r)
	}
}

// quoted reads a character inside quotes.
//
// The quote characters are KEPT in the word. The segment is re-read as a command afterwards, and
// `grep -n "two words" f` only produces one argument if the quoting survives the round trip:
// stripping it here would hand the tokeniser five words and change what the program is given.
func (s *segmentScanner) quoted(r rune) {
	s.current.WriteRune(r)
	if r == s.quote {
		s.quote = 0
	}
}

// redirectOutput reads a `>`.
func (s *segmentScanner) redirectOutput(r rune) {
	if isDigits(s.current.String()) {
		// A file descriptor on the left of the operator (`2>`, `1>>`): it belongs
		// to the redirection, not to the command's arguments.
		s.op += s.current.String()
		s.current.Reset()
	} else {
		s.flushWord()
	}
	s.op += string(r)
}

// descriptorReference reads the `&` of `2>&1`, `>&2`, `>&-`: the destination is part of the
// operator, so the redirection is complete and takes no file.
func (s *segmentScanner) descriptorReference() {
	s.flushWord()
	s.redirects = append(s.redirects, lineSegment{redirect: true, op: s.op + "&"})
	s.op = ""
	s.fdRef = true
}

// blank reads a space or a tab.
func (s *segmentScanner) blank() {
	if s.op != "" {
		// A word while a redirection is open is its target. A blank before any
		// word is just spacing.
		if s.current.Len() > 0 {
			s.takeTarget()
		}
		return
	}
	s.flushWord()
}

// flushWord ends the word being read.
func (s *segmentScanner) flushWord() {
	if s.current.Len() > 0 {
		s.words = append(s.words, s.current.String())
		s.current.Reset()
	}
}

// takeTarget closes a redirection with the word being read, which may be empty when the
// operator had no destination (`ls >` alone).
func (s *segmentScanner) takeTarget() {
	s.redirects = append(s.redirects, lineSegment{redirect: true, op: s.op, words: []string{s.current.String()}})
	s.current.Reset()
	s.op = ""
}

// flushCommand emits the command read so far, followed by the redirections that were
// interleaved with it. A command with no words and no redirections emits nothing.
func (s *segmentScanner) flushCommand(sep string) {
	s.flushWord()
	if len(s.words) > 0 {
		s.out = append(s.out, lineSegment{words: s.words, op: sep})
		s.words = nil
	}
	if len(s.redirects) > 0 {
		s.out = append(s.out, s.redirects...)
		s.redirects = nil
	}
}

// unquote strips one surrounding pair of quotes, which is what a path written inside quotes
// carries into the comparison.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// isDigits reports whether s is a non-empty run of digits, which is what a file descriptor
// glued to a redirection looks like.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isBreakRune reports the characters that start a new command rather than a new word.
//
// It is the same set the tokeniser refuses: one definition is what stops the policy's
// segmentation and the read-only policy from disagreeing about where a line divides.
func isBreakRune(r rune) bool {
	switch r {
	case '|', '&', ';', '\n', '\r', '(', ')', '`', '{', '}':
		return true
	default:
		return false
	}
}

// Relaxing is what the mode does to a verdict, in one place.
//
// It is separate from DecisionFor because it is a property of the MODE and not of the
// command: a caller holding a verdict (one it cached, or one a rule produced) has to be
// able to apply the same policy without re-deriving it, and the mandatory floor has to
// survive that path too. The only way a mandatory Deny becomes runnable is if this function
// is wrong — which is exactly why it is this small.
func (m Mode) Relaxing(d Decision) Decision {
	if d.Mandatory {
		return d
	}
	if !m.Enforce && d.Verdict == Ask {
		return Decision{Allow, d.Reason + " (approved automatically: the policy is not enforced)", d.Rule, false}
	}
	return d
}

// Decide is DecisionFor followed by Relaxing, which is what a caller almost always wants.
func (m Mode) Decide(command string, args []string, dir string) Decision {
	return m.Relaxing(m.DecisionFor(command, args, dir))
}

func baseName(command string) string {
	return strings.ToLower(filepath.Base(strings.TrimSpace(command)))
}

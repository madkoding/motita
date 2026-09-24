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
}

// DecisionFor decides one command.
//
// command is the program as written, args are its arguments, and dir is the directory the
// command runs in — the working directory the user pointed the agent at. dir is used, and
// not decoration: the difference between "writes inside my project" and "writes outside
// it" is the difference between work and damage, and it is the only judgement here that
// needs to know where.
func (m Mode) DecisionFor(command string, args []string, dir string) Decision {
	// Leading `VAR=value` assignments are the environment, not the program: `FOO=1 ls` runs
	// `ls`. The floor scan already skips them, and classifying them as a program name made the
	// policy ask about a plain reader.
	for strings.ContainsRune(command, '=') && len(args) > 0 && !strings.HasPrefix(command, "/") {
		command, args = args[0], args[1:]
	}
	kind, reason := readonly.Classify(command, args)
	name := baseName(command)
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
		// The writers whose effect reaches OUTSIDE the machine are asked about before the
		// path question, because they have no path to check: `git push`, `npm publish`,
		// `systemctl restart` and `curl -X DELETE` do not name a file, and their effect is
		// not undone by a backup.
		if reachesOutside(name, args) {
			return Decision{Ask, fmt.Sprintf("%q can reach outside this machine or change the system "+
				"as a whole, which is not something a task should do unannounced", baseName(command)),
				"external-effect", false}
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
	default:
		if reachesOutside(name, args) {
			return Decision{Ask, fmt.Sprintf("%q reaches outside this machine, and what it does there "+
				"cannot be checked from the command line", baseName(command)), "external-effect", false}
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
			"works in (%s)", baseName(command), outside, dir), "project-script-arg-outside", false}, true
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
	"deno": true, "bun": true, "java": true, "ts-node": true,
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
	"deno": true, "bun": true, "ts-node": true,
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
	"ts-node": {"-e": true},
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
	if sub, ok := subcommandScoped[name]; ok {
		return sub(firstNonFlag(args))
	}
	return true
}

// subcommandScoped holds the programs whose verdict depends on their subcommand. A program
// absent from this map is external whatever its arguments say.
var subcommandScoped = map[string]func(sub string) bool{
	// Version control: inspecting and committing locally is work; everything that leaves the
	// machine, rewrites history, or throws work away is not.
	"git": func(sub string) bool {
		switch sub {
		case "status", "log", "diff", "show", "branch", "remote", "tag", "describe",
			"rev-parse", "blame", "shortlog", "ls-files", "cat-file", "config", "grep",
			"whatchanged", "reflog", "show-ref", "for-each-ref", "count-objects",
			"add", "commit", "checkout", "switch", "restore", "stash", "merge", "rebase",
			"init", "clone", "worktree", "rm", "mv":
			return false
		}
		// push, reset, clean, filter-branch, gc, submodule … all reach somewhere the user
		// cannot see from here.
		return true
	},
	"hg":  func(sub string) bool { return sub != "status" && sub != "log" && sub != "diff" },
	"svn": func(sub string) bool { return sub != "status" && sub != "log" && sub != "diff" },

	// Package managers and toolchains: the reading and building verbs are local.
	"go": func(sub string) bool {
		switch sub {
		case "test", "build", "vet", "fmt", "list", "doc", "version", "env", "run", "generate":
			return false
		}
		return true
	},
	"npm": func(sub string) bool {
		switch sub {
		case "test", "run", "run-script", "exec", "ls", "list", "view", "outdated", "audit":
			return false
		}
		return true
	},
	"cargo": func(sub string) bool {
		switch sub {
		case "test", "build", "check", "clippy", "fmt", "run", "doc", "tree":
			return false
		}
		return true
	},
	"pip":  func(sub string) bool { return sub != "list" && sub != "show" && sub != "freeze" },
	"pip3": func(sub string) bool { return sub != "list" && sub != "show" && sub != "freeze" },
	"yarn": func(sub string) bool {
		switch sub {
		case "test", "run", "list", "why", "outdated":
			return false
		}
		return true
	},

	// Containers: the local inspection verbs are harmless, anything that runs or ships is not.
	"docker": func(sub string) bool {
		switch sub {
		case "ps", "images", "inspect", "logs", "version", "info":
			return false
		}
		return true
	},
	"kubectl": func(sub string) bool {
		switch sub {
		case "get", "describe", "logs", "version", "explain":
			return false
		}
		return true
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

	// The line is judged by its WORST segment, and the seed is the first segment's own
	// verdict rather than a guess about the line. A seed of "it only reads" would be a
	// claim nobody made, and it would win every comparison against an equally-permissive
	// segment (`touch f` inside the workspace) — the line would then be reported as a read
	// that changed nothing while a file was created.
	var (
		worst Decision
		found bool
	)
	for _, seg := range lineSegments(line) {
		var d Decision
		if seg.redirect {
			d = m.redirectTarget(seg, dir)
		} else {
			name, args, err := readonly.SplitCommand(strings.Join(seg.words, " "))
			if err != nil {
				// A segment that still cannot be read: an unclosed quote, a trailing
				// backslash. It is offered whole, or refused in strict mode, with the
				// tokeniser's own reason.
				return m.unclassified(fmt.Sprintf("%s: this part of the line cannot be read as one "+
					"program with arguments", err), "line-unreadable")
			}
			d = m.Decide(name, args, dir)
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
		return Decision{Allow, fmt.Sprintf("the line reads from %q", target), "line-redirect-input", false}
	}
	if strings.HasPrefix(target, "/dev/") {
		// The null and stream devices are not files: `> /dev/null` is the most common
		// redirect in the world and it destroys nothing. Refusing it would be the kind of
		// false alarm that teaches a user to turn the policy off.
		if isHarmlessDevice(target) {
			return Decision{Allow, fmt.Sprintf("output is discarded into %q", target), "line-redirect-devnull", false}
		}
		return Decision{
			Verdict:   Deny,
			Reason:    fmt.Sprintf("refused: writing to the device %q destroys its contents, and no setting turns this into an allowed action", target),
			Rule:      "mandatory-device-write",
			Mandatory: true,
		}
	}
	if isRootWriteTarget(target) {
		return Decision{
			Verdict:   Deny,
			Reason:    fmt.Sprintf("refused: writing to %q would overwrite a system file, and no setting turns this into an allowed action", target),
			Rule:      "mandatory-system-write",
			Mandatory: true,
		}
	}
	if outside := firstOutside([]string{target}, dir); outside != "" {
		return Decision{Ask, fmt.Sprintf("the line writes to %q, which is outside the directory this "+
			"task works in (%s)", outside, dir), "write-outside-workspace", false}
	}
	return Decision{Allow, fmt.Sprintf("the line writes inside the workspace (%s)", dir), "write-inside-workspace", false}
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
	var (
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
	)

	flushWord := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	// takeTarget closes a redirection with the word being read, which may be empty when the
	// operator had no destination (`ls >` alone).
	takeTarget := func() {
		redirects = append(redirects, lineSegment{redirect: true, op: op, words: []string{current.String()}})
		current.Reset()
		op = ""
	}
	// flushCommand emits the command read so far, followed by the redirections that were
	// interleaved with it. A command with no words and no redirections emits nothing.
	flushCommand := func(sep string) {
		flushWord()
		if len(words) > 0 {
			out = append(out, lineSegment{words: words, op: sep})
			words = nil
		}
		if len(redirects) > 0 {
			out = append(out, redirects...)
			redirects = nil
		}
	}

	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			// The quote characters are KEPT in the word. The segment is re-read as a
			// command afterwards, and `grep -n "two words" f` only produces one argument if
			// the quoting survives the round trip: stripping it here would hand the
			// tokeniser five words and change what the program is given.
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			current.WriteRune(r)
			quote = r
		case r == '>':
			if isDigits(current.String()) {
				// A file descriptor on the left of the operator (`2>`, `1>>`): it belongs
				// to the redirection, not to the command's arguments.
				op += current.String()
				current.Reset()
			} else {
				flushWord()
			}
			op += string(r)
		case r == '<':
			flushWord()
			op += string(r)
		case r == '&' && op != "":
			// `2>&1`, `>&2`, `>&-`: the destination is part of the operator, so the
			// redirection is complete and takes no file.
			flushWord()
			redirects = append(redirects, lineSegment{redirect: true, op: op + "&"})
			op = ""
		case r == ' ' || r == '	':
			if op != "" {
				// A word while a redirection is open is its target. A blank before any
				// word is just spacing.
				if current.Len() > 0 {
					takeTarget()
				}
				continue
			}
			flushWord()
		case isBreakRune(r):
			if op != "" {
				takeTarget()
			}
			flushCommand(string(r))
		default:
			current.WriteRune(r)
		}
	}
	if op != "" {
		takeTarget()
	}
	flushCommand("")
	return out
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
	}
	return false
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

// ---------------------------------------------------------------------------
// The mandatory floor
// ---------------------------------------------------------------------------

// MandatoryInLine refuses the floor commands found ANYWHERE in a line, including inside a
// quoted payload handed to a shell.
//
// It exists because the per-program check cannot see a line the tokeniser refuses and
// because a shell is the ordinary way to reach one: `sh -c 'rm -rf /'` never reaches the
// classifier as `rm`, and a rule that only fires on a well-formed command is a rule a
// malformed one walks past.
//
// It is a scan and not a parser, which is a deliberate trade. It looks for a floor program
// in COMMAND POSITION: the first token of a segment, after a wrapper like `sudo` or `env`,
// after `xargs`, after `find -exec`, or as the payload of `<shell> -c`. Tokens in argument
// position are ignored, so `echo "rm -rf /"` — a line that merely mentions the command — is
// not refused.
//
// False negatives are possible and are covered elsewhere: a floor command reached through a
// program this scan does not know still reaches the classifier as that program, which
// either refuses it (the writers list) or asks about it (unclassified). False positives are
// possible too, and they are the safe direction: the floor only ever refuses.
func MandatoryInLine(line, dir string) (Decision, bool) {
	return scanLine(line, dir)
}

// floorPrograms are the programs the floor knows. It is the membership test for the scan,
// and it must name everything mandatory() decides — see the test that holds the two
// together.
//
// `mv`, `cp`, `ln` and `install` are here for the same reason `rm` is: they reach the same
// tree from a different direction. Leaving them out would make the floor depend on which
// verb the model happened to choose, which is exactly the kind of hole a floor cannot have.
var floorPrograms = map[string]bool{
	"rm": true, "dd": true, "wipefs": true, "blkdiscard": true, "shred": true,
	"mv": true, "cp": true, "ln": true, "install": true,
	"mkfs": true, "mkfs.ext2": true, "mkfs.ext3": true, "mkfs.ext4": true,
	"mkfs.xfs": true, "mkfs.btrfs": true, "mkfs.vfat": true,
	"fdisk": true, "sfdisk": true, "cfdisk": true, "parted": true, "gdisk": true, "sgdisk": true,
	"shutdown": true, "reboot": true, "halt": true, "poweroff": true, "init": true,
}

// commandWrappers run another command: the command position follows them, so the scan keeps
// expecting a program after one and skips their own flags and `VAR=value` assignments.
var commandWrappers = map[string]bool{
	"sudo": true, "doas": true, "env": true, "nohup": true, "command": true,
	"nice": true, "ionice": true, "time": true, "stdbuf": true, "setsid": true,
}

// nestedIntroducers open a command position in the MIDDLE of a segment, which is how xargs
// and find's -exec reach a program.
var nestedIntroducers = map[string]bool{
	"xargs": true, "-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
}

func scanLine(line, dir string) (Decision, bool) {
	for _, segment := range segments(line) {
		if d, hit := scanSegment(segment, dir, false); hit {
			return d, true
		}
	}
	return Decision{}, false
}

// segments splits a line wherever a shell would start a new command: a separator, a pipe, a
// redirection, a substitution, or a quoted run inside one of those.
func segments(line string) [][]string {
	var out [][]string
	var current []string
	flush := func() {
		if len(current) > 0 {
			out = append(out, current)
			current = nil
		}
	}
	for _, token := range lineTokens(line) {
		if isSeparatorToken(token) {
			flush()
			continue
		}
		current = append(current, token)
	}
	flush()
	return out
}

// lineTokens splits a line into words, KEEPING the characters that separate commands.
//
// Ignoring quotes is deliberate: the payload of `sh -c 'rm -rf /'` is one shell word, and a
// quote-aware tokeniser would hand the scan the single token `rm -rf /` whose program name
// is none of the floor's. Keeping the separators is what lets the scan see that a line has a
// second command — dropping them, as a plain field split does, makes `ls; rm -rf /` look
// like one command called `ls` with five arguments.
func lineTokens(line string) []string {
	fields := strings.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '	' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		// A field can still hold separators glued to words (`ls;rm`), so it is split again
		// on them, with the separator emitted as its own token.
		var current strings.Builder
		for _, r := range f {
			if isSeparatorRune(r) {
				if current.Len() > 0 {
					out = append(out, strings.Trim(current.String(), `"'`))
					current.Reset()
				}
				out = append(out, string(r))
				continue
			}
			current.WriteRune(r)
		}
		if current.Len() > 0 {
			if w := strings.Trim(current.String(), `"'`); w != "" {
				out = append(out, w)
			}
		}
	}
	return out
}

func isSeparatorRune(r rune) bool {
	switch r {
	case ';', '|', '&', '(', ')', '`', '{', '}':
		return true
	}
	return false
}

// isSeparatorToken reports whether a token is a command separator.
//
// The separators arrive as their own token now, so this is a membership test rather than a
// search inside a word. The redirection operators are here too: `> file` ends the command
// that wrote to it, and the scan must not run its program name past it.
func isSeparatorToken(token string) bool {
	switch token {
	case ";", "|", "&", "(", ")", "`", "{", "}", "&&", "||", "\n", "\r",
		">", ">>", "<", "<<", "<>", ">&", "<&":
		return true
	}
	return false
}

// scanSegment walks one segment and reports a floor command in command position.
//
// fromNested is true when the program about to be read was named by an introducer — `xargs`
// or `find -exec` — which is what makes the difference between `rm -rf` typed at a prompt
// (which prints a usage error) and `rm -rf` fed by xargs (which deletes whatever it is
// handed). Only the second one is a floor command, and only the introducer says which it is.
func scanSegment(tokens []string, dir string, fromNested bool) (Decision, bool) {
	expectCommand := true
	// wrapper is the wrapper program in effect, and valueNext says the token before this
	// one was one of that wrapper's options which takes a value.
	//
	// It is tracked because skipping only the flags is not enough: `sudo -u root rm -rf /`
	// names its program AFTER the user it switches to, and a scan that treated `root` as
	// the program would walk straight past the floor. The operands of a wrapper are its
	// arguments, never the command the line runs.
	wrapper, valueNext := "", false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		name := baseName(token)

		if !expectCommand {
			// An argument: only a nested introducer moves the command position.
			if nestedIntroducers[name] {
				return scanSegment(tokens[i+1:], dir, true)
			}
			continue
		}

		// A nested introducer opens a command position wherever it appears, which is how
		// `xargs rm -rf` and `find . -exec rm {} ;` name their program.
		if nestedIntroducers[name] {
			return scanSegment(tokens[i+1:], dir, true)
		}

		// The value of a wrapper's own option — the `root` in `sudo -u root`, the `10` in
		// `nice -n 10`. It is a value, not the program.
		if valueNext {
			valueNext = false
			continue
		}
		if wrapperValueFlags[wrapper][token] {
			valueNext = true
			continue
		}
		// Flags and `VAR=value` assignments belong to the wrapper that preceded them.
		if strings.HasPrefix(token, "-") && token != "-" {
			continue
		}
		if strings.ContainsRune(token, '=') && !floorPrograms[name] {
			continue
		}
		if isDigits(token) {
			continue
		}

		if floorPrograms[name] {
			args := commandArgs(tokens[i+1:])
			// The operands in the LINE are what matters here, not the ones inside a
			// wrapper: `sh -c "rm -rf"` is caught by the shell branch below, but
			// `xargs rm -rf` reaches this point with `rm`'s own operands empty and its
			// target supplied at run time.
			if d, hit := mandatory(name, lineArgs(tokens, i+1), dir); hit {
				return d, true
			}
			// A destructive verb whose operands come from somewhere else: `xargs rm -rf`
			// names no target because xargs supplies it, and that is precisely the shape
			// that removes the wrong tree.
			if fromNested && destructiveVerb(name, args) {
				return mandatoryDeny(name, fmt.Sprintf("`%s` is given its target by the introducer "+
					"that names it, so what it destroys is not written anywhere in this line", token))
			}
			expectCommand = false
			continue
		}

		// A shell with -c takes the rest of the segment as a line of its own, which is
		// then scanned from its own command position.
		if isShellName(name) && i+1 < len(tokens) && tokens[i+1] == "-c" {
			return scanSegment(tokens[i+2:], dir, fromNested)
		}

		// The wrapper's program. It keeps the command position open for what follows.
		if commandWrappers[name] {
			wrapper, valueNext = name, false
			continue
		}

		expectCommand = false
	}
	return Decision{}, false
}

// wrapperValueFlags lists, per wrapper, the options that consume the NEXT token as their
// value. Without it the value of a wrapper's option is mistaken for the program it wraps:
// `sudo -u root rm -rf /` would be read as a program called `root`, and the floor would be
// bypassed by choosing a different user to run as.
//
// A flag absent from this table is one that stands alone (`sudo -n`, `nice -f`), so the token
// after it is still in command position.
var wrapperValueFlags = map[string]map[string]bool{
	"sudo": {
		"-u": true, "--user": true, "-g": true, "--group": true,
		"-p": true, "--prompt": true, "-C": true, "--close-from": true,
		"-h": true, "--host": true, "-r": true, "--role": true,
		"-t": true, "--type": true, "-R": true, "--chroot": true,
		"-T": true, "--command-timeout": true,
	},
	"doas": {"-u": true, "-C": true},
	"nice": {"-n": true, "--adjustment": true},
	"ionice": {"-c": true, "--class": true, "-n": true, "--classdata": true,
		"-p": true, "--pid": true},
	"stdbuf": {"-i": true, "--input": true, "-o": true, "--output": true,
		"-e": true, "--error": true},
	"time": {"-f": true, "--format": true, "-o": true, "--output": true},
	"env": {"-u": true, "--unset": true, "-C": true, "--chdir": true,
		"-S": true, "--split-string": true},
	// `nohup` and `command` take no option with a value.
}

// destructiveVerb reports whether a floor program is being asked to destroy with no operand
// of its own, which only matters when an introducer supplies those operands.
//
// Only `rm` and `dd` are here, and that is not an omission: the other floor programs
// (`mkfs`, `shred`, `wipefs`, the partition editors) are refused by mandatory() whatever
// their operands say, so a line naming one of them never reaches this question. Keeping a
// case for them would be unreachable code that reads like a safety net.
func destructiveVerb(name string, args []string) bool {
	switch name {
	case "rm":
		return hasFlag(args, 'r') && hasFlag(args, 'f') && len(rmTargets(args)) == 0
	case "dd":
		return !hasArgWithPrefix(args, "of=")
	}
	return false
}

// hasFlag reports whether a short-option cluster contains a flag character.
func hasFlag(args []string, flag rune) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a, flag) {
			return true
		}
	}
	return false
}

// lineArgs returns everything after the program, stopping only at a command separator.
//
// It is the counterpart of commandArgs, and the difference between them is the difference
// between two questions. commandArgs asks "what arguments does THIS program get?" and stops
// at a wrapper or an introducer, because `sudo rm -rf /` hands `rm` no arguments at all — the
// wrapper that follows belongs to the next program. lineArgs asks "what does the line say
// about what this program touches?", and there the operands of a wrapper do count:
// `xargs rm -rf build` does name `build`, and refusing it as a target-less delete would be
// wrong about the line in front of the user.
//
// A separator ends both, because after one the words belong to another command.
func lineArgs(tokens []string, from int) []string {
	out := make([]string, 0, len(tokens)-from)
	for _, t := range tokens[from:] {
		if isSeparatorToken(t) {
			break
		}
		out = append(out, t)
	}
	return out
}

// commandArgs trims what follows a program down to its own arguments, stopping at the first
// token that begins another command. Without it, `rm -rf a; rm -rf /` would hand the first
// rm the arguments of the second.
func commandArgs(rest []string) []string {
	out := make([]string, 0, len(rest))
	for _, t := range rest {
		if isSeparatorToken(t) || nestedIntroducers[baseName(t)] || commandWrappers[baseName(t)] {
			break
		}
		out = append(out, t)
	}
	return out
}

func isShellName(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "csh", "tcsh", "fish", "ash", "busybox":
		return true
	}
	return false
}

// mandatory answers the commands that are refused whatever the operator chose.
//
// The test for membership is not "is this dangerous". Almost everything is dangerous
// somewhere, and a floor that grows to cover it becomes a policy nobody can work under,
// which is a policy everybody turns off. The test is narrower and it is two questions:
//
//	does it destroy something that cannot be recovered, and
//	is the honest answer of a program running in somebody's home directory "never"?
//
// By that test: erasing a filesystem, the system's power state, the partition table, and a
// recursive forced delete of a root-like path. Everything else — including `rm -rf` of a
// project directory, which is exactly what a cleanup task is asked to do — is a
// confirmation, not a floor.
//
// The floor is enforced structurally rather than by good intentions:
//
//   - MandatoryInLine checks it before anything is classified, so no argument trick reaches
//     it late and no shell hides it;
//   - Relaxing refuses to touch a Mandatory decision, so `enforce: false` cannot reach it;
//   - the config layer has no setting that names it (see config.Policy).
func mandatory(name string, args []string, dir string) (Decision, bool) {
	switch name {
	case "mkfs", "mkfs.ext2", "mkfs.ext3", "mkfs.ext4", "mkfs.xfs", "mkfs.btrfs", "mkfs.vfat":
		return mandatoryDeny(name, "formatting a filesystem destroys everything on it and cannot be undone")
	case "fdisk", "sfdisk", "cfdisk", "parted", "gdisk", "sgdisk":
		return mandatoryDeny(name, "editing the partition table can destroy every filesystem on the disk")
	case "dd":
		// dd with an output target overwrites it, block for block, with no undo and no
		// confirmation of its own. A dd that only reads (`if=` and no `of=`) is untouched.
		if hasArgWithPrefix(args, "of=") {
			return mandatoryDeny(name, "writing to a device or file with dd overwrites it block for block")
		}
	case "shutdown", "reboot", "halt", "poweroff", "init":
		return mandatoryDeny(name, "the machine's power state is not the agent's to decide")
	case "wipefs", "blkdiscard", "shred":
		return mandatoryDeny(name, "this destroys data on a device with no recovery")
	case "rm":
		// A recursive, forced delete of a root-like path. This is the case the floor exists
		// for: `rm -rf .` inside a workspace is a confirmation, and `rm -rf /` is not a
		// thing anybody means to do.
		if reason, bad := recursiveRootRemoval(args, dir); bad {
			return mandatoryDeny(name, reason)
		}
	case "mv", "cp", "install", "ln":
		// The same command with a root-like target is the same destruction by another
		// route: `mv / x` and `ln -s / enlace` are not work, and neither is overwriting the
		// tree that contains the machine.
		if reason, bad := rootLikeTarget(args, dir); bad {
			return mandatoryDeny(name, reason)
		}
	}
	return Decision{}, false
}

// rootLikeTarget reports whether any operand of the command names a tree the agent must not
// touch, whatever the verb is. `rm` and `mv` reach the same place from different directions.
func rootLikeTarget(args []string, dir string) (string, bool) {
	for _, target := range rmTargets(args) {
		if isRootLike(target, dir) {
			return fmt.Sprintf("the command targets %q, which is not a directory inside the workspace "+
				"but a tree the agent must not touch", target), true
		}
	}
	return "", false
}

func mandatoryDeny(command, reason string) (Decision, bool) {
	return Decision{
		Verdict:   Deny,
		Reason:    fmt.Sprintf("refused: %s (`%s`). This is not something the policy can be configured to allow", reason, command),
		Rule:      "mandatory",
		Mandatory: true,
	}, true
}

// hasArgWithPrefix reports whether any argument starts with prefix, which is how dd's `of=`
// target is found without parsing the whole operand grammar.
func hasArgWithPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

// recursiveRootRemoval reports whether this is an `rm -r -f` whose target is a root, a home
// directory, or the tree above the workspace.
//
// A relative "." is NOT root-like: it is the project the user pointed the agent at,
// deleting it is a thing people ask for, and the workspace rule treats it as ordinary
// consequential work.
func recursiveRootRemoval(args []string, dir string) (string, bool) {
	recursive, force := false, false
	for _, a := range args {
		switch {
		case a == "--recursive":
			recursive = true
		case a == "--force":
			force = true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--"):
			if strings.ContainsRune(a, 'r') || strings.ContainsRune(a, 'R') {
				recursive = true
			}
			if strings.ContainsRune(a, 'f') {
				force = true
			}
		}
	}
	if !recursive || !force {
		return "", false
	}
	for _, target := range rmTargets(args) {
		if isRootLike(target, dir) {
			return fmt.Sprintf("`rm` deletes %q, which is not a directory inside the workspace but a "+
				"tree the agent must not remove", target), true
		}
	}
	return "", false
}

// rmTargets returns the operands of an rm-like command: everything that is not the program
// and not an option. A `--` terminator is honoured, because `rm -rf -- -weird-name` has one
// operand and it is not an option.
func rmTargets(args []string) []string {
	var out []string
	afterTerminator := false
	for _, a := range args {
		if afterTerminator {
			out = append(out, a)
			continue
		}
		if a == "--" {
			afterTerminator = true
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// isRootLike reports whether a path names something whose deletion is not local work.
//
// The comparison is on the RESOLVED path, because `..` and a symlink are the two ways a
// path that reads as local points at something that is not. A path that cannot be resolved
// (it does not exist) is compared as written: there is nothing to resolve, and refusing to
// decide would be the wrong way round for a rule whose whole job is to be sure.
func isRootLike(target, dir string) bool {
	t := strings.TrimSpace(target)
	if t == "" {
		return false
	}
	if t == "/" || t == "/*" {
		return true
	}
	// A home directory is never local work, whether it is written with a tilde or with its
	// path. The tilde is checked as written because resolving it would mean asking which
	// user the agent runs as, and that is exactly the thing that must not decide.
	if t == "~" || t == "~/" || strings.HasPrefix(t, "~/") {
		return true
	}
	abs := t
	if !filepath.IsAbs(abs) && dir != "" {
		abs = filepath.Join(dir, abs)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)

	switch abs {
	case "/", "/home", "/root", "/etc", "/usr", "/var", "/bin", "/boot", "/lib", "/opt", "/srv":
		return true
	}
	// A home directory itself, which is the other thing that is never local work.
	if rest, ok := strings.CutPrefix(abs, "/home/"); ok && !strings.ContainsRune(rest, '/') {
		return true
	}
	// The tree above the workspace: its parent, and everything further up. A path inside
	// the workspace is local work; the directory that CONTAINS the work is not.
	if dir != "" {
		base := dir
		if resolved, err := filepath.EvalSymlinks(base); err == nil {
			base = resolved
		}
		base = filepath.Clean(base)
		if abs != base && strings.HasPrefix(base, abs+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Where a write lands
// ---------------------------------------------------------------------------

// writingForm decides the writers whose arguments say WHERE the change goes.
//
// The rule is the one a person would use: inside the directory the user pointed the agent
// at is work; outside it is a confirmation. The workspace is not a wall — the agent is
// often asked to touch a sibling — but crossing it is exactly the moment a person would
// want to be asked, and it is the only moment this package is trying to find.
//
// It returns false when the command's arguments do not locate the change at all, leaving
// the caller to give the unclassified answer. That keeps "I could not tell" separate from
// "I checked and it is outside".
func writingForm(command string, args []string, dir string) (Decision, bool) {
	name := baseName(command)
	targets := writeTargets(name, args)
	if len(targets) == 0 {
		return Decision{}, false
	}
	if outside := firstOutside(targets, dir); outside != "" {
		return Decision{Ask, fmt.Sprintf("%q writes to %q, which is outside the directory this task "+
			"works in (%s)", name, outside, dir), "write-outside-workspace", false}, true
	}
	return Decision{Allow, fmt.Sprintf("%q writes inside the workspace (%s)", name, dir), "write-inside-workspace", false}, true
}

// writeTargets extracts the operands of the writers whose destination is in their
// arguments. The ones whose destination is decided by flags (`git` writing the index,
// `systemctl` changing a unit) are not here: they have no operand that locates the change,
// and they are answered by the unclassified rule rather than being guessed at.
func writeTargets(name string, args []string) []string {
	operands := rmTargets(args)
	switch name {
	case "chmod", "chown", "chgrp":
		// The first operand is a mode or an owner, not a path: reading `chmod 644` as
		// "writes to the file 644" is how a policy invents a target and then reports it
		// as being outside the workspace.
		if len(operands) > 0 {
			operands = operands[1:]
		}
	case "rm", "rmdir", "mkdir", "touch", "truncate", "tee", "mkfifo", "mknod":
		// Everything the command names is written to, which is what rmTargets already
		// returned.
	case "mv", "cp", "ln", "install":
		// The LAST operand is where it lands; the ones before it are what is moved, and
		// moving something is harmless next to writing where it is moved to. A flag's own
		// value (`install -m 755 a dst`) sits before the destination and does not change
		// which operand is last.
		if len(operands) > 0 {
			operands = operands[len(operands)-1:]
		}
	default:
		return nil
	}
	return operands
}

// isRootWriteTarget decides whether a path that is about to be WRITTEN is one the agent must
// never write, whatever the operator chose.
//
// It is the write-side counterpart of isRootLike, and it is separate because the two ask
// different questions: isRootLike asks "may this be deleted", which is about trees; this asks
// "may this be overwritten", which is about a single file inside a system tree. `/etc/passwd`
// is the second without being the first.
func isRootWriteTarget(target string) bool {
	t := strings.TrimSpace(target)
	if t == "" {
		return false
	}
	if isHarmlessDevice(t) {
		return false
	}
	if t == "/" || t == "~" || t == "~/" || strings.HasPrefix(t, "~/") {
		return true
	}
	abs := t
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	for _, tree := range []string{"/etc", "/usr", "/bin", "/sbin", "/boot", "/lib", "/lib64", "/var", "/root", "/opt", "/srv"} {
		if abs == tree || strings.HasPrefix(abs, tree+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// isHarmlessDevice reports whether a device path is one of the streams or the null device,
// where writing goes nowhere or straight to a terminal the user is looking at.
//
// The distinction matters because the alternative is a policy that refuses `> /dev/null`,
// which appears in shell code constantly, and a policy that cries wolf on the common case is
// switched off before it ever protects anything.
func isHarmlessDevice(path string) bool {
	switch strings.TrimSuffix(path, "/") {
	case "/dev/null", "/dev/zero", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/full":
		return true
	}
	// `/dev/fd/N` and `/dev/pts/N` are a descriptor and a terminal, not a disk.
	return strings.HasPrefix(path, "/dev/fd/") || strings.HasPrefix(path, "/dev/pts/")
}

// firstOutside returns the first target that resolves outside dir, or "" when every one of
// them is inside.
//
// A target that does not exist yet (a file about to be created) is resolved as far as it
// can be: the deepest ancestor that does exist is followed through its links, and the
// remaining path is re-joined to it. Stopping at "the file is not there, so use it as
// written" is how a symlink INSIDE the workspace becomes a way to write outside it — the
// link is the part that exists, and it is the part that says where the write really lands.
func firstOutside(targets []string, dir string) string {
	if dir == "" {
		return ""
	}
	base := filepath.Clean(dir)
	if !filepath.IsAbs(base) {
		// firstOutside compares ABSOLUTE paths, so a relative workspace is resolved against
		// the process's own directory first. A caller is expected to hand in an absolute
		// workspace; this keeps the comparison from silently treating every target as
		// outside when it does not.
		if abs, err := filepath.Abs(base); err == nil {
			base = abs
		}
	}
	// The workspace itself is resolved once: a workspace reached through a link must not
	// make every path inside it look like it is outside.
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	for _, t := range targets {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "~") {
			// A home path written with a tilde is outside by construction.
			return t
		}
		abs := t
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, abs)
		}
		abs = resolveAsFarAsPossible(abs)
		if abs != base && !strings.HasPrefix(abs, base+string(filepath.Separator)) {
			return t
		}
	}
	return ""
}

// resolveAsFarAsPossible resolves the links of the deepest part of a path that exists, and
// re-joins the part that does not.
//
// The walk is bounded by the root, and the root is always resolvable, so the loop always
// terminates by finding an ancestor that exists: either the path exists, or one of its
// parents does, or the root does.
func resolveAsFarAsPossible(abs string) string {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	remainder := ""
	current := filepath.Clean(abs)
	for {
		parent := filepath.Dir(current)
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Clean(filepath.Join(resolved, remainder))
		}
	}
}

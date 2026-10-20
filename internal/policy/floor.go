package policy

import (
	"fmt"
	"path/filepath"
	"strings"
)

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
		out = appendFieldTokens(out, f)
	}
	return out
}

// appendFieldTokens appends the tokens of one blank-separated field. A field can still hold
// separators glued to words (`ls;rm`), so it is split again on them, with the separator emitted
// as its own token.
func appendFieldTokens(out []string, f string) []string {
	var current strings.Builder
	for _, r := range f {
		if !isSeparatorRune(r) {
			current.WriteRune(r)
			continue
		}
		if current.Len() > 0 {
			out = append(out, strings.Trim(current.String(), `"'`))
			current.Reset()
		}
		out = append(out, string(r))
	}
	if w := strings.Trim(current.String(), `"'`); w != "" {
		out = append(out, w)
	}
	return out
}

func isSeparatorRune(r rune) bool {
	switch r {
	case ';', '|', '&', '(', ')', '`', '{', '}':
		return true
	default:
		return false
	}
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
	default:
		return false
	}
}

// scanSegment walks one segment and reports a floor command in command position.
//
// fromNested is true when the program about to be read was named by an introducer — `xargs`
// or `find -exec` — which is what makes the difference between `rm -rf` typed at a prompt
// (which prints a usage error) and `rm -rf` fed by xargs (which deletes whatever it is
// handed). Only the second one is a floor command, and only the introducer says which it is.
func scanSegment(tokens []string, dir string, fromNested bool) (Decision, bool) {
	s := floorScan{tokens: tokens, dir: dir, fromNested: fromNested, expectCommand: true}
	for i := range tokens {
		if d, hit, final := s.step(i); final {
			return d, hit
		}
	}
	return Decision{}, false
}

// floorScan is the state of scanSegment while it walks a segment.
type floorScan struct {
	tokens        []string
	dir           string
	fromNested    bool
	expectCommand bool
	// wrapper is the wrapper program in effect, and valueNext says the token before this
	// one was one of that wrapper's options which takes a value.
	//
	// It is tracked because skipping only the flags is not enough: `sudo -u root rm -rf /`
	// names its program AFTER the user it switches to, and a scan that treated `root` as
	// the program would walk straight past the floor. The operands of a wrapper are its
	// arguments, never the command the line runs.
	wrapper   string
	valueNext bool
}

// step reads the token at i. final reports that the answer for the whole segment is known.
func (s *floorScan) step(i int) (d Decision, hit, final bool) {
	token := s.tokens[i]
	name := baseName(token)

	// A nested introducer opens a command position wherever it appears, which is how
	// `xargs rm -rf` and `find . -exec rm {} ;` name their program. In an argument it is the
	// only thing that moves the command position.
	if nestedIntroducers[name] {
		d, hit = scanSegment(s.tokens[i+1:], s.dir, true)
		return d, hit, true
	}
	if !s.expectCommand || s.optionValue(token) || notAProgram(token, name) {
		return Decision{}, false, false
	}

	if floorPrograms[name] {
		s.expectCommand = false
		d, hit = floorVerdict(s.tokens, i, s.dir, s.fromNested)
		return d, hit, hit
	}

	// A shell with -c takes the rest of the segment as a line of its own, which is
	// then scanned from its own command position.
	if isShellName(name) && i+1 < len(s.tokens) && s.tokens[i+1] == "-c" {
		d, hit = scanSegment(s.tokens[i+2:], s.dir, s.fromNested)
		return d, hit, true
	}

	// The wrapper's program. It keeps the command position open for what follows.
	if commandWrappers[name] {
		s.wrapper, s.valueNext = name, false
		return Decision{}, false, false
	}

	s.expectCommand = false
	return Decision{}, false, false
}

// optionValue reports whether a token belongs to the options of the wrapper in effect: the
// value of one of them — the `root` in `sudo -u root`, the `10` in `nice -n 10` — or an option
// whose value comes next. It is a value, not the program.
func (s *floorScan) optionValue(token string) bool {
	if s.valueNext {
		s.valueNext = false
		return true
	}
	if wrapperValueFlags[s.wrapper][token] {
		s.valueNext = true
		return true
	}
	return false
}

// notAProgram reports a token in command position that cannot be the program: flags and
// `VAR=value` assignments belong to the wrapper that preceded them, and a bare number is a
// wrapper's value.
func notAProgram(token, name string) bool {
	if strings.HasPrefix(token, "-") && token != "-" {
		return true
	}
	if strings.ContainsRune(token, '=') && !floorPrograms[name] {
		return true
	}
	return isDigits(token)
}

// floorVerdict judges a floor program named at tokens[i].
func floorVerdict(tokens []string, i int, dir string, fromNested bool) (Decision, bool) {
	token := tokens[i]
	name := baseName(token)
	args := commandArgs(tokens[i+1:])
	// The operands in the LINE are what matters here, not the ones inside a
	// wrapper: `sh -c "rm -rf"` is caught by the shell branch, but
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
	default:
		return false
	}
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
	default:
		return false
	}
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
	default:
		// Any other program is not on the floor.
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
		Verdict: Deny,
		Reason: fmt.Sprintf("refused: %s (`%s`). This is not something the policy can be configured to allow",
			reason, command),
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
	recursive, force := rmFlags(args)
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

// rmFlags reports whether rm's arguments ask for a recursive and for a forced delete, in their
// long or short (clustered) form.
func rmFlags(args []string) (recursive, force bool) {
	for _, a := range args {
		r, f := rmFlag(a)
		recursive = recursive || r
		force = force || f
	}
	return recursive, force
}

// rmFlag reports whether one argument of rm asks for a recursive or a forced delete.
func rmFlag(a string) (recursive, force bool) {
	switch {
	case a == "--recursive":
		return true, false
	case a == "--force":
		return false, true
	case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--"):
		return strings.ContainsRune(a, 'r') || strings.ContainsRune(a, 'R'), strings.ContainsRune(a, 'f')
	default:
		return false, false
	}
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

// isSystemRootOrHome reports whether a clean absolute path is a system root or a home directory
// itself: neither is ever local work.
func isSystemRootOrHome(p string) bool {
	switch p {
	case "/", "/home", "/root", "/etc", "/usr", "/var", "/bin", "/boot", "/lib", "/opt", "/srv":
		return true
	default:
		rest, ok := strings.CutPrefix(p, "/home/")
		return ok && !strings.ContainsRune(rest, '/')
	}
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
	paths := writtenAndResolved(abs)
	for _, p := range paths {
		if isSystemRootOrHome(p) {
			return true
		}
	}
	abs = paths[1]
	// The tree above the workspace: its parent, and everything further up. A path inside
	// the workspace is local work; the directory that CONTAINS the work is not.
	if dir != "" {
		base := writtenAndResolved(dir)[1]
		if abs != base && strings.HasPrefix(base, abs+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

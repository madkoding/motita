// THE TABLES, and the argument rules that make some of them two-sided.
//
// They live in their own file because the refusal message tells an operator to edit
// `internal/readonly/readers.go`, and a message that names a file which does not exist is
// the kind of small lie that costs somebody ten minutes at exactly the wrong moment.
//
// Every name here is a DELIBERATE act with a test. That is the property worth keeping: a
// program nobody has classified is refused (or asked about, in the wider policy), so the
// lists are the policy — not a cache of it.
package readonly

import (
	"fmt"
	"strings"
)

// writers are programs that change the system by nature. Refused by name.
//
// Members are chosen for what the NAME does, not for what a particular invocation does:
// `git commit` writes whatever else it is given, and `tee` writes wherever it is told.
// A program whose reading form is worth allowing does not belong here — it belongs in
// readers with a rule in argumentRules — because a name in this table is refused even
// when its arguments would have made it a read.
var writers = map[string]bool{
	"rm": true, "rmdir": true, "mv": true, "cp": true, "dd": true, "install": true,
	"touch": true, "truncate": true, "tee": true, "mkfifo": true, "mknod": true,
	"chmod": true, "chown": true, "chgrp": true, "ln": true,
	"mkdir": true, "mkfs": true, "fdisk": true, "parted": true, "mount": true,
	"umount": true, "swapon": true, "swapoff": true,
	"apt": true, "apt-get": true, "dpkg": true, "yum": true, "dnf": true,
	"pacman": true, "apk": true, "snap": true, "pip": true, "pip3": true,
	"npm": true, "yarn": true, "pnpm": true, "cargo": true, "gem": true,
	"useradd": true, "userdel": true, "usermod": true, "groupadd": true,
	"passwd": true, "su": true, "sudo": true, "visudo": true,
	"reboot": true, "shutdown": true, "halt": true, "poweroff": true, "init": true,
	"kill": true, "killall": true, "pkill": true,
	"systemd-run": true, "service": true, "crontab": true, "at": true,
	"iptables": true, "nft": true, "ufw": true, "firewall-cmd": true,
	"curl": true, "wget": true, // they write files and reach the network
	"ssh": true, "scp": true, "sftp": true, "rsync": true, "nc": true, "netcat": true,
	"docker": true, "podman": true, "kubectl": true, "helm": true,
	"vi": true, "vim": true, "nano": true, "emacs": true, "ed": true,
	"perl":   true, // `perl -i` rewrites in place, and -e carries a program
	"python": true, "python3": true, "node": true, "ruby": true, "php": true,
	"make": true, "gcc": true, "cc": true, "clang": true, "ld": true,
	"mvn": true, "gradle": true, "dotnet": true, "java": true,
}

// argumentRules are the commands that read or write depending on their arguments. Each one
// gets a rule that refuses the writing form with the reason, and it is consulted BEFORE the
// readers table — see the order in Classify.
//
// "Unknown is a write" is NOT the rule here, and the two SHAPES of rule differ in which way
// they lean, which is worth knowing before writing another one:
//
//   - A rule that COUNTS OPERANDS (`uniq`, `xxd`) cannot know whether an unrecognised flag
//     takes a value, so a separate value counts as an operand. That biases toward seeing a
//     write, which is the safe direction, and it is why `optionValues` exists: without an
//     entry, `xxd -l 16 f` would be refused as a two-operand write.
//   - A rule that SCANS FLAGS (`sed`, `awk`, `yq`, `xmllint`, `base64`, `sort`) treats a flag
//     it does not know as a READ, because the reading form is the common case and refusing
//     every unrecognised flag would interrupt the ordinary work of looking at a file. The
//     flags it does know are the writing ones, and the tests name each of them.
//
// Neither shape guesses about the program's TEXT: `sed 'w out' f` and `awk '{print > out}'`
// write and are not refused, because no lexical rule can tell them from a read. Those are
// recorded as gaps in the tests, not closed by a rule that would refuse real work.
var argumentRules = map[string]func([]string) (string, bool){
	// --- the line editors ---------------------------------------------------
	//
	// `sed` is the program a coding agent reaches for to look at a file — measured on real
	// sessions, `sed -n '1,300p' file` was typed 197 times — and it was in the writers
	// table, so every one of those reads stopped to ask. Its writing form is `-i`, and the
	// honest rule is therefore about `-i` rather than about the name.
	//
	// This is a rule about FLAGS, not about the script, and that is the right shape here:
	// the script's effect is only realised by an `-i` that writes it back. `sed 's/a/b/' f`
	// prints to stdout and is a read. A `w` command inside a script writes a file with or
	// without `-i`, and closing that would mean parsing sed; it is not closed, and it is
	// recorded as such rather than pretended away.
	"sed": func(args []string) (string, bool) {
		for _, a := range args {
			switch {
			case a == "--in-place":
				return "sed --in-place rewrites the files", true
			case strings.HasPrefix(a, "--in-place="):
				return "sed --in-place rewrites the files", true
			case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") &&
				strings.ContainsRune(a, 'i'):
				// `-i`, `-i.bak`, `-ni` (a cluster, which GNU sed really does accept), and
				// `-e`-less forms with the flag glued to its suffix.
				return fmt.Sprintf("sed %s rewrites the files", a), true
			}
		}
		return "", false
	},
	// `awk`'s own options cannot write, so the writing form is the PROGRAM: `print > "f"`
	// redirects, `system()` runs anything, and the GNU in-place extension writes. There is
	// no flag to look for, so the script is read — the opposite shape from the rule above,
	// which is why they are two rules and not one.
	"awk": awkRule,
	"find": func(args []string) (string, bool) {
		for _, a := range args {
			// -delete/-exec/-execdir/-ok/-fprint* turn find into a writer.
			switch {
			case a == "-delete":
				return `find -delete removes files`, true
			case a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir":
				return `find -exec runs an arbitrary command`, true
			case strings.HasPrefix(a, "-fprint"):
				return `find -fprint writes a file`, true
			}
		}
		return "", false
	},
	"git": func(args []string) (string, bool) {
		// Read-only git: inspecting a repository. Anything that writes the index,
		// the working tree or the remote is refused.
		reading := map[string]bool{
			"status": true, "log": true, "diff": true, "show": true, "branch": true,
			"remote": true, "tag": true, "describe": true, "rev-parse": true,
			"blame": true, "shortlog": true, "ls-files": true, "cat-file": true,
			"config": true, "grep": true, "whatchanged": true, "reflog": true,
			"show-ref": true, "for-each-ref": true, "count-objects": true,
		}
		sub := firstNonFlag(args)
		if sub == "" {
			return "", false
		}
		if !reading[sub] {
			return fmt.Sprintf("git %s can change the repository", sub), true
		}
		// `git config` reads with no value and writes with one:
		//   git config user.name           -> reads
		//   git config user.name "Someone" -> writes
		// A test found the plain writing form slipping through, which is why the
		// positional arguments are counted instead of only looking at the flags.
		if sub == "config" {
			for _, a := range args {
				switch {
				case a == "--add", a == "--unset", a == "--unset-all", a == "--edit",
					a == "--replace-all", a == "--rename-section", a == "--remove-section":
					return fmt.Sprintf("git config %s writes the configuration", a), true
				}
			}
			// Count the positionals after "config": one is a read, two are a write.
			positional := 0
			seenConfig := false
			for _, a := range args {
				if strings.HasPrefix(a, "-") {
					continue
				}
				if !seenConfig {
					if a == "config" {
						seenConfig = true
					}
					continue
				}
				positional++
			}
			if positional >= 2 {
				return "git config with a value writes the configuration", true
			}
		}
		return "", false
	},
	"systemctl": func(args []string) (string, bool) {
		sub := firstNonFlag(args)
		switch sub {
		case "status", "show", "list-units", "list-unit-files", "is-active",
			"is-enabled", "is-failed", "cat", "list-timers", "list-sockets",
			"get-default":
			return "", false
		}
		if sub == "" {
			return "", false
		}
		return fmt.Sprintf("systemctl %s can change the system", sub), true
	},
	"go": func(args []string) (string, bool) {
		sub := firstNonFlag(args)
		switch sub {
		case "version", "env", "list", "doc", "vet", "fmt":
			// `go vet` and `go fmt` are special: vet compiles into a cache and fmt
			// rewrites files. Both are refused below by the caller's rule on -w.
			if sub == "fmt" {
				for _, a := range args {
					if a == "-w" {
						return "go fmt -w rewrites the files", true
					}
				}
			}
			return "", false
		case "test":
			// Compiling into the build cache is not "changing the system": it is
			// what the check does, and refusing it would make plan mode useless for
			// the case this project is about. Writing a test binary out is refused.
			for _, a := range args {
				if a == "-c" || a == "-o" {
					return fmt.Sprintf("go test %s writes a binary", a), true
				}
			}
			return "", false
		case "build":
			return "go build writes a binary", true
		}
		if sub == "" {
			return "", false
		}
		return fmt.Sprintf("go %s can write to the module cache or the tree", sub), true
	},
	// --- the readers whose OPERANDS can write -------------------------------
	//
	// This block is the sweep that the `sed` defect made necessary: for each of these the
	// question "what would a person have to add to make it write?" has an answer, and the
	// answer was not in the table. `sort -o out.txt in.txt` and `uniq in.txt out.txt` were
	// reported as "only reads" and ran in SILENCE while writing a file — the same defect as
	// `sed`, in the direction that has no question to catch it.
	"sort": func(args []string) (string, bool) {
		if a, ok := writesTo(args, "-o", "--output"); ok {
			return fmt.Sprintf("sort %s writes a file", a), true
		}
		return "", false
	},
	"uniq": func(args []string) (string, bool) {
		// `uniq [OPTION]... [INPUT [OUTPUT]]`: a SECOND operand is an output file.
		if len(positionals("uniq", args)) >= 2 {
			return "uniq with an output file writes it", true
		}
		return "", false
	},
	"xxd": func(args []string) (string, bool) {
		// The synopsis is `xxd [options] [infile [outfile]]`, and the SECOND operand is
		// written whatever the flags — `xxd f out` writes out just as `xxd -r f out` does.
		// The first re-reading of this rule looked for `-r` and would have left the plain
		// two-operand form writing in silence, which is why the rule is about the operand.
		if len(positionals("xxd", args)) >= 2 {
			return "xxd with an output file writes it", true
		}
		return "", false
	},
	"xmllint": func(args []string) (string, bool) {
		// `--output FILE` and its short form write the parsed document out.
		if a, ok := writesTo(args, "-o", "--output"); ok {
			return fmt.Sprintf("xmllint %s writes a file", a), true
		}
		return "", false
	},
	// base64 and xmllint take an output file through a FLAG. Both were classified as "only
	// reads" with no rule, so `base64 -o out f` — where the tool supports it — would have
	// run in silence while writing. `grep -f file` is deliberately NOT here: in grep that
	// flag names a PATTERN file and reads it, and a rule that refused it would break the
	// commonest way to grep with a pattern list.
	"base64": func(args []string) (string, bool) {
		if a, ok := writesTo(args, "-o", "--output"); ok {
			return fmt.Sprintf("base64 %s writes a file", a), true
		}
		return "", false
	},
	// The awk family, one rule under three names: `gawk` and `mawk` are the same program,
	// and having them in the readers table with no rule is precisely the state `sed` was in.
	"gawk": awkRule,
	"mawk": awkRule,
	"yq": func(args []string) (string, bool) {
		// A query is a read; `-i` writes the result back into the input file.
		for _, a := range args {
			if a == "-i" || a == "--inplace" {
				return "yq -i rewrites the input file", true
			}
		}
		return "", false
	},
}

// awkRule is the rule for `awk`, `gawk` and `mawk` — one program under three names.
//
// Its own options cannot write, so the writing form is the PROGRAM: `print > "f"` redirects,
// `system()` runs anything, and the GNU in-place extension rewrites its input. There is no
// flag to look for in the common case, which is why this rule reads the script — the
// opposite shape from the `sed` rule above.
func awkRule(args []string) (string, bool) {
	for i, a := range args {
		// `-i inplace` (gawk) and the glued `-iinplace`.
		if a == "-i" && i+1 < len(args) && args[i+1] == "inplace" {
			return "awk -i inplace rewrites the files", true
		}
		if strings.HasPrefix(a, "-i") && strings.Contains(a, "inplace") {
			return "awk -i inplace rewrites the files", true
		}
		if strings.Contains(a, "system(") {
			return "awk with system() runs an arbitrary command", true
		}
	}
	// What writes inside the program is a redirection to a QUOTED target: `print > "out"`,
	// `printf ... >> "out"`, `print | "sort"`. The quote is what makes this narrow enough to
	// be worth having — `awk '$1 > 5'` and `awk 'a || b'` are ordinary comparisons, and a
	// rule that fired on the bare operator would refuse most real awk programs.
	if prog, ok := awkProgram(args); ok && awkRedirects(prog) {
		return "awk writes a file (a redirection in its program)", true
	}
	return "", false
}

// writesTo reports the first flag that names an output file, in either the separate form
// (`-o file`, `--output file`) or the glued one (`-ofile`, `--output=file`).
func writesTo(args []string, names ...string) (string, bool) {
	for _, a := range args {
		for _, flag := range names {
			switch {
			case a == flag:
				return flag, true
			case strings.HasPrefix(flag, "--") && strings.HasPrefix(a, flag+"="):
				return a, true
			case len(flag) == 2 && len(a) > 2 && strings.HasPrefix(a, flag):
				// -ofile
				return a, true
			}
		}
	}
	return "", false
}

// optionValues names, per program, the options that take a VALUE. It exists for the rules
// that count OPERANDS, and it is per program because the same letter means different things
// to different tools: without it, `xxd -l 16 in.bin` counts `16` as a second operand and
// reports a write that is not happening — a false alarm on one of xxd's commonest
// invocations, which is the kind of error that gets a policy switched off.
//
// A program whose rule counts operands MUST have an entry here; the tests for `xxd` and
// `uniq` cover exactly the flags that would otherwise be miscounted.
var optionValues = map[string]map[string]bool{
	"xxd": {
		"-c": true, "--cols": true,
		"-g": true, "--groupsize": true,
		"-l": true, "--len": true,
		"-n": true, "--name": true,
		"-o": true, "--offset": true,
		"-s": true, "--seek": true,
		"-R": true,
	},
	"uniq": {
		"-f": true, "--skip-fields": true,
		"-s": true, "--skip-chars": true,
		"-w": true, "--check-chars": true,
	},
}

// positionals returns the arguments that are operands rather than options, for the program
// named. It stops taking options at the first `--`, and it skips the VALUE of an option that
// takes one, because that value is not an operand.
//
// A bare `-` is counted as an operand rather than as an option: it is how these tools are
// told stdin or stdout, so `xxd - out` names an output and must be judged on it.
func positionals(name string, args []string) []string {
	var out []string
	ended := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case ended:
			out = append(out, a)
		case a == "--":
			ended = true
		case a == "-":
			// stdin or stdout, and an operand for the purposes of these rules.
			out = append(out, a)
		case strings.HasPrefix(a, "-"):
			if optionValues[name][a] {
				// Its value follows and is not an operand. A GLUED value (`-l16`) needs no
				// handling: it is a single token, and it is an option.
				i++
			}
		default:
			out = append(out, a)
		}
	}
	return out
}

// awkRedirects reports whether an awk program redirects output to a quoted target, which is
// the shape awk uses to write a file from inside a script.
//
// The quote is deliberate: awk's print redirection and its comparisons share the `>` token,
// so the only thing that separates `print > "out"` from `$1 > 5` is what follows it. A bare
// operator is left alone, and so is a redirection to an unquoted name, which no lexical rule
// can tell from a comparison. Both are known gaps; they are in the tests as gaps, because a
// rule that guesses here would refuse the awk programs a person actually writes.
func awkRedirects(prog string) bool {
	for i := 0; i < len(prog); i++ {
		switch prog[i] {
		case '>', '|':
			j := i + 1
			if j < len(prog) && prog[j] == '>' {
				j++
			}
			for j < len(prog) && prog[j] == ' ' {
				j++
			}
			if j < len(prog) && (prog[j] == '"' || prog[j] == '\'') {
				return true
			}
		}
	}
	return false
}

// awkProgram returns the awk program text, which is the first non-option argument that is
// not the value of an option.
func awkProgram(args []string) (string, bool) {
	// The options that take a VALUE, so the token after them is not the program.
	valueOptions := map[string]bool{"-f": true, "--file": true, "-v": true, "-F": true, "--field-separator": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if valueOptions[a] {
			i++
			continue
		}
		if a == "--" {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a, true
	}
	return "", false
}

// readers are programs that only read, ONCE their arguments have been through the rule
// above. A name here with no rule is one whose reading form is its whole life.
//
// The distinction between this table and writers is a decision about the NAME, and the
// question to ask a candidate is the one that found four unsound entries: "what would a
// person have to add to make this write?" If the answer is an argument list rather than a
// different program, it belongs here WITH a rule. If the answer is "nothing — the name
// itself writes", it belongs in writers.
var readers = map[string]bool{
	// files
	"cat": true, "head": true, "tail": true, "less": true, "more": true,
	"ls": true, "dir": true, "tree": true, "stat": true, "file": true,
	"wc": true, "nl": true, "tac": true, "rev": true, "fold": true,
	"cut": true, "paste": true, "join": true, "column": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"strings": true, "xxd": true, "od": true, "hexdump": true, "base64": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "sha512sum": true,
	"cksum": true, "sum": true, "cmp": true, "diff": true, "comm": true,
	"readlink": true, "realpath": true, "basename": true, "dirname": true,
	"sort": true, "uniq": true, "tr": true, "seq": true, "expr": true,
	"jq": true, "yq": true, "xmllint": true, "csvlook": true,
	// The line editors. Their reading form is what a coding agent lives on; their writing
	// form is a flag, and a flag is what a rule can name.
	"sed": true, "awk": true, "find": true, "gawk": true, "mawk": true,
	// system state
	"ps": true, "top": true, "free": true, "uptime": true, "vmstat": true,
	"iostat": true, "mpstat": true, "lsof": true, "pstree": true, "pidof": true,
	"uname": true, "hostname": true, "hostnamectl": true, "arch": true,
	"nproc": true, "getconf": true, "ldd": true,
	"df": true, "du": true, "lsblk": true, "blkid": true, "findmnt": true,
	"id": true, "whoami": true, "groups": true, "users": true, "who": true,
	"w": true, "last": true, "lastlog": true,
	"date": true, "cal": true, "locale": true, "timedatectl": true,
	// `env` and `command` are readers only in the forms that run nothing (bare `env`,
	// `command -v x`): with a program after them, Unwrap hands Classify that program instead.
	"env": true, "printenv": true, "echo": true, "printf": true,
	"which": true, "type": true, "whereis": true, "command": true,
	"true": true, "false": true, "test": true, "[": true,
	// processes and services, read-only subcommands enforced above
	"systemctl": true,
	// version control, read-only subcommands enforced above
	"git": true,
	// toolchain, read-only subcommands enforced above
	"go": true,
	// networking inspection (no writes, no connections opened)
	"ip": true, "ifconfig": true, "route": true, "ss": true, "netstat": true,
	"arp": true, "ping": true, "traceroute": true, "dig": true, "nslookup": true,
	"host": true, "getent": true, "resolvectl": true,
	// logs
	"journalctl": true, "dmesg": true, "loginctl": true,
	// hardware
	"lscpu": true, "lsmem": true, "lsusb": true, "lspci": true, "lshw": true,
	"dmidecode": true, "sensors": true, "hwinfo": true, "lsmod": true,
}

// firstNonFlag returns the first argument that is not an option, which is how the
// subcommand of git/systemctl/go is found.
func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

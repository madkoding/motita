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
//   - A rule that SCANS FLAGS (`yq`, `xmllint`, `base64`) treats a flag it does not know as a
//     READ, because the reading form is the common case and refusing every unrecognised flag
//     would interrupt the ordinary work of looking at a file. The flags it does know are the
//     writing ones, and the tests name each of them.
//   - A rule for a program that can RUN something (`sed`, `awk`, `go`, `git branch`) is an
//     ALLOWLIST of options, and it reads the program text where there is one (scripts.go):
//     a list of the bad options misses the abbreviation, the cluster and the one nobody
//     thought of, and in read-only mode a missed one runs code.
var argumentRules = map[string]func([]string) (string, bool){
	// --- the line editors ---------------------------------------------------
	//
	// `sed` is the program a coding agent reaches for to look at a file — measured on real
	// sessions, `sed -n '1,300p' file` was typed 197 times — and it was in the writers
	// table, so every one of those reads stopped to ask. Its writing forms are `-i` and the
	// script commands that write, read or run (`w`, `r`, `e`), and the rule reads both.
	"sed": sedRule,
	// `awk`'s writing form is the PROGRAM: `print > "f"` redirects, `system()` runs anything,
	// `"cmd" | getline` runs a command, and the GNU in-place extension writes.
	"awk": awkRule,
	"find": func(args []string) (string, bool) {
		for _, a := range args {
			// -delete/-exec/-execdir/-ok/-fprint*/-fls turn find into a writer.
			switch {
			case a == "-delete":
				return `find -delete removes files`, true
			case a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir":
				return `find -exec runs an arbitrary command`, true
			case strings.HasPrefix(a, "-fprint") || a == "-fls":
				return fmt.Sprintf("find %s writes a file", a), true
			}
		}
		return "", false
	},
	"git": gitRule,
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
	"go": goRule,
	// --- the readers whose OPERANDS can write -------------------------------
	//
	// This block is the sweep that the `sed` defect made necessary: for each of these the
	// question "what would a person have to add to make it write?" has an answer, and the
	// answer was not in the table. `sort -o out.txt in.txt` and `uniq in.txt out.txt` were
	// reported as "only reads" and ran in SILENCE while writing a file — the same defect as
	// `sed`, in the direction that has no question to catch it.
	"sort": sortRule,
	// `rg --pre CMD` runs CMD on every file it searches, and `--hostname-bin` runs a program to
	// name the host: both are a reader handing a program to run.
	"rg": func(args []string) (string, bool) {
		for _, a := range args {
			name, _, _ := strings.Cut(a, "=")
			if name == "--pre" || name == "--pre-glob" || name == "--hostname-bin" {
				return fmt.Sprintf("rg %s runs a program on what it searches", name), true
			}
		}
		return "", false
	},
	// `tree -o FILE` writes its output to a file, and `-R` (with -H) writes an index file into
	// every directory it lists. Both are single letters that cluster with the reading ones.
	"tree": func(args []string) (string, bool) {
		for _, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "oR") {
				return fmt.Sprintf("tree %s writes files", a), true
			}
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

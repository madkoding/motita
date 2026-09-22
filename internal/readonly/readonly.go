// Package readonly decides whether a command may run when the agent is in
// read-only mode (the "plan" mode: explore and prepare a plan, change nothing).
//
// It exists because the alternative — telling the model "do not write anything" —
// is not a guarantee: it is a request the model can ignore or misread. The point of
// this project is that the model proposes and something deterministic disposes, so
// here the decision is a list, it is testable, and it is applied by the sandbox
// before anything runs.
//
// The policy is deliberately conservative: a command is allowed only when it is
// known to be a reader. Anything unknown, ambiguous or simply not on the list is
// refused, and the refusal is explained so the user (and the model) can see why.
package readonly

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Decision is the verdict for one command.
type Decision struct {
	// Allowed is true only when the command is known to only read.
	Allowed bool
	// Reason explains the verdict, in a form a person can act on.
	Reason string
}

// Check decides whether a command may run in read-only mode.
//
// command is the program (as written, so `grep` or `/usr/bin/grep`), args are its
// arguments.
//
// It is written on top of Classify so that the read-only answer and the wider policy's
// answer come from ONE decision order. The messages below are the read-only mode's own:
// they name the mode and the file to change, because the reader of this one is an
// operator deciding whether to extend the list.
func Check(command string, args []string) Decision {
	kind, reason := Classify(command, args)
	name := strings.ToLower(filepath.Base(strings.TrimSpace(command)))

	switch kind {
	case KindReader:
		return Decision{true, reason}
	case KindMissing:
		return Decision{false, reason}
	case KindShell:
		return Decision{false, reason + ". Run the reader directly (for example " +
			"`grep -n x file` instead of `sh -c \"grep x file\"`)"}
	case KindWriter:
		// A writer by nature has no rule of its own to quote; a writer because of its
		// arguments does, and that reason is the specific one.
		if reason != "" {
			return Decision{false, reason}
		}
		return Decision{false, fmt.Sprintf(
			"%q can change the system, and read-only mode refuses it", name)}
	default:
		if readers[name] {
			// A listed wrapper (env, command) whose own option nobody classified.
			return Decision{false, reason}
		}
		return Decision{false, fmt.Sprintf(
			"%q is not on the read-only list, so it is refused. If it only reads, add it to "+
				"internal/readonly/readers.go with a test", name)}
	}
}

func isShell(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "csh", "tcsh", "fish", "ash", "busybox":
		return true
	}
	return false
}

// argumentRules are the commands that read or write depending on their flags. Each
// one gets a rule that refuses the writing form with the reason.
var argumentRules = map[string]func([]string) (string, bool){
	"find": func(args []string) (string, bool) {
		for _, a := range args {
			// -delete/-exec/-execdir/-ok/-fprint* turn find into a writer.
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
		for _, a := range args {
			if a == "--output" || strings.HasPrefix(a, "--output=") {
				return fmt.Sprintf("git %s --output writes a file", sub), true
			}
		}
		if reason, bad := gitRefRule(sub, args[slices.Index(args, sub)+1:]); bad {
			return reason, true
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
		case "version", "env", "list", "doc", "vet":
			// `go vet` compiles into the build cache, which is not a change (see test).
			// `go env -w`/`-u` write the user's go env file.
			if sub == "env" {
				for _, a := range args {
					if a == "-w" || a == "-u" {
						return fmt.Sprintf("go env %s writes the go environment file", a), true
					}
				}
			}
			return "", false
		case "fmt":
			// go fmt is gofmt -l -w: it rewrites files with or without flags.
			return "go fmt rewrites the files", true
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
	"sort": func(args []string) (string, bool) {
		for _, a := range args {
			switch {
			case strings.HasPrefix(a, "--output"), shortFlagHas(a, "o"):
				return "sort -o writes a file", true
			case strings.HasPrefix(a, "--compress-program"):
				return "sort --compress-program runs a program", true
			}
		}
		return "", false
	},
	// uniq IN OUT and xxd IN OUT write their second operand.
	"uniq": outputOperand("uniq", "-f", "-s", "-w"),
	"xxd":  outputOperand("xxd", "-c", "-g", "-l", "-s", "-o", "-n"),
	"tree": func(args []string) (string, bool) {
		for _, a := range args {
			if shortFlagHas(a, "o") {
				return "tree -o writes a file", true
			}
		}
		return "", false
	},
	"yq": func(args []string) (string, bool) {
		for _, a := range args {
			if strings.HasPrefix(a, "--inplace") || shortFlagHas(a, "i") {
				return "yq -i edits the file in place", true
			}
		}
		return "", false
	},
}

// shortFlagHas reports whether a is a single-dash option cluster containing one of
// letters. Clusters are matched whole (`-ro` has o) because refusing a rare reading
// form costs less than allowing a writing one.
func shortFlagHas(a, letters string) bool {
	return len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], letters)
}

// outputOperand is the rule for a program whose second operand is a file it writes.
// valued are its options that take the next argument as their value.
func outputOperand(name string, valued ...string) func([]string) (string, bool) {
	return func(args []string) (string, bool) {
		operands := 0
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case len(a) > 1 && a[0] == '-':
				for _, v := range valued {
					if a == v {
						i++
					}
				}
			default:
				operands++
			}
		}
		if operands >= 2 {
			return fmt.Sprintf("%s with an output operand writes a file", name), true
		}
		return "", false
	}
}

// gitRefRule decides git branch, tag and remote: they list with no operand and create,
// delete or rename with one. Only known listing options are allowed.
func gitRefRule(sub string, rest []string) (string, bool) {
	refuse := fmt.Sprintf("git %s with these arguments can change the repository", sub)
	if sub == "remote" {
		switch firstNonFlag(rest) {
		case "", "show", "get-url":
			return "", false
		}
		return refuse, true
	}
	writeShort := map[string]string{"branch": "dDmMcCfu", "tag": "asufdmFe"}[sub]
	if writeShort == "" {
		return "", false
	}
	valued := map[string]bool{"--contains": true, "--no-contains": true, "--merged": true,
		"--no-merged": true, "--points-at": true, "--sort": true, "--format": true}
	listingLong := map[string]bool{"--list": true, "--all": true, "--remotes": true,
		"--verbose": true, "--color": true, "--no-color": true, "--column": true,
		"--no-column": true, "--show-current": true, "--ignore-case": true,
		"--abbrev": true, "--no-abbrev": true, "--omit-empty": true, "--verify": true}
	listing, operands := false, 0
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			operands += len(rest) - i - 1
			i = len(rest)
		case strings.HasPrefix(a, "--"):
			flag, _, hasValue := strings.Cut(a, "=")
			if !valued[flag] && !listingLong[flag] {
				return refuse, true
			}
			if valued[flag] && !hasValue {
				i++
			}
			listing = listing || flag == "--list" || flag == "--verify"
		case shortFlagHas(a, writeShort):
			return refuse, true
		case shortFlagHas(a, "lv"):
			listing = true
		case strings.HasPrefix(a, "-") && len(a) > 1:
		default:
			operands++
		}
	}
	if operands > 0 && !listing {
		return refuse, true
	}
	return "", false
}

// writers are programs that change the system by nature. Refused by name.
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
	"sed": true, "awk": true, "perl": true, "python": true, "python3": true,
	"node": true, "ruby": true, "php": true, // interpreters can write
	"make": true, "gcc": true, "cc": true, "clang": true, "ld": true,
	"mvn": true, "gradle": true, "dotnet": true, "java": true,
}

// readers are programs that only observe. Anything not here is refused, so the list
// is the policy: adding a name is a deliberate act with a test.
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
	"find": true, // with the argument rule above
	"sort": true, "uniq": true, "tr": true, "seq": true, "expr": true,
	"jq": true, "yq": true, "xmllint": true, "csvlook": true,
	// system state
	"ps": true, "top": true, "free": true, "uptime": true, "vmstat": true,
	"iostat": true, "mpstat": true, "lsof": true, "pstree": true, "pidof": true,
	"uname": true, "hostname": true, "hostnamectl": true, "arch": true,
	"nproc": true, "getconf": true, "ldd": true,
	"df": true, "du": true, "lsblk": true, "blkid": true, "findmnt": true,
	"id": true, "whoami": true, "groups": true, "users": true, "who": true,
	"w": true, "last": true, "lastlog": true,
	"date": true, "cal": true, "locale": true, "timedatectl": true,
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

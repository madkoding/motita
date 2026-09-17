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
func Check(command string, args []string) Decision {
	trimmed := strings.TrimSpace(command)
	// The emptiness is checked BEFORE taking the base name, because filepath.Base("")
	// is ".": without this, an empty command would be reported as "not on the list"
	// instead of the clearer "there is no command". The check is the only one needed:
	// once the text is not empty and not all separators, Base always returns a name.
	if trimmed == "" || trimmed == "." || trimmed == "/" {
		return Decision{false, "there is no command to run"}
	}
	name := strings.ToLower(filepath.Base(trimmed))

	// A shell is the biggest loophole: `sh -c 'rm -rf /'` would defeat the whole
	// policy, and deciding what a shell line does means parsing shell, which is not
	// something to get wrong. Shells are refused; the caller can run the reader
	// directly.
	if isShell(name) {
		return Decision{false, fmt.Sprintf(
			"%q is a shell: a line can do anything, so it cannot be checked. "+
				"Run the reader directly (for example `grep -n x file` instead of `sh -c \"grep x file\"`)", name)}
	}

	// Writing programs, refused by name even when an argument would make them read.
	// `sed -n` only reads, but `sed -i` rewrites, and one policy is easier to keep
	// honest than a table of exceptions.
	if _, bad := writers[name]; bad {
		return Decision{false, fmt.Sprintf(
			"%q can change the system, and read-only mode refuses it", name)}
	}

	// Commands whose arguments decide: the dangerous form is refused explicitly.
	if rule, ok := argumentRules[name]; ok {
		if reason, bad := rule(args); bad {
			return Decision{false, reason}
		}
	}

	if _, ok := readers[name]; ok {
		return Decision{true, fmt.Sprintf("%q only reads", name)}
	}

	// Unknown: refused, not allowed. This is the important default — a command
	// nobody has classified cannot be assumed harmless.
	return Decision{false, fmt.Sprintf(
		"%q is not on the read-only list, so it is refused. If it only reads, add it to "+
			"internal/readonly/readers.go with a test", name)}
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

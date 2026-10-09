package readonly

// The readers of SYSTEM STATE whose arguments change that state: the clock, the hostname,
// the routes and addresses, the kernel log and the journal. Each is listed in readers for its
// reading form, which is what an agent exploring a machine runs, and each has a form that is
// the opposite of reading. Where the program has a verb, the reading verbs are an allowlist.

import (
	"fmt"
	"strings"
)

// journalctlRule refuses the options that delete, rotate or move the journal, or write keys.
func journalctlRule(args []string) (string, bool) {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		switch {
		case strings.HasPrefix(name, "--vacuum"), name == "--rotate", name == "--flush",
			name == "--sync", name == "--relinquish-var", name == "--smart-relinquish-var",
			name == "--setup-keys", name == "--update-catalog":
			return fmt.Sprintf("journalctl %s changes the journal", name), true
		}
	}
	return "", false
}

// dmesgRule refuses clearing the kernel ring buffer and changing what reaches the console.
func dmesgRule(args []string) (string, bool) {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		switch {
		case name == "--clear" || name == "--read-clear" || name == "--console-off" ||
			name == "--console-on" || name == "--console-level":
			return fmt.Sprintf("dmesg %s changes the kernel log", name), true
		case !strings.HasPrefix(a, "--") && strings.HasPrefix(a, "-") && strings.ContainsAny(a, "cCDEn"):
			return fmt.Sprintf("dmesg %s changes the kernel log", a), true
		}
	}
	return "", false
}

// dateRule refuses setting the clock: `-s`/`--set`, and an operand that is not a `+FORMAT`
// (`date MMDDhhmm` sets the date). The values of -d, -f and -r are not operands.
func dateRule(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, glued := strings.Cut(a, "=")
		switch {
		case strings.HasPrefix(name, "--se"):
			return fmt.Sprintf("date %s sets the clock", a), true
		case name == "--date" || name == "--file" || name == "--reference":
			if !glued {
				i++
			}
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for j := 1; j < len(a); j++ {
				if a[j] == 's' {
					return fmt.Sprintf("date %s sets the clock", a), true
				}
				if a[j] == 'I' {
					// -I takes an optional value, glued only: the rest of the token is it.
					break
				}
				if strings.IndexByte("dfr", a[j]) >= 0 {
					if j == len(a)-1 {
						i++
					}
					break
				}
			}
		case !strings.HasPrefix(a, "+"):
			return fmt.Sprintf("date %s sets the clock", a), true
		}
	}
	return "", false
}

// hostnameRule refuses setting the hostname: an operand, or -F/--file/-b/--boot.
func hostnameRule(args []string) (string, bool) {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "-") || name == "-F" || name == "--file" || name == "-b" || name == "--boot" {
			return fmt.Sprintf("hostname %s sets the hostname", a), true
		}
	}
	return "", false
}

// verbRule allows a systemd-style tool only with one of its reading verbs, and refuses the
// rest (`set-hostname`, `set-time`, `ntp-servers`, …). A property verb such as `hostnamectl
// hostname` reads with no value and sets with one, so it is allowed only alone.
func verbRule(tool string, reading, property map[string]bool, args []string) (string, bool) {
	var operands []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			operands = append(operands, a)
		}
	}
	switch {
	case len(operands) == 0 || reading[operands[0]]:
		return "", false
	case property[operands[0]] && len(operands) == 1:
		return "", false
	}
	return fmt.Sprintf("%s %s changes the system", tool, strings.Join(operands, " ")), true
}

func hostnamectlRule(args []string) (string, bool) {
	return verbRule("hostnamectl", map[string]bool{"status": true},
		map[string]bool{"hostname": true, "icon-name": true, "chassis": true, "deployment": true,
			"location": true}, args)
}

func timedatectlRule(args []string) (string, bool) {
	return verbRule("timedatectl", map[string]bool{"status": true, "show": true,
		"list-timezones": true, "timesync-status": true, "show-timesync": true}, nil, args)
}

// ipValueOptions are the `ip` options whose value is the next token.
var ipValueOptions = map[string]bool{"-n": true, "-netns": true, "-f": true, "-family": true,
	"-rc": true, "-rcvbuf": true, "-l": true, "-loops": true}

// ipRule allows `ip OBJECT [show|list|get|help] …`: the command is matched the way ip matches
// it (any prefix of the word), and every other command adds, deletes, changes, flushes or runs
// something (`ip netns exec`). `-batch` and `-force` run commands from a file.
func ipRule(args []string) (string, bool) {
	var operands []string
	for i := 0; i < len(args); i++ {
		// ip accepts `--opt` for `-opt`, and any prefix of an option's name: `-b` is -batch,
		// `-br` is -brief.
		a := strings.TrimPrefix(args[i], "-")
		if !strings.HasPrefix(a, "-") {
			a = args[i]
		}
		switch {
		case len(a) > 1 && strings.HasPrefix("-batch", a) || len(a) > 2 && strings.HasPrefix("-force", a):
			return fmt.Sprintf("ip %s runs commands from a file", a), true
		case ipValueOptions[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			operands = append(operands, a)
		}
	}
	if len(operands) < 2 {
		return "", false
	}
	for _, verb := range []string{"show", "list", "lst", "get", "help"} {
		if strings.HasPrefix(verb, operands[1]) {
			return "", false
		}
	}
	return fmt.Sprintf("ip %s %s changes the network configuration", operands[0], operands[1]), true
}

// routeRule allows the listing (`route`, `route -n`, `route -A inet6`): any operand is a
// command that adds, deletes or flushes a route.
func routeRule(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-A":
			i++
		case !strings.HasPrefix(a, "-"):
			return fmt.Sprintf("route %s changes the routing table", a), true
		}
	}
	return "", false
}

// ifconfigRule allows `ifconfig [-a|-s|-v] [INTERFACE]`: anything after the interface (up,
// down, an address, mtu …) changes it.
func ifconfigRule(args []string) (string, bool) {
	if ops := positionals("ifconfig", args); len(ops) > 1 {
		return fmt.Sprintf("ifconfig %s changes the interface", strings.Join(ops, " ")), true
	}
	return "", false
}

// arpRule refuses deleting, setting and loading entries (-d, -s, -f and their long forms).
func arpRule(args []string) (string, bool) {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		switch {
		case name == "--delete" || name == "--set" || name == "--file":
			return fmt.Sprintf("arp %s changes the ARP cache", a), true
		case !strings.HasPrefix(a, "--") && strings.HasPrefix(a, "-") && strings.ContainsAny(a, "dsf"):
			return fmt.Sprintf("arp %s changes the ARP cache", a), true
		}
	}
	return "", false
}

// agRule refuses `--pager CMD`, which runs CMD on the output.
func agRule(args []string) (string, bool) {
	for _, a := range args {
		if name, _, _ := strings.Cut(a, "="); name == "--pager" {
			return "ag --pager runs a program on the output", true
		}
	}
	return "", false
}

package app

import (
	"fmt"
	"regexp"
	"strings"
)

// valueFlags are the flags that consume the argument after them. The list exists for ONE reason: to
// tell a flag's VALUE apart from a subcommand. `-session gateway` names a conversation called
// "gateway", and reading that word as a command would make `motita -session gateway -connect
// host` start a service instead of attaching a client - a silent reversal of what was asked for.
var valueFlags = map[string]bool{
	"-config": true, "--config": true,
	"-task": true, "--task": true,
	"-task-file": true, "--task-file": true,
	"-gateway": true, "--gateway": true,
	"-connect": true, "--connect": true,
	"-session": true, "--session": true,
	"-p": true, "--prompt": true,
}

// flagValues marks the positions of arguments that are a flag's VALUE rather than a word of their
// own. It is derived from valueFlags and not written out again, so a flag added above is skipped
// below without anyone remembering to do it.
func flagValues(args []string) []bool {
	skip := make([]bool, len(args))
	for i := 0; i < len(args); i++ {
		name, _, hasValue := strings.Cut(args[i], "=")
		// "-flag=value" carries its own value, so the next argument is a word of its own.
		if hasValue || !valueFlags[name] {
			continue
		}
		if i+1 < len(args) {
			skip[i+1] = true
			i++
		}
	}
	return skip
}

// parse reads the accepted flags. It is done by hand, and not with the flag
// package over os.Args, so the function stays pure and does not depend on the
// process' global state (which is what used to make it impossible to test).
func parse(args []string) (flags, error) {
	var b flags

	// The subcommands are read FIRST, because they are POSITIONAL arguments and the loop below is
	// written for flags: `motita gateway start` reaching that loop would be reported as an
	// unknown flag named "gateway", which is a confusing way to say the user used the right word in
	// the right place.
	//
	// They are found by scanning for the word while SKIPPING the values of flags, which is why there
	// is a list of the flags that take one. Without that skipping, `-session gateway` would be read
	// as the subcommand and `motita -session gateway -connect host` would start a SERVICE instead
	// of a client that attaches to a conversation called "gateway".
	//
	// The scan RESTARTS after every word it removes, so one line can carry two subcommands
	// (`motita gateway start curator status`) without the second being read at a stale index.
	args, err := b.parseSubcommands(args)
	if err != nil {
		return b, err
	}
	if err := b.parseFlags(args); err != nil {
		return b, err
	}
	// A process cannot be a gateway and a client of one at the same time.
	//
	// Refused rather than resolved because there is no sensible resolution: -serve makes this
	// process the thing that holds conversations, -connect makes it a viewer of somebody else's,
	// and the two contradict. Picking one silently would leave the user with an interface for the
	// thing they did not ask for.
	if b.serve && b.connect != "" {
		return b, fmt.Errorf("-serve and -connect cannot be used together: " +
			"-serve makes this process a gateway, and -connect makes it a client of one")
	}
	return b, nil
}

// parseSubcommands records the subcommand words of the line and returns the arguments without
// them. The scan restarts after every word it removes (see parse).
func (b *flags) parseSubcommands(args []string) ([]string, error) {
	for {
		rest, consumed, err := b.takeSubcommand(args)
		if err != nil || !consumed {
			return args, err
		}
		args = rest
	}
}

// takeSubcommand finds the first subcommand word that is not a flag's value, records it and
// returns the arguments without it; consumed is false when there is none.
func (b *flags) takeSubcommand(args []string) (rest []string, consumed bool, err error) {
	skip := flagValues(args)
	for i := range args {
		if skip[i] {
			continue
		}
		if rest, consumed, err = b.subcommandAt(args, i); err != nil || consumed {
			return rest, consumed, err
		}
	}
	return args, false, nil
}

// subcommandAt reads the subcommand at args[i], if that word is one.
func (b *flags) subcommandAt(args []string, i int) ([]string, bool, error) {
	switch args[i] {
	case "gateway":
		return b.gatewaySubcommand(args, i)
	case "config":
		// The setup wizard. A word and not a flag because -config already names the
		// YAML file; `motita config` is the one place the two cannot be confused.
		b.initConfig = true
		return withoutArgs(args, i, 1), true, nil
	case "doctor":
		// A word like `config`: a report on the machine, read before any file is.
		b.doctor = true
		return withoutArgs(args, i, 1), true, nil
	case "curator":
		return b.curatorSubcommand(args, i)
	case "sandbox":
		return b.sandboxSubcommand(args, i)
	default:
		return args, false, nil
	}
}

// withoutArgs returns a copy of args without the n arguments starting at i.
func withoutArgs(args []string, i, n int) []string {
	return append(append([]string{}, args[:i]...), args[i+n:]...)
}

// gatewaySubcommand reads `gateway <action>` at args[i].
func (b *flags) gatewaySubcommand(args []string, i int) ([]string, bool, error) {
	if i+1 >= len(args) {
		return nil, false, fmt.Errorf("gateway needs an action: %s", gatewayActions)
	}
	switch strings.ToLower(strings.TrimSpace(args[i+1])) {
	case "allow", "deny":
		return b.gatewayCodeAction(args, i)
	case "start", "stop", "status", "setup", "computers":
		// Recorded as the user wrote it: what they typed is what gets reported.
		b.gatewayAction = args[i+1]
		// Removed from the argument list so the flag loop never sees them: a positional argument
		// is not a flag, and leaving it there would end in "unknown flag: gateway".
		return withoutArgs(args, i, 2), true, nil
	default:
		// Rejected with the list rather than ignored: falling through would turn a
		// typo into something else entirely - `motita gateway strat` starting the
		// interface.
		return nil, false, fmt.Errorf("unknown gateway action %q: %s", args[i+1], gatewayActions)
	}
}

// gatewayActions lists the actions of `motita gateway`, for the errors above.
const gatewayActions = "start, stop, status, setup, computers, allow or deny"

// codeHalf is one half of a pairing code, as the page shows it: "123 456".
var codeHalf = regexp.MustCompile(`^\d{3}$`)

// gatewayCodeAction reads `gateway allow|deny <code>` at args[i]. The code is the one the other
// computer's page shows, typed with its space ("123 456", two arguments) or without it.
func (b *flags) gatewayCodeAction(args []string, i int) ([]string, bool, error) {
	action := strings.ToLower(strings.TrimSpace(args[i+1]))
	if i+2 >= len(args) {
		return nil, false, fmt.Errorf("gateway %s needs the code the other computer shows: "+
			"`motita gateway computers` lists the ones waiting", action)
	}
	n := 3
	code := strings.TrimSpace(args[i+2])
	if codeHalf.MatchString(code) && i+3 < len(args) && codeHalf.MatchString(strings.TrimSpace(args[i+3])) {
		code += strings.TrimSpace(args[i+3])
		n = 4
	}
	b.gatewayAction = action
	b.gatewayCode = code
	return withoutArgs(args, i, n), true, nil
}

// sandboxSubcommand reads `sandbox apparmor <print|install|remove>` at args[i].
func (b *flags) sandboxSubcommand(args []string, i int) ([]string, bool, error) {
	const usage = "sandbox apparmor <print|install|remove>"
	if i+2 >= len(args) || args[i+1] != "apparmor" {
		return nil, false, fmt.Errorf("usage: motita %s", usage)
	}
	switch action := strings.ToLower(strings.TrimSpace(args[i+2])); action {
	case "print", "install", "remove":
		b.sandboxAction = action
		return withoutArgs(args, i, 3), true, nil
	default:
		return nil, false, fmt.Errorf("unknown sandbox apparmor action %q: usage: motita %s", args[i+2], usage)
	}
}

// curatorSubcommand reads `curator <action> [skill]` at args[i].
func (b *flags) curatorSubcommand(args []string, i int) ([]string, bool, error) {
	if i+1 >= len(args) {
		return nil, false, fmt.Errorf("curator needs an action: %s", curatorActions)
	}
	action := strings.ToLower(strings.TrimSpace(args[i+1]))
	switch action {
	case "status", "run", "list-archived", "list-proposed":
		b.curatorAction = action
		return withoutArgs(args, i, 2), true, nil
	case "pin", "unpin", "restore", "show-proposed", "accept", "reject":
		return b.curatorSkillAction(args, i, action)
	default:
		return nil, false, fmt.Errorf("unknown curator action %q: %s", args[i+1], curatorActions)
	}
}

// curatorSkillAction reads `curator <action> <skill>` at args[i], for the actions that name a
// skill.
func (b *flags) curatorSkillAction(args []string, i int, action string) ([]string, bool, error) {
	if i+2 >= len(args) {
		return nil, false, fmt.Errorf("curator %s needs a skill name", action)
	}
	b.curatorAction = action
	// The name is data and is kept EXACTLY as typed: it is not an action, so
	// lower-casing it would be inventing a skill the user did not name.
	b.curatorSkill = strings.TrimSpace(args[i+2])
	return withoutArgs(args, i, 3), true, nil
}

// parseFlags reads the flags left once the subcommands are out. Both forms are accepted:
// -flag value and -flag=value.
func (b *flags) parseFlags(args []string) error {
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if !valueFlags[name] {
			if err := b.setSwitch(name, value, hasValue, args[i]); err != nil {
				return err
			}
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return fmt.Errorf("%s is missing its value", name)
			}
			i++
			value = args[i]
		}
		if err := b.setValue(name, value); err != nil {
			return err
		}
	}
	return nil
}

// setValue records a flag that takes a value (one of valueFlags).
func (b *flags) setValue(name, v string) error {
	switch name {
	case "-config", "--config":
		b.configPath = v
	case "-task", "--task":
		b.task = v
	case "-task-file", "--task-file":
		b.taskFile = v
	case "-gateway", "--gateway":
		b.gateway = v
	case "-connect", "--connect":
		return b.setConnect(v)
	case "-session", "--session":
		b.session = strings.TrimSpace(v)
	case "-p", "--prompt":
		b.prompt = v
	default:
		// valueFlags names no other flag.
	}
	return nil
}

// setConnect records the gateway address of -connect.
func (b *flags) setConnect(v string) error {
	// An EMPTY address is refused here rather than passed on: "-connect" with nothing
	// after it, or "-connect=" with nothing after the equals, would otherwise produce a
	// client pointed at nothing and fail somewhere far from the mistake.
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("-connect needs an address, like -connect 127.0.0.1:7477")
	}
	b.connect = strings.TrimSpace(v)
	return nil
}

// setSwitch records a flag that takes no separate value; arg is the argument as written.
func (b *flags) setSwitch(name, value string, hasValue bool, arg string) error {
	switch name {
	case "-plan", "--plan":
		b.plan = !hasValue || value == "true" || value == "1"
	case "-tui", "--tui":
		b.tui = true
	case "-serve", "--serve":
		b.serve = true
	case "-token-only", "--token-only":
		b.tokenOnly = true
	case "-json", "--json":
		b.jsonOut = true
	case "-validate-config", "--validate-config":
		b.validateConfig = true
	case "-version", "--version":
		b.version = true
	case "-isolation", "--isolation":
		b.isolation = true
	// The switches of `curator run`. They take NO value: a boolean flag that consumed
	// the argument after it would swallow a word the user meant as a command, which is
	// why neither appears in valueFlags.
	case "-consolidate", "--consolidate":
		b.curatorConsolidate = true
	case "-dry-run", "--dry-run":
		b.curatorDryRun = true
	case "-h", "-help", "--help":
		return fmt.Errorf("help requested")
	case "":
		return fmt.Errorf("empty argument")
	default:
		return fmt.Errorf("unknown flag: %q", arg)
	}
	return nil
}

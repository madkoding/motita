package readonly

import (
	"fmt"
	"strings"
)

// ghReadingVerbs are the gh subcommands that only read, and for each one the verbs that read.
//
// `gh` is not a reader by name: `gh pr create` opens a pull request and `gh api -X POST`
// changes GitHub, so the name alone cannot answer. The table is an allowlist of the
// (subcommand, verb) pairs a coding agent needs to look at a pull request and its checks;
// everything else is refused, which is the safe default for a program that talks to a service.
var ghReadingVerbs = map[string]map[string]bool{
	"pr":          {"list": true, "view": true, "checks": true, "diff": true, "status": true},
	"issue":       {"list": true, "view": true, "status": true},
	"run":         {"list": true, "view": true},
	"workflow":    {"list": true, "view": true},
	"repo":        {"list": true, "view": true},
	"release":     {"list": true, "view": true},
	"label":       {"list": true},
	"cache":       {"list": true},
	"gist":        {"list": true, "view": true},
	"org":         {"list": true},
	"project":     {"list": true, "view": true, "field-list": true, "item-list": true},
	"ruleset":     {"list": true, "view": true, "check": true},
	"variable":    {"list": true, "get": true},
	"secret":      {"list": true},
	"extension":   {"list": true, "search": true},
	"alias":       {"list": true},
	"auth":        {"status": true},
	"config":      {"get": true, "list": true},
	"search":      {"code": true, "commits": true, "issues": true, "prs": true, "repos": true},
	"gpg-key":     {"list": true},
	"ssh-key":     {"list": true},
	"codespace":   {"list": true, "logs": true},
	"attestation": {"verify": true},
}

// ghRule allows the gh subcommands that only read. The subcommand is the first operand; the
// verb after it decides for the subcommands that have both a reading and a writing form.
// `gh api` is judged by its method and body, and `gh status` has no verb.
func ghRule(args []string) (string, bool) {
	i := 0
	for ; i < len(args) && strings.HasPrefix(args[i], "-"); i++ {
		switch {
		case args[i] == "--help" || args[i] == "-h" || args[i] == "--version":
			return "", false
		case args[i] == "-R" || args[i] == "--repo":
			i++ // the repository to read, not a program to run
		case strings.HasPrefix(args[i], "--repo=") || strings.HasPrefix(args[i], "-R="):
			// The value is glued to the flag.
		default:
			return fmt.Sprintf("gh %s changes what gh runs or where, and read-only mode cannot check it", args[i]), true
		}
	}
	if i == len(args) {
		return "", false
	}
	sub, rest := args[i], args[i+1:]
	switch sub {
	case "version", "help", "status":
		return "", false
	case "api":
		return ghAPIRule(rest)
	}
	verbs, ok := ghReadingVerbs[sub]
	if !ok {
		return fmt.Sprintf("gh %s can change GitHub or the working tree", sub), true
	}
	if len(rest) > 0 && (rest[0] == "--help" || rest[0] == "-h") {
		return "", false
	}
	verb := firstNonFlag(rest)
	if !verbs[verb] {
		if verb == "" {
			return fmt.Sprintf("gh %s can change GitHub or the working tree; only its reading verbs are allowed", sub), true
		}
		return fmt.Sprintf("gh %s %s can change GitHub or the working tree; only its reading verbs are allowed", sub, verb), true
	}
	return "", false
}

// ghAPIRule allows `gh api` only as a GET with no body: `-X POST`, `--method`, `-f`, `-F`,
// `--field`, `--raw-field` and `--input` all send something that changes GitHub, and a
// `graphql` mutation is a body like any other.
func ghAPIRule(rest []string) (string, bool) {
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if strings.HasPrefix(a, "--") {
			name, value, glued := strings.Cut(a[2:], "=")
			switch name {
			case "method":
				if !glued && i+1 < len(rest) {
					value = rest[i+1]
					i++
				}
				if !strings.EqualFold(value, "GET") {
					return fmt.Sprintf("gh api --method %s changes GitHub", value), true
				}
			case "field", "raw-field", "input":
				return fmt.Sprintf("gh api %s sends a body, which changes GitHub", a), true
			}
			continue
		}
		switch a[1] {
		case 'X':
			value := a[2:]
			if value == "" && i+1 < len(rest) {
				value = rest[i+1]
				i++
			}
			if !strings.EqualFold(value, "GET") {
				return fmt.Sprintf("gh api -X %s changes GitHub", value), true
			}
		case 'f', 'F':
			return fmt.Sprintf("gh api %s sends a body, which changes GitHub", a), true
		}
	}
	return "", false
}

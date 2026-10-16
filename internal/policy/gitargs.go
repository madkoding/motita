package policy

import "strings"

// Literals used more than once in this file.
const (
	gitDirOption      = "--git-dir"
	gitWorkTreeOption = "--work-tree"
)

// gitCodeGlobalPrefixes are the options BEFORE the subcommand that make git run a command, or move
// the repository it reads its configuration from: `-c core.fsmonitor=…` and `-c core.pager=…`
// name a program git then starts, `--exec-path` swaps the directory its helpers come from,
// `--config-env` is `-c` taking the value from the environment, and `--git-dir` / `--work-tree`
// point it at a repository the workspace does not own. `-C dir` only changes where the
// (workspace) command runs, and is left alone.
var gitCodeGlobalPrefixes = []string{"-c", "--config-env", "--exec-path", gitDirOption, gitWorkTreeOption}

// gitGlobalRunsCode reports whether an option before the subcommand is one of those.
func gitGlobalRunsCode(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return false
		}
		if a == "-C" || a == "--namespace" {
			i++
			continue
		}
		if hasAnyPrefix(a, gitCodeGlobalPrefixes) {
			return true
		}
	}
	return false
}

// hasAnyPrefix reports whether s starts with one of the prefixes.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// abbreviates reports whether name is option or an abbreviation of it at least minLen long, as
// git accepts for its long options.
func abbreviates(name, option string, minLen int) bool {
	return len(name) >= minLen && strings.HasPrefix(option, name)
}

// gitCloneRunsCode are the long options of `clone` that name a program or set configuration.
var gitCloneRunsCode = []string{"--upload-pack", "--config"}

// gitVerbRunsCode reports whether an argument of an otherwise local verb makes git run a program
// or write outside the workspace: `--ext-diff` / `--textconv` run the drivers a configuration
// names, `--output` writes a file anywhere, `rebase -x` runs a command per commit, `grep -O`
// opens the matches in a pager, and `clone` runs the program a `ext::` URL, `--upload-pack` or
// `-c` names.
func gitVerbRunsCode(sub string, rest []string) bool {
	for _, a := range rest {
		name, _, _ := strings.Cut(a, "=")
		if gitAnyVerbRunsCode(name) || gitOneVerbRunsCode(sub, a, name) {
			return true
		}
	}
	return false
}

// gitAnyVerbRunsCode is the part of gitVerbRunsCode that holds for every verb.
func gitAnyVerbRunsCode(name string) bool {
	return abbreviates(name, "--ext-diff", len("--ext")) ||
		abbreviates(name, "--textconv", len("--textc")) ||
		abbreviates(name, "--output", len("--out"))
}

// gitOneVerbRunsCode is the part that holds for one verb: a is the argument, name its part before
// any `=`.
func gitOneVerbRunsCode(sub, a, name string) bool {
	switch sub {
	case "rebase":
		return shortBundleHas(a, "x") || abbreviates(name, "--exec", len("--ex"))
	case "grep":
		return shortBundleHas(a, "O") || abbreviates(name, "--open-files-in-pager", len("--op"))
	case "clone":
		return strings.Contains(a, "::") || shortBundleHas(a, "uc") || hasAnyPrefix(name, gitCloneRunsCode) ||
			abbreviates(name, "--template", len("--tem"))
	default:
		return false
	}
}

// shortBundleHas reports whether a is a bundle of short options (`-ix`, `-nO'cmd'`, `-qu`) that
// contains one of the letters. Git reads the letters left to right and the first one that takes
// a value swallows the rest, so a letter after it is a value, not an option; every letter that
// takes a value in the verbs checked here is in the set asked about or ends the scan.
func shortBundleHas(a, letters string) bool {
	if len(a) < 2 || a[0] != '-' || a[1] == '-' {
		return false
	}
	return strings.ContainsAny(a[1:], letters)
}

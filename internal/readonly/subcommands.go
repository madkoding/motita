package readonly

// The rules for the programs whose SUBCOMMAND decides: git, go and sort's options. They are
// allowlists where the program can run something, because a list of the bad forms is a list
// of the forms somebody already thought of.

import (
	"fmt"
	"strings"
)

// gitReading are the git subcommands that inspect a repository. Anything else can write the
// index, the working tree or the remote, and is refused.
var gitReading = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "branch": true,
	"remote": true, "tag": true, "describe": true, "rev-parse": true,
	"blame": true, "shortlog": true, "ls-files": true, "cat-file": true,
	"config": true, "grep": true, "whatchanged": true, "reflog": true,
	"show-ref": true, "for-each-ref": true, "count-objects": true,
}

// gitRule allows read-only git: a reading subcommand, with the options before it limited to
// the ones that change nothing (`-c core.pager=…` and `-C dir` change what runs and where),
// no `--output` file, and `branch`, `tag` and `remote` only in their listing forms.
func gitRule(args []string) (string, bool) {
	i := 0
	for ; i < len(args) && strings.HasPrefix(args[i], "-"); i++ {
		switch args[i] {
		case "--no-pager", "-P", "--no-replace-objects", "--literal-pathspecs",
			"--glob-pathspecs", "--noglob-pathspecs", "--icase-pathspecs", "--no-optional-locks":
		default:
			return fmt.Sprintf("git %s changes what git runs or where, and read-only mode cannot check it", args[i]), true
		}
	}
	if i == len(args) {
		return "", false
	}
	sub, rest := args[i], args[i+1:]
	if !gitReading[sub] {
		return fmt.Sprintf("git %s can change the repository", sub), true
	}
	for _, a := range rest {
		if a == "--output" || strings.HasPrefix(a, "--output=") {
			return fmt.Sprintf("git %s %s writes a file", sub, a), true
		}
	}
	switch sub {
	case "config":
		return gitConfigRule(rest)
	case "grep":
		// `-O CMD` / `--open-files-in-pager=CMD` runs CMD on the matching files. git accepts
		// abbreviated long options, and the short one clusters (`-nOvim`).
		for _, a := range rest {
			name, _, _ := strings.Cut(a, "=")
			if (!strings.HasPrefix(a, "--") && strings.HasPrefix(a, "-") && strings.Contains(a, "O")) ||
				(len(name) > 3 && strings.HasPrefix("--open-files-in-pager", name)) {
				return fmt.Sprintf("git grep %s runs a program on the matches", a), true
			}
		}
	case "branch", "tag":
		return gitListRule(sub, rest)
	case "remote":
		// `git remote`, `git remote -v`, `git remote get-url NAME` and `git remote show NAME`.
		verb := firstNonFlag(rest)
		if verb != "" && verb != "get-url" && verb != "show" {
			return fmt.Sprintf("git remote %s can change the repository", verb), true
		}
		for _, a := range rest {
			if verb == "" && a != "-v" && a != "--verbose" {
				return fmt.Sprintf("git remote %s is not a listing form", a), true
			}
		}
	}
	return "", false
}

// gitConfigRule: `git config` reads with no value and writes with one:
//
//	git config user.name           -> reads
//	git config user.name "Someone" -> writes
//
// A test found the plain writing form slipping through, which is why the positional arguments
// are counted instead of only looking at the flags. The newer subcommand forms (`git config
// set`, `git config edit`) write or open an editor.
func gitConfigRule(rest []string) (string, bool) {
	positional := 0
	for _, a := range rest {
		switch {
		case a == "--add", a == "--unset", a == "--unset-all", a == "--edit", a == "-e",
			a == "--replace-all", a == "--rename-section", a == "--remove-section":
			return fmt.Sprintf("git config %s writes the configuration", a), true
		case strings.HasPrefix(a, "-"):
		case positional == 0 && (a == "set" || a == "unset" || a == "edit" ||
			a == "rename-section" || a == "remove-section"):
			return fmt.Sprintf("git config %s writes the configuration", a), true
		default:
			positional++
		}
	}
	if positional >= 2 {
		return "git config with a value writes the configuration", true
	}
	return "", false
}

// gitListValued are the listing options of `git branch` and `git tag` that take a value, and
// gitListAlone the ones that stand alone.
var (
	gitListValued = map[string]bool{"--contains": true, "--no-contains": true, "--merged": true,
		"--no-merged": true, "--points-at": true, "--sort": true, "--format": true}
	gitListAlone = map[string]bool{"--all": true, "--remotes": true, "--verbose": true,
		"--show-current": true, "--ignore-case": true, "--color": true, "--no-color": true,
		"--column": true, "--no-column": true, "--abbrev": true, "--no-abbrev": true,
		"--list": true, "--omit-empty": true}
)

// gitListRule allows `git branch` and `git tag` only as listings: the options that filter or
// format a list, and a pattern only after `-l`/`--list`. A name without `--list` creates a
// branch or a tag, and every other option renames, deletes, moves or verifies.
func gitListRule(sub string, rest []string) (string, bool) {
	shorts := "arvil" // branch: -a -r -v -i -l
	if sub == "tag" {
		shorts = "il"
	}
	list, named := false, false
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		name, _, glued := strings.Cut(a, "=")
		switch {
		case a == "--":
			named = named || i+1 < len(rest)
			i = len(rest)
		case gitListValued[name]:
			if !glued {
				i++
			}
		case gitListAlone[name]:
			list = list || name == "--list"
		case sub == "tag" && strings.HasPrefix(a, "-n") && strings.Trim(a[2:], "0123456789") == "":
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1 &&
			strings.Trim(a[1:], shorts) == "":
			list = list || strings.Contains(a, "l")
		case strings.HasPrefix(a, "-"):
			return fmt.Sprintf("git %s %s is not a listing form", sub, a), true
		default:
			named = true
		}
	}
	if named && !list {
		return fmt.Sprintf("git %s with a name and no --list creates one", sub), true
	}
	return "", false
}

// goRule allows the go subcommands that only look: version, env, list, doc and vet. `go test`
// runs the package's own code and `go fmt` rewrites the files, so neither is a read; options
// that hand go a program to run (`-vettool`, `-toolexec`, `-exec`) or a file to write (`-o`,
// `-modfile`, `-mod=mod`, `go env -w`) are refused under any subcommand.
//
// `go vet` compiles into the build cache, and that is not "changing the system": it is what the
// check does, and refusing it would make plan mode useless for the case this project is about.
func goRule(args []string) (string, bool) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, value, glued := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !glued && i+1 < len(args) {
			value = args[i+1]
		}
		switch {
		case name == "vettool" || name == "toolexec" || name == "exec":
			return fmt.Sprintf("go %s runs a program of its choosing", a), true
		case name == "o" || name == "modfile" || (name == "mod" && value == "mod"):
			return fmt.Sprintf("go %s writes a file", a), true
		}
	}
	sub := firstNonFlag(args)
	switch sub {
	case "version", "list", "doc", "vet", "":
		return "", false
	case "env":
		for _, a := range args {
			if a == "-w" || a == "-u" {
				return fmt.Sprintf("go env %s writes the go environment file", a), true
			}
		}
		return "", false
	case "test":
		return "go test compiles and runs the package's code", true
	case "fmt":
		return "go fmt rewrites the files", true
	case "build":
		return "go build writes a binary", true
	}
	return fmt.Sprintf("go %s can write to the module cache or the tree", sub), true
}

var sortLongOptions = []string{
	"--ignore-leading-blanks", "--dictionary-order", "--ignore-case", "--general-numeric-sort",
	"--ignore-nonprinting", "--month-sort", "--human-numeric-sort", "--numeric-sort",
	"--random-sort", "--random-source", "--reverse", "--sort", "--version-sort", "--batch-size",
	"--check", "--compress-program", "--debug", "--files0-from", "--key", "--merge", "--output",
	"--stable", "--buffer-size", "--field-separator", "--temporary-directory", "--parallel",
	"--unique", "--zero-terminated", "--help", "--version",
}

// sortRule refuses `-o FILE`, which writes, and `--compress-program=CMD`, which runs CMD on
// sort's temporary files. GNU sort accepts abbreviated long options (`--out=f`, `--comp=x`) and
// short clusters (`-uo f`), so both are resolved the way sort resolves them.
func sortRule(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return "", false
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(a, "=")
			switch resolveLong(name, sortLongOptions) {
			case "--output":
				return fmt.Sprintf("sort %s writes a file", a), true
			case "--compress-program":
				return fmt.Sprintf("sort %s runs a program of its choosing", a), true
			}
		case strings.HasPrefix(a, "-"):
			for j := 1; j < len(a); j++ {
				if a[j] == 'o' {
					return fmt.Sprintf("sort %s writes a file", a), true
				}
				if strings.IndexByte("ktST", a[j]) >= 0 {
					// The rest of the token, or the next one, is this option's value.
					if j == len(a)-1 {
						i++
					}
					break
				}
			}
		}
	}
	return "", false
}

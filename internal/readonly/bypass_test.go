package readonly

import (
	"strings"
	"testing"
)

// The regressions for A4: read-only (plan) mode ran code and wrote files through readers whose
// options or program text do exactly that. Each command below was allowed before; each must be
// refused now. The second table is the other half — the reading forms of the same programs,
// which a rule that refused too much would have broken.
//
// The commands are written as the argument vectors Check receives (after the line splitter has
// removed the shell quoting), so a program's text is exactly what the tool would read.

type argv []string

func TestReadOnlyModeRefusesEveryKnownBypass(t *testing.T) {
	for _, c := range []argv{
		// sed: -i in every spelling, a script file, an unknown option, and the script commands
		// and s flags that write, read or run.
		{"sed", "-n", "e id", "f"}, {"sed", "w out", "f"}, {"sed", "1W out", "f"},
		{"sed", "r /etc/shadow", "f"}, {"sed", "R x", "f"}, {"sed", "s/a/b/w out", "f"},
		{"sed", "s/a/b/e", "f"}, {"sed", "s/a/b/gpe", "f"}, {"sed", "-e", "p", "-e", "w out", "f"},
		{"sed", "--expression=1w out", "f"}, {"sed", "--expression", "1w out", "f"},
		{"sed", "--in-pl", "s/a/b/", "f"}, {"sed", "-f", "script.sed", "f"}, {"sed", "--file=x", "f"},
		{"sed", "--weird", "p", "f"}, {"sed", "--s", "p", "f"}, {"sed", "-X", "p", "f"},
		{"sed", "/x/w out", "f"}, {"sed", `\%x%w out`, "f"}, {"sed", "1,/x/w out", "f"},
		{"sed", "$!w out", "f"}, {"sed", "1{w out\n}", "f"}, {"sed", "1a\\\nfoo\nw out", "f"},
		{"sed", "y/ab/cd/;w out", "f"}, {"sed", "--", "w out", "f"}, {"sed", "p;e", "f"},
		{"sed", "s/[/]/x/w out", "f"}, {"sed", "y/[/]/;w out", "f"}, {"sed", "s/[[:alpha:]/]/x/w out", "f"}, {"sed", "'w out'", "f"},
		// sed: scripts that cannot be read are refused rather than guessed at.
		{"sed", "s/a/b", "f"}, {"sed", "s/[a/b/", "f"}, {"sed", "y/a", "f"}, {"sed", "s", "f"},
		{"sed", "y", "f"}, {"sed", "1", "f"}, {"sed", "1,", "f"}, {"sed", "/a", "f"}, {"sed", `\`, "f"},
		{"sed", "s/a/b/X", "f"}, {"sed", "k", "f"}, {"sed", "1!", "f"}, {"sed", "s/[[:alpha:/x/", "f"},
		{"sed", "s/a\n/b/", "f"}, {"sed", "1,/a", "f"},
		// awk: system, getline, pipes, redirections, @, and options that load a program.
		{"awk", `BEGIN{system("id")}`}, {"awk", `BEGIN{system ("id")}`}, {"awk", `BEGIN{"id"|getline}`},
		{"awk", `{getline line < "/etc/shadow"}`}, {"awk", `{print|"sh"}`}, {"awk", `{print > "out"}`},
		{"awk", `{print >> out}`}, {"awk", `{printf("%s",$1) > "out"}`}, {"awk", "{print $1,\n$2 > \"f\"}"},
		{"awk", "{print $1 ||\n$2 > \"f\"}"}, {"awk", `{print |& "cmd"}`}, {"awk", `@load "x"`},
		{"awk", `BEGIN{f="system";@f("id")}`}, {"awk", `'{print > "out"}'`},
		{"awk", "-f", "prog.awk", "f"}, {"awk", "--file=prog.awk", "f"}, {"awk", "-E", "prog", "f"},
		{"awk", "-i", "lib", "f"}, {"awk", "--include=lib", "f"}, {"awk", "-l", "ext", "f"},
		{"awk", "--load=ext", "f"}, {"awk", "-W", "exec", "f"}, {"awk", "--weird", "f"},
		{"awk", "-e", `BEGIN{system("id")}`}, {"awk", `--source=BEGIN{system("id")}`},
		{"awk", "--source", `BEGIN{system("id")}`}, {"awk", "--", `BEGIN{system("id")}`},
		{"gawk", `BEGIN{system("id")}`}, {"mawk", `BEGIN{system("id")}`},
		{"awk", `BEGIN{x="unterminated}`}, {"awk", "/unterminated"}, {"awk", "/[unterminated/"},
		{"awk", "/a\n/"}, {"awk", `/a\`}, {"awk", `/[[:a/`},
		// find, rg, sort, tree.
		{"find", ".", "-fls", "out"}, {"find", ".", "-fprint", "out"}, {"find", ".", "-fprintf", "out", "%p"},
		{"find", ".", "-delete"}, {"find", ".", "-exec", "id", ";"}, {"find", ".", "-execdir", "id", ";"},
		{"find", ".", "-ok", "id", ";"},
		{"rg", "--pre", "./x", "p"}, {"rg", "--pre=./x", "p"}, {"rg", "--pre-glob", "*.x", "p"},
		{"rg", "--hostname-bin=./x", "p"},
		{"sort", "--compress-program=./x", "f"}, {"sort", "--compress=./x", "f"}, {"sort", "--co", "./x", "f"},
		{"sort", "-o", "out", "f"}, {"sort", "-uo", "out", "f"}, {"sort", "--out=out", "f"},
		{"sort", "-k1", "-o", "out", "f"}, {"sort", "-k", "1", "-o", "out", "f"},
		{"tree", "-o", "out"}, {"tree", "-ao", "out"}, {"tree", "-R", "-H", ".", "-L", "1"},
		// git: options before the subcommand, --output, grep -O, and the non-listing forms of
		// branch, tag, remote and config.
		{"git", "-c", "core.pager=./x", "log"}, {"git", "-C", "/tmp", "status"},
		{"git", "--exec-path=/x", "status"}, {"git", "--config-env=core.pager=X", "log"},
		{"git", "log", "--output=x"}, {"git", "diff", "--output", "x"}, {"git", "show", "--output=x", "HEAD"},
		{"git", "grep", "-O./x", "p"}, {"git", "grep", "-O", "./x", "p"}, {"git", "grep", "-nO./x", "p"},
		{"git", "grep", "--open-files-in-pager=./x", "p"}, {"git", "grep", "--open=./x", "p"},
		{"git", "grep", "--op", "p"},
		{"git", "branch", "-D", "main"}, {"git", "branch", "-d", "x"}, {"git", "branch", "new"},
		{"git", "branch", "-m", "a", "b"}, {"git", "branch", "-f", "main", "HEAD"},
		{"git", "branch", "--set-upstream-to=x"}, {"git", "branch", "-c", "a", "b"},
		{"git", "branch", "--", "x"}, {"git", "branch", "-a", "x"},
		{"git", "tag", "v1"}, {"git", "tag", "-d", "v1"}, {"git", "tag", "-a", "v1", "-m", "x"},
		{"git", "tag", "-v", "v1"}, {"git", "tag", "-f", "v1"}, {"git", "tag", "-nx"},
		{"git", "remote", "add", "x", "https://e"}, {"git", "remote", "remove", "origin"},
		{"git", "remote", "set-url", "origin", "x"}, {"git", "remote", "rename", "a", "b"},
		{"git", "remote", "prune", "origin"}, {"git", "remote", "--weird"},
		{"git", "config", "-e"}, {"git", "config", "set", "user.name", "x"}, {"git", "config", "edit"},
		{"git", "config", "unset", "user.name"}, {"git", "config", "--unset", "user.name"},
		{"git", "config", "user.name", "x"}, {"git", "push"}, {"git", "commit", "-m", "x"},
		// go: test, fmt, -vettool, -toolexec, env -w.
		{"go", "test", "./..."}, {"go", "fmt", "./..."}, {"go", "vet", "-vettool=./x", "./..."},
		{"go", "list", "-toolexec=./x"}, {"go", "env", "-w", "GOFLAGS=x"},
	} {
		if d := Check(c[0], c[1:]); d.Allowed {
			t.Errorf("%q must be refused in read-only mode, got allowed: %s", c, d.Reason)
		} else if d.Reason == "" {
			t.Errorf("%q: a refusal must explain itself", c)
		}
	}
}

func TestReadOnlyModeStillReadsWithTheSamePrograms(t *testing.T) {
	for _, c := range []argv{
		{"sed", "-n", "1,300p", "f"}, {"sed", "-n", "'1,300p'", "f"}, {"sed", "s/a/b/g", "f"},
		{"sed", "-E", "s/(a|b)/c/2", "f"}, {"sed", "-n", "/start/,/end/p", "f"}, {"sed", "-n", "/a/I p", "f"},
		{"sed", "-n", "$p", "f"}, {"sed", "-n", "1~2p;2,+3p", "f"}, {"sed", "-n", "0,/a/p", "f"},
		{"sed", "1!G;h;$!d", "f"}, {"sed", "-n", ":a;N;$!ba;p", "f"}, {"sed", "1a hello; w out", "f"},
		{"sed", "y/ab/cd/", "f"}, {"sed", "s/[/]/X/", "f"}, {"sed", "s/[]/]/X/", "f"},
		{"sed", "s/[^]/]/X/", "f"}, {"sed", "s/[[:alpha:]/]/X/", "f"}, {"sed", "s|a|b|", "f"},
		{"sed", "-n", "1{p}", "f"}, {"sed", "# comment\np", "f"}, {"sed", "-n", "p # trailing", "f"},
		{"sed", "q5", "f"}, {"sed", "l 20", "f"}, {"sed", "bend;:end", "f"}, {"sed", "=", "f"},
		{"sed", `\%a%d`, "f"}, {"sed", `s/a\/b/c/`, "f"}, {"sed", "-n", "/a/Mp", "f"},
		{"sed", "-ne", "p", "f"}, {"sed", "-nep", "f"}, {"sed", "--quiet", "--expression=p", "f"},
		{"sed", "--expression", "p", "f"}, {"sed", "-l", "80", "-n", "l", "f"},
		{"sed", "--line-length=80", "l", "f"}, {"sed", "--line-length", "80", "l", "f"},
		{"sed", "-s", "-u", "-z", "-b", "-r", "p", "f"}, {"sed", "--posix", "--debug", "--sandbox", "p", "f"},
		{"sed", "--", "p", "f"}, {"sed"}, {"sed", "-e"}, {"sed", "--expression"}, {"sed", "s/a/b/I", "f"},
		{"awk", "{print}"}, {"awk", "'{print $1}'", "f"}, {"awk", "-F:", "{print $1}", "f"},
		{"awk", "-F", ":", "{print}", "f"}, {"awk", "-v", "n=1", "{print n}", "f"}, {"awk", "-vn=1", "{print}", "f"},
		{"awk", "$1 > 5", "f"}, {"awk", "a || b", "f"}, {"awk", "{x = a > b}", "f"},
		{"awk", "{if ($1 > 2) print}", "f"}, {"awk", "{print ($1 > 2)}", "f"}, {"awk", "{print a[$1>2]}", "f"},
		{"awk", "/a|b/", "f"}, {"awk", "/[/]/", "f"}, {"awk", `{print "a>b|c"}`, "f"},
		{"awk", "{print x++ / 2}", "f"}, {"awk", "{print 1.5e3 / 2}", "f"}, {"awk", "{print $NF/2}", "f"},
		{"awk", "# system\n{print}", "f"}, {"awk", `{print "\"q\""}`, "f"}, {"awk", `/a\/b/`, "f"},
		{"awk", "{print \\\n$1}", "f"}, {"awk", "{print}\n$1 > 2", "f"}, {"awk", "{print}; $1 > 2", "f"},
		{"awk", "--field-separator=:", "--assign=a=1", "{print}", "f"},
		{"awk", "--field-separator", ":", "{print}", "f"}, {"awk", "--posix", "--re-interval", "{print}", "f"},
		{"awk", "-P", "-b", "{print}", "f"}, {"awk", "-e", "{print}", "f"}, {"awk", "--source={print}", "f"},
		{"awk", "--", "{print}", "f"}, {"awk"}, {"awk", "-e"}, {"awk", "--source"},
		{"awk", "-", "{print}"},
		{"find", ".", "-name", "x", "-print"}, {"rg", "-n", "pattern"}, {"rg", "--pre-globx", "p"},
		{"sort", "-n", "f"}, {"sort", "-k", "2", "-t", ",", "f"}, {"sort", "-k2", "f"},
		{"sort", "--random-source=f", "f"}, {"sort", "--reverse", "f"}, {"sort", "--", "-o"},
		{"sort", "--c", "f"},
		{"tree", "-L", "2"}, {"tree", "--noreport"},
		{"git", "status"}, {"git", "--no-pager", "log", "-5"}, {"git", "-P", "diff"}, {"git"},
		{"git", "grep", "-n", "x"}, {"git", "grep", "-e", "Owner"}, {"git", "grep", "--or", "x"},
		{"git", "branch"}, {"git", "branch", "-a"}, {"git", "branch", "-vv"},
		{"git", "branch", "-r", "--list", "origin/*"}, {"git", "branch", "--contains", "HEAD"},
		{"git", "branch", "--sort=-committerdate", "--format=%(refname)"}, {"git", "branch", "-l", "feat*"},
		{"git", "branch", "--show-current"}, {"git", "branch", "--merged", "main"},
		{"git", "branch", "--list", "--", "x"},
		{"git", "tag"}, {"git", "tag", "-l", "v1*"}, {"git", "tag", "--list"}, {"git", "tag", "-n5"},
		{"git", "tag", "-n"}, {"git", "tag", "--points-at", "HEAD"}, {"git", "tag", "--sort=v:refname"},
		{"git", "tag", "--contains=HEAD"},
		{"git", "remote"}, {"git", "remote", "-v"}, {"git", "remote", "get-url", "origin"},
		{"git", "remote", "show", "origin"},
		{"git", "config", "user.name"}, {"git", "config", "--get", "user.name"}, {"git", "config", "list"},
		{"go", "vet", "./..."}, {"go", "env", "GOPATH"}, {"go", "version"}, {"go", "list", "-m", "all"},
	} {
		if d := Check(c[0], c[1:]); !d.Allowed {
			t.Errorf("%q only reads and must be allowed, got: %s", c, d.Reason)
		}
	}
}

func TestResolveLongMatchesGetopt(t *testing.T) {
	known := []string{"--output", "--check", "--compress-program"}
	for name, want := range map[string]string{
		"--output": "--output", "--out": "--output", "--co": "--compress-program",
		"--c": "", "--x": "", "--": "",
	} {
		if got := resolveLong(name, known); got != want {
			t.Errorf("resolveLong(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestUnquoteRemovesOneLayer(t *testing.T) {
	for in, want := range map[string]string{
		"'p'": "p", `"p"`: "p", "'p": "'p", "p": "p", "'": "'", `'"'"'`: `"'"`,
	} {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTheRefusalsNameWhatTheyFound: the reason is what the model reads to propose something
// else, so it must name the command or option that was refused.
func TestTheRefusalsNameWhatTheyFound(t *testing.T) {
	for _, tc := range []struct {
		c    argv
		want string
	}{
		{argv{"sed", "w out", "f"}, "writes a file"},
		{argv{"sed", "e id", "f"}, "runs a command"},
		{argv{"sed", "r x", "f"}, "reads another file"},
		{argv{"sed", "s/a/b/w x", "f"}, "flag writes a file"},
		{argv{"awk", `BEGIN{"id"|getline}`}, "pipe"}, {argv{"awk", "{getline}"}, "getline"},
		{argv{"rg", "--pre=x", "p"}, "rg --pre runs"},
		{argv{"go", "test"}, "runs the package's code"},
		{argv{"git", "grep", "-Ox"}, "runs a program"},
		{argv{"./evil/cat", "f"}, "bare name"},
	} {
		if d := Check(tc.c[0], tc.c[1:]); !strings.Contains(d.Reason, tc.want) {
			t.Errorf("%q: the refusal %q must mention %q", tc.c, d.Reason, tc.want)
		}
	}
}

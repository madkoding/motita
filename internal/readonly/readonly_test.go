package readonly

import (
	"strings"
	"testing"
)

// TestReadersAreAllowed: the programs the mode exists to run.
func TestReadersAreAllowed(t *testing.T) {
	cases := []struct {
		command string
		args    []string
	}{
		{"cat", []string{"/etc/hostname"}},
		{"grep", []string{"-n", "root", "/etc/passwd"}},
		{"ls", []string{"-la", "/var/log"}},
		{"du", []string{"-sh", "/var"}},
		{"df", []string{"-h"}},
		{"ps", []string{"aux"}},
		{"journalctl", []string{"-u", "nginx", "-n", "50"}},
		{"uname", []string{"-a"}},
		{"go", []string{"test", "./..."}},
		{"go", []string{"version"}},
		{"git", []string{"status"}},
		{"git", []string{"log", "--oneline", "-5"}},
		{"systemctl", []string{"status", "nginx"}},
	}
	for _, tc := range cases {
		d := Check(tc.command, tc.args)
		if !d.Allowed {
			t.Errorf("%s %v must be allowed, got refused: %s", tc.command, tc.args, d.Reason)
		}
	}
}

// TestWritersAreRefused: the whole point of the mode.
func TestWritersAreRefused(t *testing.T) {
	cases := []struct {
		command string
		args    []string
	}{
		{"rm", []string{"-rf", "/tmp/x"}},
		{"mv", []string{"a", "b"}},
		{"cp", []string{"a", "b"}},
		{"tee", []string{"/etc/passwd"}},
		{"touch", []string{"new"}},
		{"mkdir", []string{"dir"}},
		{"chmod", []string{"777", "f"}},
		{"dd", []string{"if=/dev/zero", "of=/dev/sda"}},
		{"apt-get", []string{"install", "x"}},
		{"pip", []string{"install", "x"}},
		{"sudo", []string{"anything"}},
		{"kill", []string{"-9", "1"}},
		{"shutdown", []string{"-h", "now"}},
		{"curl", []string{"-o", "f", "http://x"}},
		{"make", []string{"install"}},
		{"sed", []string{"-i", "s/a/b/", "f"}},
		{"python3", []string{"-c", "open('/tmp/x','w').write('y')"}},
	}
	for _, tc := range cases {
		d := Check(tc.command, tc.args)
		if d.Allowed {
			t.Errorf("%s %v must be refused", tc.command, tc.args)
		}
		if d.Reason == "" {
			t.Errorf("%s %v: a refusal must explain itself", tc.command, tc.args)
		}
	}
}

// TestShellsAreRefused: a shell is the loophole that would defeat the policy, because
// the line it carries can do anything.
func TestShellsAreRefused(t *testing.T) {
	for _, name := range []string{"sh", "bash", "zsh", "dash", "fish", "busybox"} {
		d := Check(name, []string{"-c", "rm -rf /"})
		if d.Allowed {
			t.Errorf("%q must be refused: it can run anything", name)
		}
	}
	// Even a "harmless" shell line is refused: the point is that the command cannot
	// be classified, not that this particular one happens to be harmless.
	if d := Check("sh", []string{"-c", "echo hi"}); d.Allowed {
		t.Error("a shell must be refused even for a harmless line")
	}
}

// TestUnknownCommandsAreRefused: the important default. A command nobody classified
// cannot be assumed harmless.
func TestUnknownCommandsAreRefused(t *testing.T) {
	d := Check("some-brand-new-tool", []string{"--read"})
	if d.Allowed {
		t.Error("an unknown command must be refused, not allowed")
	}
	if d.Reason == "" {
		t.Error("the refusal must explain how to allow it")
	}
}

// TestFindDependsOnItsArguments: reading with find is fine, deleting with it is not.
func TestFindDependsOnItsArguments(t *testing.T) {
	if d := Check("find", []string{"/var", "-name", "*.log"}); !d.Allowed {
		t.Errorf("find for reading must be allowed: %s", d.Reason)
	}
	for _, bad := range [][]string{
		{"/tmp", "-delete"},
		{"/tmp", "-name", "x", "-exec", "rm", "{}", ";"},
		{"/tmp", "-fprint", "/tmp/out"},
		{".", "-execdir", "sh", "-c", "x", ";"},
	} {
		if d := Check("find", bad); d.Allowed {
			t.Errorf("find %v must be refused", bad)
		}
	}
}

// TestGitDependsOnItsSubcommand: inspecting a repository is read-only, changing it is
// not.
func TestGitDependsOnItsSubcommand(t *testing.T) {
	allowed := [][]string{
		{"status"}, {"log"}, {"diff"}, {"show", "HEAD"}, {"branch", "-a"},
		{"remote", "-v"}, {"rev-parse", "HEAD"}, {"ls-files"}, {"blame", "f"},
		{"config", "--get", "user.name"}, {"config", "--list"},
	}
	for _, args := range allowed {
		if d := Check("git", args); !d.Allowed {
			t.Errorf("git %v must be allowed: %s", args, d.Reason)
		}
	}
	refused := [][]string{
		{"commit", "-m", "x"}, {"push"}, {"pull"}, {"checkout", "main"},
		{"reset", "--hard"}, {"clean", "-fd"}, {"rm", "f"}, {"merge", "x"},
		{"config", "user.name", "x"}, {"config", "--unset", "user.name"},
		{"add", "."}, {"init"}, {"clone", "x"},
	}
	for _, args := range refused {
		if d := Check("git", args); d.Allowed {
			t.Errorf("git %v must be refused", args)
		}
	}
}

// TestSystemctlDependsOnItsSubcommand.
func TestSystemctlDependsOnItsSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"status", "nginx"}, {"show", "nginx"}, {"is-active", "nginx"},
		{"list-units"}, {"cat", "nginx"}, {"list-timers"},
	} {
		if d := Check("systemctl", args); !d.Allowed {
			t.Errorf("systemctl %v must be allowed: %s", args, d.Reason)
		}
	}
	for _, args := range [][]string{
		{"start", "nginx"}, {"stop", "nginx"}, {"restart", "nginx"},
		{"enable", "nginx"}, {"disable", "nginx"}, {"daemon-reload"},
	} {
		if d := Check("systemctl", args); d.Allowed {
			t.Errorf("systemctl %v must be refused", args)
		}
	}
}

// TestGoDependsOnItsSubcommand: `go test` is the check this project is built around,
// so it must be allowed; writing a binary out is not.
func TestGoDependsOnItsSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"test", "./..."}, {"test", "-run", "TestX", "./..."}, {"version"},
		{"env", "GOPATH"}, {"list", "./..."}, {"doc", "fmt"},
	} {
		if d := Check("go", args); !d.Allowed {
			t.Errorf("go %v must be allowed: %s", args, d.Reason)
		}
	}
	for _, args := range [][]string{
		{"install", "x"}, {"get", "x"}, {"mod", "tidy"}, {"generate"},
		{"run", "."}, {"build"}, {"test", "-c"}, {"fmt", "-w"},
	} {
		if d := Check("go", args); d.Allowed {
			t.Errorf("go %v must be refused", args)
		}
	}
}

// TestTheCommandIsTakenFromItsBaseName: /usr/bin/grep is grep.
func TestTheCommandIsTakenFromItsBaseName(t *testing.T) {
	if d := Check("/usr/bin/grep", []string{"x", "f"}); !d.Allowed {
		t.Errorf("an absolute path to a reader must be allowed: %s", d.Reason)
	}
	if d := Check("/bin/rm", []string{"-rf", "/"}); d.Allowed {
		t.Error("an absolute path to a writer must be refused")
	}
}

// TestEmptyCommandIsRefused: an empty command gets its own message, not "not on the
// list". `filepath.Base("")` is ".", which is why the check happens before the base
// name is taken.
func TestEmptyCommandIsRefused(t *testing.T) {
	for _, c := range []string{"", "   ", "\t", ".", "/", "  .  "} {
		d := Check(c, nil)
		if d.Allowed {
			t.Errorf("%q must be refused", c)
		}
		if !strings.Contains(d.Reason, "no command") {
			t.Errorf("%q must say there is no command, got %q", c, d.Reason)
		}
	}
}

// TestTheCaseDoesNotMatter: RM is rm.
func TestTheCaseDoesNotMatter(t *testing.T) {
	if d := Check("RM", []string{"-rf", "/"}); d.Allowed {
		t.Error("the name must be compared without case")
	}
	if d := Check("CAT", []string{"f"}); !d.Allowed {
		t.Error("CAT must be allowed as cat")
	}
}

// TestEveryReaderIsReachable: every entry in the allowlist must be a name the Check
// function accepts, so the table and the policy cannot drift apart.
func TestEveryReaderIsReachable(t *testing.T) {
	for name := range readers {
		d := Check(name, nil)
		if !d.Allowed && !d.ReasonAllowsMore() {
			// A reader with an argument rule may refuse with no arguments; what must
			// not happen is a reader refused as "not on the list".
			if d.Reason != "" && stringContains(d.Reason, "not on the read-only list") {
				t.Errorf("%q is in the allowlist but Check says it is not", name)
			}
		}
	}
}

// TestEveryWriterIsRefused: the two tables must not overlap, and nothing in the
// writer table may be allowed.
func TestEveryWriterIsRefused(t *testing.T) {
	for name := range writers {
		if d := Check(name, nil); d.Allowed {
			t.Errorf("%q is in the writer table but Check allows it", name)
		}
		if readers[name] {
			t.Errorf("%q is in BOTH tables: the policy contradicts itself", name)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func stringContains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// ReasonAllowsMore reports whether the verdict was "refused for its arguments"
// rather than "not on the list". It keeps the reachability test readable.
func (d Decision) ReasonAllowsMore() bool {
	return d.Reason != "" && !stringContains(d.Reason, "not on the read-only list")
}

// TestEnvAndCommandAreWhatTheyRun: env and command run their operand as a program, so
// `env rm -rf x` is rm and must be refused like rm, while env and command -v alone read.
func TestEnvAndCommandAreWhatTheyRun(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"env", nil}, {"env", []string{"-i", "-u", "X", "--unset=Y", "A=b", "--", "cat", "f"}},
		{"env", []string{"-C", "/tmp", "-0", "ls"}}, {"env", []string{"--"}},
		{"command", []string{"-v", "rm"}}, {"command", []string{"-p", "cat", "f"}},
		{"command", nil}, {"env", []string{"env", "grep", "x", "f"}},
	} {
		if d := Check(tc.command, tc.args); !d.Allowed {
			t.Errorf("%s %v must be allowed: %s", tc.command, tc.args, d.Reason)
		}
	}
	for _, tc := range []struct {
		command string
		args    []string
		kind    Kind
	}{
		{"env", []string{"rm", "-rf", "/tmp/x"}, KindWriter},
		{"env", []string{"A=b", "sh", "-c", "rm -rf ~"}, KindShell},
		{"env", []string{"-S", "rm -rf x"}, KindShell},
		{"env", []string{"--chdir=/", "curl", "x"}, KindWriter},
		{"env", []string{"--bogus", "cat"}, KindUnknown},
		{"command", []string{"rm", "x"}, KindWriter},
		{"command", []string{"--", "rm", "x"}, KindWriter},
		{"command", []string{"-p", "some-new-tool"}, KindUnknown},
	} {
		if kind, _ := Classify(tc.command, tc.args); kind != tc.kind {
			t.Errorf("%s %v: kind %d, want %d", tc.command, tc.args, kind, tc.kind)
		}
		d := Check(tc.command, tc.args)
		if d.Allowed || d.Reason == "" {
			t.Errorf("%s %v must be refused with a reason, got %+v", tc.command, tc.args, d)
		}
	}
	if d := Check("env", []string{"--bogus"}); !strings.Contains(d.Reason, "--bogus") {
		t.Errorf("an unknown env option must be named, got %q", d.Reason)
	}
}

// TestReadersThatCanWriteAFileAreRefusedInThatForm: sort -o, uniq IN OUT, xxd IN OUT,
// tree -o, find -fls and yq -i write, so plan mode refuses those forms only.
func TestReadersThatCanWriteAFileAreRefusedInThatForm(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"sort", []string{"-rn", "-k", "2", "f"}}, {"uniq", []string{"-c", "-f", "1", "in"}},
		{"xxd", []string{"-l", "16", "in"}}, {"xxd", []string{"-"}}, {"tree", []string{"-L", "2"}},
		{"yq", []string{"-I", "2", ".a", "f.yaml"}}, {"find", []string{".", "-ls"}},
	} {
		if d := Check(tc.command, tc.args); !d.Allowed {
			t.Errorf("%s %v must be allowed: %s", tc.command, tc.args, d.Reason)
		}
	}
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"sort", []string{"-o", "/tmp/out", "f"}}, {"sort", []string{"-ro/tmp/out", "f"}},
		{"sort", []string{"--output=/tmp/out", "f"}}, {"sort", []string{"--compress-program=sh", "f"}},
		{"uniq", []string{"in", "/tmp/out"}}, {"xxd", []string{"in", "/tmp/out"}},
		{"tree", []string{"-o", "/tmp/out"}}, {"find", []string{".", "-fls", "/tmp/out"}},
		{"yq", []string{"-i", ".a=1", "f.yaml"}}, {"yq", []string{"--inplace", ".a=1", "f.yaml"}},
	} {
		if d := Check(tc.command, tc.args); d.Allowed || d.Reason == "" {
			t.Errorf("%s %v must be refused with a reason, got %+v", tc.command, tc.args, d)
		}
	}
}

// TestGitRefsAndOutputFilesAreWrites: listing branches, tags and remotes reads;
// creating, deleting or renaming them, or diffing into a file, writes.
func TestGitRefsAndOutputFilesAreWrites(t *testing.T) {
	for _, args := range [][]string{
		{"branch"}, {"branch", "-avv"}, {"branch", "--list", "feat*"},
		{"branch", "--contains", "HEAD"}, {"branch", "--sort=-committerdate", "--no-color"},
		{"branch", "-r"}, {"tag"}, {"tag", "-l", "v*"}, {"tag", "-n5"},
		{"tag", "--verify", "v1"}, {"remote"}, {"remote", "-v"}, {"remote", "show", "origin"},
		{"remote", "get-url", "origin"}, {"diff", "--output-indicator-new=+"},
	} {
		if d := Check("git", args); !d.Allowed {
			t.Errorf("git %v must be allowed: %s", args, d.Reason)
		}
	}
	for _, args := range [][]string{
		{"branch", "-D", "main"}, {"branch", "-dr", "origin/x"}, {"branch", "new"},
		{"branch", "--delete", "x"}, {"branch", "--set-upstream-to=origin/x"},
		{"branch", "--", "new"}, {"tag", "-d", "v1"}, {"tag", "v2"}, {"tag", "-am", "x", "v2"},
		{"remote", "remove", "origin"}, {"remote", "add", "x", "url"},
		{"remote", "set-url", "origin", "url"}, {"diff", "--output=/tmp/out"},
		{"log", "--output", "/tmp/out"},
	} {
		if d := Check("git", args); d.Allowed || d.Reason == "" {
			t.Errorf("git %v must be refused with a reason, got %+v", args, d)
		}
	}
}

// TestGoEnvWriteAndGoFmtAreRefused: go env -w/-u write the go env file and go fmt
// rewrites sources even without flags.
func TestGoEnvWriteAndGoFmtAreRefused(t *testing.T) {
	for _, args := range [][]string{
		{"env", "-w", "GOFLAGS=-x"}, {"env", "-u", "GOFLAGS"}, {"fmt", "./..."},
	} {
		if d := Check("go", args); d.Allowed || d.Reason == "" {
			t.Errorf("go %v must be refused with a reason, got %+v", args, d)
		}
	}
}

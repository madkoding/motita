package readonly

import (
	"strings"
	"testing"
)

// onWindows makes the network-path rule answer as it does on Windows for one test.
func onWindows(t *testing.T, windows bool) {
	t.Helper()
	old := windowsNetworkPaths
	windowsNetworkPaths = windows
	t.Cleanup(func() { windowsNetworkPaths = old })
}

// On Windows a reader handed a network path connects to that machine and signs in as the user,
// which hands it the user's NTLM hash: plan mode refuses the line whatever the reader.
func TestPlanModeRefusesNetworkPathsOnWindows(t *testing.T) {
	onWindows(t, true)
	for _, args := range [][]string{
		{"findstr", "x", `\\host\s\f`},
		{"more", `\\host\s\f`},
		{"tree", `\\host\share`},
		{"cat", "//host/share/f"},
		{"get-content", `-Path:\\host\s`},
		{"grep", "--file=//host/s/p", "x"},
		{"env", "cat", `\\host\s`},
	} {
		d := Check(args[0], args[1:])
		if d.Allowed || !strings.Contains(d.Reason, "network path") {
			t.Errorf("%v: got %+v, want a refusal naming the network path", args, d)
		}
	}
	// A URL is not a network path, and a plain local path is untouched.
	for _, args := range [][]string{
		{"echo", "https://example.com/a"}, {"cat", `src\a.go`}, {"grep", "-rn", "x", "."},
	} {
		if d := Check(args[0], args[1:]); !d.Allowed {
			t.Errorf("%v: got %+v, want allowed", args, d)
		}
	}
}

// Elsewhere `//` is a plain path and `\\` no separator: a search for `//go:build` stays a read.
func TestNetworkPathsAreWindowsOnly(t *testing.T) {
	onWindows(t, false)
	for _, args := range [][]string{
		{"grep", "-rn", "//go:build", "."}, {"cat", `\\host\s\f`},
	} {
		if d := Check(args[0], args[1:]); !d.Allowed {
			t.Errorf("%v: got %+v, want allowed", args, d)
		}
	}
}

func TestNetworkPathAtKnowsItsForms(t *testing.T) {
	for s, want := range map[string]bool{
		`\\h`: true, "//h": true, "x//h": true, "https://h": false, "git+ssh://h": false,
		"a=https://h": false, "1://h": true, "://h": true, "/a/b": false, `\a`: false, "x": false,
		"-Uri=git+ssh://h": false, "(s://h": true,
	} {
		runes := []rune(s)
		got := false
		for i := range runes {
			got = got || NetworkPathAt(runes, i)
		}
		if got != want {
			t.Errorf("NetworkPathAt(%q) = %v, want %v", s, got, want)
		}
	}
}

// `file -C` compiles a magic file and writes it; it is not the reader `file` is.
func TestFileCompileWrites(t *testing.T) {
	for _, args := range [][]string{
		{"-C"}, {"-bC", "-m", "x"}, {"--compile"}, {"--comp"}, {"--co"},
	} {
		if d := Check("file", args); d.Allowed {
			t.Errorf("file %v: got allowed, want refused", args)
		}
	}
	for _, args := range [][]string{{"x"}, {"-b", "x"}, {"--mime", "x"}, {"-m", "magic.C", "x"}} {
		if d := Check("file", args); !d.Allowed {
			t.Errorf("file %v: got %s, want allowed", args, d.Reason)
		}
	}
}

// `--ext-diff` and `--textconv` make git run the program a configuration names.
func TestGitDiffDriversAreRefused(t *testing.T) {
	for _, args := range [][]string{
		{"diff", "--ext-diff"}, {"log", "-p", "--ext"}, {"show", "--textconv", "HEAD"},
		{"diff", "--textc"}, {"log", "--textconv=x"},
	} {
		if d := Check("git", args); d.Allowed || !strings.Contains(d.Reason, "diff driver") {
			t.Errorf("git %v: got %+v, want refused", args, d)
		}
	}
	for _, args := range [][]string{
		{"diff", "--no-ext-diff"}, {"diff", "--no-textconv"}, {"diff", "--text"}, {"log", "--ex"},
	} {
		if d := Check("git", args); !d.Allowed {
			t.Errorf("git %v: got %s, want allowed", args, d.Reason)
		}
	}
}

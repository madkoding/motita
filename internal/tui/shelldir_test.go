//go:build !windows

package tui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	taskpkg "github.com/madkoding/motita/internal/task"
)

// lineAgent records every line it is asked to run and answers with out and exit.
type lineAgent struct {
	fakeAgent
	lines []string
	out   string
	exit  int
}

func (l *lineAgent) RunCommand(_ context.Context, line string) (string, int, error) {
	l.lines = append(l.lines, line)
	return l.out, l.exit, nil
}

// projectRunner is a runner whose workspace is a project folder holding a repository one level
// down, the shape of the report: <ws>/.motita and <ws>/motita/.git.
func projectRunner(t *testing.T) (*AppRunner, *lineAgent, string) {
	t.Helper()
	ws := t.TempDir()
	for _, dir := range []string{".motita", filepath.Join("motita", ".git"), filepath.Join("motita", "web")} {
		if err := os.MkdirAll(filepath.Join(ws, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Agent.WorkspaceDir = ws
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, &sandbox.Sandbox{}, logx.Global())
	ag := &lineAgent{}
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return ag
	}
	return r, ag, ws
}

func run(t *testing.T, r *AppRunner, line string) (string, int, error) {
	t.Helper()
	return r.RunShell(context.Background(), line, func(context.Context, agent.ApprovalRequest) (bool, error) {
		return true, nil
	})
}

// TestATypedCdIsKeptForTheNextLines is the report: `cd motita` answered [ok] and the next `git
// checkout .` still ran in the project folder. The cd now holds, and later lines run there.
func TestATypedCdIsKeptForTheNextLines(t *testing.T) {
	r, ag, _ := projectRunner(t)
	if out, exit, err := run(t, r, "cd motita"); out != "" || exit != 0 || err != nil {
		t.Fatalf("cd = %q %d %v", out, exit, err)
	}
	if len(ag.lines) != 0 {
		t.Errorf("a lone cd runs nothing: %q", ag.lines)
	}
	run(t, r, "git checkout .")
	run(t, r, "cd web")
	run(t, r, "pwd")
	want := []string{"cd 'motita' && git checkout .", "cd 'motita/web' && pwd"}
	if strings.Join(ag.lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q, want %q", ag.lines, want)
	}
}

// TestCdMovesLikeAShell: a bare cd goes back to the workspace, `cd -` to the folder before, `..`
// climbs, quotes are read and an absolute folder inside the workspace is taken.
func TestCdMovesLikeAShell(t *testing.T) {
	r, ag, ws := projectRunner(t)
	steps := []struct{ cd, want string }{
		{"cd 'motita'", "cd 'motita' && x"},
		{"cd ..", "x"},
		{"cd -", "cd 'motita' && x"},
		{"cd", "x"},
		{"cd " + filepath.Join(ws, "motita", "web"), "cd 'motita/web' && x"},
		{"cd ~", "x"},
		{`cd "motita"`, "cd 'motita' && x"},
	}
	for _, s := range steps {
		if _, exit, err := run(t, r, s.cd); exit != 0 || err != nil {
			t.Fatalf("%s = %d %v", s.cd, exit, err)
		}
		ag.lines = nil
		run(t, r, "x")
		if ag.lines[0] != s.want {
			t.Errorf("after %q the line is %q, want %q", s.cd, ag.lines[0], s.want)
		}
	}
}

// TestCdRefusesWhatIsNotAFolderOfTheWorkspace: a missing folder, a file and a folder outside the
// workspace (directly or through a link) answer like a shell's cd and leave the folder where it was.
func TestCdRefusesWhatIsNotAFolderOfTheWorkspace(t *testing.T) {
	r, ag, ws := projectRunner(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "out")); err != nil {
		t.Fatal(err)
	}
	run(t, r, "cd motita")
	for _, line := range []string{"cd nope", "cd ../notes.txt", "cd " + outside, "cd ../out", "cd ../..", "cd ~/x"} {
		out, exit, err := run(t, r, line)
		if exit != 1 || err == nil || !strings.HasPrefix(out, "cd: ") {
			t.Errorf("%s = %q %d %v", line, out, exit, err)
		}
	}
	ag.lines = nil
	run(t, r, "ls")
	if ag.lines[0] != "cd 'motita' && ls" {
		t.Errorf("a refused cd moves nothing: %q", ag.lines[0])
	}
}

// TestACdInsideALineRunsAsWritten: `cd x && make` and other lines with more than a cd go to the
// shell as they are and keep nothing, as they always did.
func TestACdInsideALineRunsAsWritten(t *testing.T) {
	r, ag, _ := projectRunner(t)
	for _, line := range []string{"cd motita && make", "cd -P motita", "cd $HOME", "cd 'my dir"} {
		run(t, r, line)
	}
	run(t, r, "pwd")
	if got := ag.lines[len(ag.lines)-1]; got != "pwd" {
		t.Errorf("no lone cd ran, so pwd runs in the workspace: %q (all %q)", got, ag.lines)
	}
}

// TestALineForADeletedFolderDoesNotRunElsewhere: the line was meant for a folder that is gone, so
// it does not run at the workspace in its place; the folder goes back to the workspace.
func TestALineForADeletedFolderDoesNotRunElsewhere(t *testing.T) {
	r, ag, ws := projectRunner(t)
	run(t, r, "cd motita/web")
	if err := os.RemoveAll(filepath.Join(ws, "motita", "web")); err != nil {
		t.Fatal(err)
	}
	out, exit, err := run(t, r, "rm -rf build")
	if exit != 1 || !errors.Is(err, errFolderGone) || !strings.Contains(out, "nothing ran") || len(ag.lines) != 0 {
		t.Fatalf("= %q %d %v (ran %q)", out, exit, err, ag.lines)
	}
	run(t, r, "ls")
	if ag.lines[0] != "ls" {
		t.Errorf("back at the workspace: %q", ag.lines[0])
	}
}

// TestANewWorkspaceStartsAtItsTop: the folder belongs to the old workspace.
func TestANewWorkspaceStartsAtItsTop(t *testing.T) {
	r, ag, _ := projectRunner(t)
	run(t, r, "cd motita")
	r.SetWorkspace(t.TempDir())
	run(t, r, "ls")
	if ag.lines[0] != "ls" {
		t.Errorf("line = %q", ag.lines[0])
	}
}

// TestGitAtTheProjectTopPointsAtItsRepositories: the project folder holds its repositories one
// level down, and "not a git repository" there now says where they are.
func TestGitAtTheProjectTopPointsAtItsRepositories(t *testing.T) {
	r, ag, ws := projectRunner(t)
	if err := os.WriteFile(filepath.Join(ws, "docs.git"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ag.out, ag.exit = "fatal: not a git repository (or any of the parent directories): .git\n", 128
	out, exit, _ := run(t, r, "git checkout .")
	if exit != 128 || !strings.Contains(out, "the repositories are in: motita/ - run `cd motita` first") {
		t.Errorf("= %q %d", out, exit)
	}
	run(t, r, "cd motita/web")
	if out, _, _ := run(t, r, "git status"); strings.Contains(out, "[this folder") {
		t.Errorf("no repository below: no hint, %q", out)
	}
	ag.out = "fatal: something else\n"
	if out, _, _ := run(t, r, "cd .."); out != "" {
		t.Fatal(out)
	}
	if out, _, _ := run(t, r, "git status"); strings.Contains(out, "[this folder") {
		t.Errorf("another failure: no hint, %q", out)
	}
}

func TestCdLineForEachShell(t *testing.T) {
	cases := map[string]string{
		"/bin/bash":      "cd 'a b' && ls",
		"powershell.exe": "cd 'a b' -ErrorAction Stop; ls",
		"cmd.exe":        `pushd "a b" && ls`,
	}
	for shell, want := range cases {
		if got := cdLine(shell, "a b", "ls"); got != want {
			t.Errorf("%s: %q, want %q", shell, got, want)
		}
	}
}

func TestReposBelowAnUnreadableFolder(t *testing.T) {
	if got := reposBelow(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Errorf("= %q", got)
	}
}

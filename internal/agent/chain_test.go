package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
)

// recorder answers each program with a scripted output and exit code, and remembers what ran.
type recorder struct {
	ran   []string
	reply map[string]struct {
		out  string
		code int
		err  error
	}
}

func (r *recorder) run(_ context.Context, p execx.Request) (string, bool, int, error) {
	key := strings.TrimSpace(p.Command + " " + strings.Join(p.Args, " "))
	r.ran = append(r.ran, key)
	rep := r.reply[key]
	return rep.out, false, rep.code, rep.err
}

func readOnlyAgent(t *testing.T, r *recorder) *Agent {
	a := bareAgent(t, func(c *config.Config) { c.Agent.ReadOnly = true })
	a.ExecCommand = r.run
	return a
}

type scripted = struct {
	out  string
	code int
	err  error
}

func TestReadOnlyLinesChainWithoutAShell(t *testing.T) {
	r := &recorder{reply: map[string]scripted{
		"git status":     {"clean", 0, nil},
		"git log":        {"c1\nc2\nc3\n", 0, nil},
		"cat missing":    {"no such file", 1, nil},
		"echo recovered": {"recovered\n", 0, nil},
	}}
	a := readOnlyAgent(t, r)
	cases := []struct {
		line string
		out  string
		exit int
		ran  string
	}{
		{"git status", "clean", 0, "git status"},
		{"git status; git log", "clean\nc1\nc2\nc3\n", 0, "git status|git log"},
		{"git status && git log | head -n 1", "clean\nc1\n", 0, "git status|git log"},
		{"cat missing && echo no", "no such file", 1, "cat missing"},
		{"cat missing || echo recovered", "no such file\nrecovered\n", 0, "cat missing|echo recovered"},
		{"git status || echo recovered", "clean", 0, "git status"},
		{"git log 2>&1 | grep c2 | wc -l", "1\n", 0, "git log"},
		{"git log | grep zzz", "", 1, "git log"},
		{"cat missing; git log | tail -1", "no such file\nc3\n", 0, "cat missing|git log"},
	}
	for _, tc := range cases {
		r.ran = nil
		out, exit, err := a.RunCommand(context.Background(), tc.line)
		if err != nil || out != tc.out || exit != tc.exit || strings.Join(r.ran, "|") != tc.ran {
			t.Errorf("%q = %q, %d, %v (ran %v); want %q, %d (ran %s)", tc.line, out, exit, err, r.ran,
				tc.out, tc.exit, tc.ran)
		}
	}
}

func TestAReadOnlyLineIsRefusedAsAWholeBeforeAnythingRuns(t *testing.T) {
	r := &recorder{}
	a := readOnlyAgent(t, r)
	for line, want := range map[string]string{
		"git status; rm -rf x":     `in "rm -rf x"`,
		"git status && git push":   `in "git push"`,
		"git status | sort":        "cannot follow a pipe",
		"git status | grep x file": "reads the pipe",
		"git status;":              "no command after it",
		"git status; echo $(pwd)":  "needs a shell",
		"git status > out":         "needs a shell",
		"rm x":                     "can change the system",
		"git status & git log":     "needs a shell",
		"a;b;c;d;e;f;g;h;i":        "at most 8",
		"":                         "no command to run",
	} {
		r.ran = nil
		out, exit, err := a.RunCommand(context.Background(), line)
		if err == nil || exit != 1 || !strings.Contains(out, "[refused:") || !strings.Contains(out, want) || len(r.ran) != 0 {
			t.Errorf("%q = %q, %d, %v (ran %v); want a refusal containing %q and nothing run", line, out, exit, err, r.ran, want)
		}
	}
}

func TestAReadOnlyLineStopsOnAnExecutionError(t *testing.T) {
	boom := errors.New("timed out")
	r := &recorder{reply: map[string]scripted{"git log": {"partial", -1, boom}}}
	a := readOnlyAgent(t, r)
	out, exit, err := a.RunCommand(context.Background(), "git log; git status")
	if !errors.Is(err, boom) || exit != -1 || out != "partial" || len(r.ran) != 1 {
		t.Errorf("= %q, %d, %v (ran %v)", out, exit, err, r.ran)
	}
}

func TestPlanModeReadsAFetchAndAPipe(t *testing.T) {
	r := &recorder{reply: map[string]scripted{"git fetch origin": {"From origin\n", 0, nil}}}
	a := readOnlyAgent(t, r)
	out, exit, err := a.RunCommand(context.Background(), "git fetch origin")
	if err != nil || exit != 0 || out != "From origin\n" {
		t.Errorf("= %q, %d, %v", out, exit, err)
	}
}

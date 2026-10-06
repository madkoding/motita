package agent

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/task"
)

func TestGoalOf(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/goal add a login page", "add a login page", true},
		{"  /goal   fix the build ", "fix the build", true},
		{"/goal", "/goal", false},
		{"/goalpost review", "/goalpost review", false},
		{"fix the build", "fix the build", false},
	}
	for _, c := range cases {
		got, ok := goalOf(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("goalOf(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDecisionsAreRecordedAndReturned(t *testing.T) {
	dir := t.TempDir()
	if err := recordDecisions(dir, "add login", nil); err != nil || ReadDecisions(dir) != "" {
		t.Fatalf("nothing decided must write nothing")
	}
	ds := []ReportDecision{{Decision: "use sessions", Why: "simplest"}, {Decision: "no OAuth"}}
	if err := recordDecisions(dir, "add login", ds); err != nil {
		t.Fatal(err)
	}
	got := ReadDecisions(dir)
	for _, want := range []string{"add login", "- use sessions — simplest", "- no OAuth"} {
		if !strings.Contains(got, want) {
			t.Errorf("decisions file missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(decisionsContext(dir), "use sessions") {
		t.Errorf("the next run must see the standing decisions")
	}
	if decisionsContext(t.TempDir()) != "" {
		t.Errorf("no file, no context")
	}
}

func TestNormalizeDropsEmptyDecisions(t *testing.T) {
	r := Report{Decisions: []ReportDecision{{Decision: " "}, {Decision: " x ", Why: " y "}}}
	r.normalize(true)
	if len(r.Decisions) != 1 || r.Decisions[0].Decision != "x" || r.Decisions[0].Why != "y" {
		t.Fatalf("decisions = %+v", r.Decisions)
	}
}

func TestRemoveDecision(t *testing.T) {
	dir := t.TempDir()
	_ = recordDecisions(dir, "a", []ReportDecision{{Decision: "one"}, {Decision: "two"}})
	_ = recordDecisions(dir, "b", []ReportDecision{{Decision: "three"}})
	if got := NumberedDecisions(dir); !strings.Contains(got, "2. two") || !strings.Contains(got, "3. three") {
		t.Fatalf("numbering:\n%s", got)
	}
	if got, err := RemoveDecision(dir, 3); err != nil || got != "three" {
		t.Fatalf("removed %q, %v", got, err)
	}
	if strings.Contains(ReadDecisions(dir), "— b") {
		t.Errorf("the emptied section's heading must go too:\n%s", ReadDecisions(dir))
	}
	if _, err := RemoveDecision(dir, 9); err == nil {
		t.Errorf("an unknown number must fail")
	}
	_, _ = RemoveDecision(dir, 1)
	_, _ = RemoveDecision(dir, 1)
	if ReadDecisions(dir) != "" {
		t.Errorf("an empty file must be removed")
	}
}

func goalRun(t *testing.T, s *scriptServer, text string, prepare func(dir string)) (*fixture, TaskResult) {
	t.Helper()
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, nil)
	src, err := task.NewText(text, "test")
	if err != nil {
		t.Fatal(err)
	}
	e.agent.source = src
	if prepare != nil {
		prepare(e.dir)
	}
	var got TaskResult
	e.agent.Observer = func(r TaskResult) { got = r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e, got
}

// A goal never asks, is told to decide, and what it decided is kept for the next run.
func TestAGoalDecidesAndRecordsWhatItDecided(t *testing.T) {
	s := &scriptServer{
		execute:    func(int, string) string { return step(true, "true") },
		synthReply: `{"summary":"done","decisions":[{"decision":"use sessions","why":"simplest"}]}`,
	}
	e, result := goalRun(t, s, "/goal add login", nil)
	if !result.Pass || e.agent.Interactive {
		t.Fatalf("pass=%v interactive=%v", result.Pass, e.agent.Interactive)
	}
	if len(s.analyses) == 0 || !strings.Contains(s.analyses[0], "This is a GOAL") || !strings.Contains(s.analyses[0], "add login") {
		t.Errorf("the model must be told it is a goal:\n%v", s.analyses)
	}
	if got := ReadDecisions(e.dir); !strings.Contains(got, "use sessions — simplest") {
		t.Errorf("the decision was not recorded:\n%s", got)
	}

	// The next ordinary task receives it as a standing decision.
	s2 := &scriptServer{execute: func(int, string) string { return step(true, "true") }}
	goalRun(t, s2, "do more", func(dir string) { _ = recordDecisions(dir, "x", []ReportDecision{{Decision: "keep sessions"}}) })
	if len(s2.analyses) == 0 || !strings.Contains(s2.analyses[0], "keep sessions") {
		t.Errorf("a later run must see the standing decisions:\n%v", s2.analyses)
	}
}

// A decisions file that cannot be written is reported, never fatal.
func TestAGoalSurvivesAnUnwritableDecisionsFile(t *testing.T) {
	s := &scriptServer{
		execute:    func(int, string) string { return step(true, "true") },
		synthReply: `{"summary":"done","decisions":[{"decision":"x"}]}`,
	}
	_, result := goalRun(t, s, "/goal go", func(dir string) {
		_ = os.WriteFile(filepath.Join(dir, ".motita"), []byte("a file, not a folder"), 0o644)
	})
	if !result.Pass {
		t.Fatalf("a failed save must not fail the goal: %+v", result)
	}
}

func TestDecisionsHelpersEdges(t *testing.T) {
	if recordDecisions("", "g", []ReportDecision{{Decision: "x"}}) != nil || ReadDecisions("") != "" {
		t.Error("no workspace, no file")
	}
	if NumberedDecisions(t.TempDir()) != "" {
		t.Error("no file, nothing to number")
	}
	long := strings.Repeat("a ", 200)
	if got := oneLine(long); len(got) > 125 {
		t.Errorf("oneLine = %d bytes", len(got))
	}
	dir := t.TempDir()
	_ = recordDecisions(dir, "g", []ReportDecision{{Decision: strings.Repeat("d", maxDecisionsContext)}})
	if len(decisionsContext(dir)) > maxDecisionsContext+200 {
		t.Error("the context handed to the model must be bounded")
	}
	// A folder where the file should be: neither read, written nor removed.
	bad := t.TempDir()
	_ = os.MkdirAll(filepath.Join(bad, DecisionsFile, "x"), 0o755)
	if err := recordDecisions(bad, "g", []ReportDecision{{Decision: "x"}}); err == nil {
		t.Error("writing over a folder must fail")
	}
	if _, err := RemoveDecision(t.TempDir(), 1); err == nil {
		t.Error("removing from nothing must fail")
	}
	ws := t.TempDir()
	_ = recordDecisions(ws, "g", []ReportDecision{{Decision: "one"}})
	_ = os.Remove(filepath.Join(ws, DecisionsFile))
	_ = os.MkdirAll(filepath.Join(ws, DecisionsFile), 0o755)
	if _, err := RemoveDecision(ws, 1); err == nil {
		t.Error("an unreadable file has no decisions")
	}
}

func TestRemovingAMiddleDecisionDropsItsHeading(t *testing.T) {
	dir := t.TempDir()
	_ = recordDecisions(dir, "first", []ReportDecision{{Decision: "a"}})
	_ = recordDecisions(dir, "second", []ReportDecision{{Decision: "b"}})
	_ = recordDecisions(dir, "third", []ReportDecision{{Decision: "c"}})
	if _, err := RemoveDecision(dir, 2); err != nil {
		t.Fatal(err)
	}
	got := ReadDecisions(dir)
	if strings.Contains(got, "second") || !strings.Contains(got, "first") || !strings.Contains(got, "third") {
		t.Errorf("only the middle section must go:\n%s", got)
	}
}

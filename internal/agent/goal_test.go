package agent

import (
	"context"
	"net/http/httptest"
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

func goalRun(t *testing.T, s *scriptServer, text string) (*fixture, TaskResult) {
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
	var got TaskResult
	e.agent.Observer = func(r TaskResult) { got = r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e, got
}

// A goal never asks and is told to decide alone.
func TestAGoalIsToldToDecideAlone(t *testing.T) {
	s := &scriptServer{
		execute: func(int, string) string { return step(true, "true") },
	}
	e, result := goalRun(t, s, "/goal add login")
	if !result.Pass || e.agent.Interactive {
		t.Fatalf("pass=%v interactive=%v", result.Pass, e.agent.Interactive)
	}
	if len(s.analyses) == 0 || !strings.Contains(s.analyses[0], "This is a GOAL") ||
		!strings.Contains(s.analyses[0], "add login") {
		t.Errorf("the model must be told it is a goal:\n%v", s.analyses)
	}
}

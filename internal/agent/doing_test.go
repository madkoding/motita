package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCommandDoingIsTheModelsWordsOrAName(t *testing.T) {
	long := strings.Repeat("palabra ", 30)
	for _, c := range []struct {
		name string
		cmd  Command
		want string
	}{
		{"the model's words", Command{Description: " Reading   the\nfolder's files "}, "Reading the folder's files"},
		{"words too long are cut", Command{Description: long}, truncate(collapse(long), doingWidth)},
		{"no words: a program", Command{Kind: "command", Command: "ls -la"}, "Listing files"},
		{"no words: a kind", Command{Kind: "write_file", Command: "a.go\nx"}, "Writing a file"},
	} {
		if got := c.cmd.doing(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDescribeActionNamesWhatItDoes(t *testing.T) {
	for _, c := range []struct{ kind, command, want string }{
		{"edit_file", "", "Editing a file"},
		{" Spawn_Agent ", "", "Starting a background agent"},
		{"desktop_click", "x: 1", "Using the desktop"},
		{"something_new", "x", "Running an action"},
		{"", "cat README.md", "Reading a file"},
		{"command", "grep -rn foo .", "Searching the code"},
		{"command", "cd web && npm test", "Running the tests"},
		{"command", "cd web; FOO=1 /usr/bin/go build ./...", "Building and checking the project"},
		{"command", "npm install", "Installing dependencies"},
		{"command", "pytest -q", "Running the tests"},
		{"command", "go run .", "Running a command"},
		{"command", "frobnicate --all", "Running a command"},
		{"command", "   ", "Running a command"},
		{"command", "FOO=1", "Running a command"},
	} {
		if got := DescribeAction(c.kind, c.command); got != c.want {
			t.Errorf("%q %q: %q, want %q", c.kind, c.command, got, c.want)
		}
	}
}

func TestSplitDoingReadsTheWordsOfAnAction(t *testing.T) {
	d, rest, ok := SplitDoing(DoingPrefix + "Running the tests\nrunning: go test")
	if !ok || d != "Running the tests" || rest != "running: go test" {
		t.Errorf("compound: %q %q %v", d, rest, ok)
	}
	d, rest, ok = SplitDoing(DoingPrefix + "Reading a skill")
	if !ok || d != "Reading a skill" || rest != "" {
		t.Errorf("alone: %q %q %v", d, rest, ok)
	}
	d, rest, ok = SplitDoing("running: ls")
	if ok || d != "" || rest != "running: ls" {
		t.Errorf("not words: %q %q %v", d, rest, ok)
	}
}

// TestActionsAnnounceThemselvesInWords: the line before the command says what the action is for, and
// one that never runs a command (a read from the library) says it on its own.
func TestActionsAnnounceThemselvesInWords(t *testing.T) {
	e := fileAgent(t)
	var lines []string
	e.agent.Progress = func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	_, err := e.agent.runActions(context.Background(), []Command{
		{Kind: "command", Description: "Saying hello", Command: "echo hi"},
		{Kind: "write_file", Command: "a.txt\nhello"},
		{Kind: "list_skills"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	var doings []string
	for _, l := range lines {
		if d, rest, ok := SplitDoing(l); ok {
			doings = append(doings, d+"|"+strings.SplitN(rest, "\n", 2)[0])
		}
	}
	want := []string{"Saying hello|running: echo hi", "Writing a file|running: write_file a.txt", "Looking at the skills|"}
	if strings.Join(doings, ";") != strings.Join(want, ";") {
		t.Errorf("doings = %q, want %q", doings, want)
	}
}

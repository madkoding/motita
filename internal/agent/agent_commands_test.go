package agent

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/task"
)

// TestAttemptFailedLogsTheProposedCommands: the "attempt failed" record must carry the commands
// the model proposed, not only the validation reason.
//
// Why it matters: the retry loop stops when the counter runs out, and the only way to learn
// whether retrying CONVERGES is to compare one attempt's commands with the next. The validation
// reason alone ("the check failed") is identical for a run that is correcting itself and for one
// that is repeating itself; the commands are what tell them apart. Without them in the log, that
// question can only be answered by guessing, and the loop's whole design rests on it.
func TestAttemptFailedLogsTheProposedCommands(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "attempts.log")
	l, err := logx.New(logx.Options{Path: logPath, Level: logx.Warn, Console: false})
	if err != nil {
		t.Fatalf("could not create the logger: %v", err)
	}

	// Two attempts, both failing: the anchor wants result.txt and neither command makes it.
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{
			{"echo first-try > wrong.txt"},
			{"echo second-try > other.txt"},
		},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	dir := t.TempDir()
	c := config.Default()
	c.LLM.APIKey = "key"
	c.LLM.BaseURL = srv.URL
	c.LLM.MaxAttempts = 1
	c.LLM.BackoffInitial = time.Millisecond
	c.LLM.BackoffMax = 2 * time.Millisecond
	c.LLM.Timeout = 5 * time.Second
	c.Anchor = config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "test -s result.txt"},
		Timeout: 10 * time.Second,
	}
	c.Agent.WorkspaceDir = dir
	c.Agent.MaxRetries = 1
	c.TaskSource.Kind = "file"

	box, err := sandbox.New(sandbox.Options{
		Dir: dir, Limits: sandbox.Limits{MemoryMB: 256, CPUSeconds: 10},
		Timeout: 20 * time.Second, MaxOutputKB: 64, Log: l,
	})
	if err != nil {
		t.Fatalf("could not create the sandbox: %v", err)
	}
	defer box.Close()

	engine, err := llm.New(c.LLM, l)
	if err != nil {
		t.Fatalf("could not create the engine: %v", err)
	}
	source, err := task.NewText("do the test task", "test")
	if err != nil {
		t.Fatal(err)
	}

	ag := New(c, l, engine, box, source)
	// The run is EXPECTED to end in failure: both attempts miss what the anchor checks. That is
	// the only way to produce two "attempt failed" records, so the error is not a test failure.
	if err := ag.Run(context.Background()); err == nil {
		t.Fatal("the run should have failed: neither attempt satisfies the anchor")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("closing the log: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("could not read the log: %v", err)
	}

	// Each failing attempt must leave a record naming the command it proposed.
	var attempts []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["msg"] == "attempt failed" {
			attempts = append(attempts, rec)
		}
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 'attempt failed' records, got %d (log: %s)", len(attempts), raw)
	}
	if got, want := attempts[0]["commands"], "echo first-try > wrong.txt"; got != want {
		t.Errorf("first record's commands = %v, want %q", got, want)
	}
	if got, want := attempts[1]["commands"], "echo second-try > other.txt"; got != want {
		t.Errorf("second record's commands = %v, want %q", got, want)
	}
}

// TestProposedCommands: the line must describe every attempt honestly, including the two shapes
// that carry no command at all.
//
// An attempt with an empty command and an attempt with no commands are both real outcomes, and
// both must be distinguishable in the log from an attempt that proposed something. Rendering them
// as an empty string would make a thrashing run look like it never tried, which is the opposite of
// what this field exists to show.
func TestProposedCommands(t *testing.T) {
	cases := []struct {
		name   string
		action Action
		want   string
	}{
		{
			name:   "one command",
			action: Action{Actions: []Command{{Command: "ls -la"}}},
			want:   "ls -la",
		},
		{
			name:   "several commands, in order",
			action: Action{Actions: []Command{{Command: "echo a"}, {Command: "echo b"}}},
			want:   "echo a | echo b",
		},
		{
			name:   "surrounding whitespace is not part of the command",
			action: Action{Actions: []Command{{Command: "  printf ok  "}}},
			want:   "printf ok",
		},
		{
			name:   "an entry with no command still counts",
			action: Action{Actions: []Command{{Command: "echo a"}, {Description: "just words"}}},
			want:   "echo a | (empty)",
		},
		{
			name:   "nothing proposed",
			action: Action{},
			want:   "(no commands proposed)",
		},
		{
			name:   "a very long command is bounded",
			action: Action{Actions: []Command{{Command: strings.Repeat("x", 900)}}},
			want:   strings.Repeat("x", 500) + "...",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := proposedCommands(tc.action); got != tc.want {
				t.Errorf("proposedCommands() = %q, want %q", got, tc.want)
			}
		})
	}
}

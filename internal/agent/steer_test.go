package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// TestAMessageSteersTheRunningTask: a message handed to the running task reaches the next
// round's prompt and the trail, and it is not taken before or after the task runs.
func TestAMessageSteersTheRunningTask(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	rounds := 0
	started := make(chan struct{}, 1)
	steered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"task\",\"understandable\":true,\"summary\":\"s\"}"}}]}`)
		case strings.Contains(text, "## ACTION PLAN"):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[]}"}}]}`)
		case strings.Contains(text, "## PREVIOUS ROUNDS"):
			mu.Lock()
			prompts = append(prompts, text)
			rounds++
			n := rounds
			mu.Unlock()
			if n == 1 {
				select {
				case started <- struct{}{}:
				default:
				}
				<-steered
				fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"reasoning\":\"r\",\"actions\":[{\"command\":\"true\"}],\"done\":false}"}}]}`)
				return
			}
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"reasoning\":\"r\",\"actions\":[],\"done\":true,`+
				`\"final_action\":{\"command\":\"true\"}}"}}]}`)
		default:
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
		}
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.cfg.Agent.CriteriaAudit = false
	e.agent.cfg.Agent.MaxRetries = 3
	var lines []string
	e.agent.Progress = func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	if e.agent.Steer("too early") {
		t.Fatal("nothing is running yet: the message must not be taken")
	}

	done := make(chan error, 1)
	go func() { done <- e.agent.Run(context.Background()) }()
	<-started
	if !e.agent.Steer("  also rename the flag  ") || e.agent.Steer("   ") {
		t.Fatal("the running task takes a message, and not a blank one")
	}
	close(steered)
	<-done
	if e.agent.Steer("too late") {
		t.Fatal("the task ended: the message must not be taken")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(prompts) < 2 || !strings.Contains(prompts[1], "WHILE YOU WORKED") || !strings.Contains(prompts[1], "also rename the flag") {
		t.Fatalf("the second round must read the message, got %d rounds; trail %q", len(prompts), lines)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "you: also rename the flag") {
		t.Errorf("the trail must show the message: %q", lines)
	}
}

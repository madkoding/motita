package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/sandbox"
)

// scriptedServer answers each phase with its replies in order, repeating the last one.
type scriptedServer struct {
	mu                     sync.Mutex
	analysis, plan, action []string
	actionCalls            int
}

func (s *scriptedServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		next := func(list *[]string) string {
			reply := (*list)[0]
			if len(*list) > 1 {
				*list = (*list)[1:]
			}
			return reply
		}
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = next(&s.analysis)
		case strings.Contains(text, "## ACTION PLAN"):
			content = next(&s.plan)
		case strings.Contains(text, "## ACTION"):
			s.actionCalls++
			content = next(&s.action)
		default:
			content = "{}"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}
}

const validAction = `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`

// TestASubtaskAnsweredAsChatDoesNotPassTheParent: only the anchor declares PASS, so a subtask the
// model waves off as chat cannot make the parent report success without any validation.
func TestASubtaskAnsweredAsChatDoesNotPassTheParent(t *testing.T) {
	s := &scriptedServer{
		analysis: []string{`{"understandable":true,"summary":"x","needs_subtasks":true}`, `{"kind":"chat"}`},
		plan:     []string{`{"plan":[],"subtasks":["SUBONE"]}`},
		action:   []string{validAction},
	}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "false", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.SubtaskDepth = 1
	})
	var results []TaskResult
	e.agent.Observer = func(r TaskResult) { results = append(results, r) }

	if err := e.agent.Run(context.Background()); err == nil {
		t.Error("a parent whose subtask was never validated must fail")
	}
	last := results[len(results)-1]
	if last.Pass {
		t.Errorf("the parent passed with no anchor run: %+v", last)
	}
	if !strings.Contains(last.Reason, "only 0 of 1") {
		t.Errorf("reason = %q", last.Reason)
	}
}

// TestAMalformedActionReplyIsRetried: one reply without JSON is the model's mistake to correct, and
// the retries the configuration grants are used for it instead of ending the task.
func TestAMalformedActionReplyIsRetried(t *testing.T) {
	s := &scriptedServer{
		analysis: []string{`{"understandable":true,"summary":"x","needs_subtasks":false}`},
		plan:     []string{`{"plan":[]}`},
		action:   []string{"I will just run true.", validAction},
	}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("the second, valid reply should pass: %v", err)
	}
	if !result.Pass || result.Attempts != 2 || s.actionCalls != 2 {
		t.Errorf("pass=%v attempts=%d action calls=%d", result.Pass, result.Attempts, s.actionCalls)
	}
}

// TestTheAnchorIsNotLimitedByTheSandboxOutputCap: a check that prints more than the agent sandbox
// keeps must still be matched on its full output, because the anchor runs outside that sandbox.
func TestTheAnchorIsNotLimitedByTheSandboxOutputCap(t *testing.T) {
	s := &scriptedServer{
		analysis: []string{`{"understandable":true,"summary":"x","needs_subtasks":false}`},
		plan:     []string{`{"plan":[]}`},
		action:   []string{validAction},
	}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh",
		Args:         []string{"-c", `head -c 200000 /dev/zero | tr '\0' x; echo; echo READY`},
		ExpectOutput: "READY",
		Timeout:      10 * time.Second,
	}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("the check prints READY, so it must pass: %v (%+v)", err, result)
	}
}

// TestTheAnchorSharesTheSandboxOnlyInChrootMode: in a chroot the workspace resolves only inside the
// jail, so that is the one mode where validation runs in the agent's sandbox.
func TestTheAnchorSharesTheSandboxOnlyInChrootMode(t *testing.T) {
	box := &sandbox.Sandbox{}
	a := &Agent{sandbox: box}
	if a.anchorSandbox() != nil {
		t.Error("outside chroot the anchor must run on its own")
	}
	a.cfg.Sandbox.Kind = "chroot"
	if a.anchorSandbox() != box {
		t.Error("in chroot mode the anchor must validate in the sandbox")
	}
}

// TestAPlanOfOnlyBlankSubtasksRunsAsASingleTask: blank subtasks are noise, so the task falls back
// to the execute/anchor cycle instead of failing as "0 of 0 subtasks".
func TestAPlanOfOnlyBlankSubtasksRunsAsASingleTask(t *testing.T) {
	s := &scriptedServer{
		analysis: []string{`{"understandable":true,"summary":"x","needs_subtasks":true}`},
		plan:     []string{`{"plan":[],"subtasks":[" ",""]}`},
		action:   []string{validAction},
	}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.Agent.SubtaskDepth = 1
	})
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("it should run as a single task and pass: %v", err)
	}
	if result.Subtasks != 0 || s.actionCalls != 1 || result.Validation == nil {
		t.Errorf("subtasks=%d action calls=%d validation=%v", result.Subtasks, s.actionCalls, result.Validation)
	}
}

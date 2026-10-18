package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/task"
)

// TestFollowedPlanRoundTrips: the plan a go-ahead carries out is read back whole, without the
// person's words, and an ordinary task is not mistaken for one.
func TestFollowedPlanRoundTrips(t *testing.T) {
	task := FollowPlanTask("do it", "1. read\n2. write\n")
	plan, ok := FollowedPlan(task)
	if !ok || plan != "1. read\n2. write" {
		t.Fatalf("plan = %q, ok = %v", plan, ok)
	}
	if _, ok := FollowedPlan("fix the build"); ok {
		t.Fatal("an ordinary task is not a plan-following one")
	}
}

// TestAPlanFollowingTurnSkipsPlanningAndNeverAsks: the plan phase is not called, and an analysis
// that would ask is carried on as work, since the plan is the person's own decision.
func TestAPlanFollowingTurnSkipsPlanningAndNeverAsks(t *testing.T) {
	planned := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"ask\",\"understandable\":false,`+
				`\"question\":\"Which one?\",\"assumption\":\"the first\",\"decision\":true}"}}]}`)
		case strings.Contains(text, "## ACTION PLAN"):
			planned = true
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[]}"}}]}`)
		default:
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"reasoning\":\"r\",\"actions\":[],\"done\":true,`+
				`\"final_action\":{\"command\":\"true\"}}"}}]}`)
		}
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true
	e.agent.cfg.Agent.CriteriaAudit = false
	src, err := task.NewText(FollowPlanTask("ok", "1. run true"), "test")
	if err != nil {
		t.Fatal(err)
	}
	e.agent.source = src
	var lines []string
	e.agent.Progress = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	_ = e.agent.Run(context.Background())

	if result == nil || result.NeedsInput {
		t.Fatalf("a plan-following turn never asks: %+v", result)
	}
	if planned {
		t.Error("the approved plan must not be planned again")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "carrying out the approved plan") {
		t.Errorf("the turn must say it is carrying out the plan: %q", lines)
	}
}

// TestAnAnalysisThatPlansSkipsThePlanPhase: with steps in the analysis the plan phase is not
// called; without them, or with subtasks asked for, or with the setting off, it is.
func TestAnAnalysisThatPlansSkipsThePlanPhase(t *testing.T) {
	for _, tc := range []struct {
		name     string
		analysis string
		quick    bool
		planned  bool
	}{
		{"steps in the analysis", `\"plan\":[\"read\",\" \",\"write\"]`, true, false},
		{"no steps", `\"plan\":[]`, true, true},
		{"subtasks asked for", `\"plan\":[\"a\"],\"needs_subtasks\":true`, true, true},
		{"setting off", `\"plan\":[\"a\"]`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planned := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				text := string(body)
				switch {
				case strings.Contains(text, "## ANALYSIS OF THE TASK"):
					fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"task\",\"understandable\":true,`+
						`\"summary\":\"s\",\"success_criteria\":[\"c\"],`+tc.analysis+`}"}}]}`)
				case strings.Contains(text, "## ACTION PLAN"):
					planned = true
					fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[]}"}}]}`)
				default:
					if strings.Contains(text, "## PREVIOUS ROUNDS") && strings.Contains(text, "1. read") == tc.planned {
						t.Errorf("the execute phase must see the analysis' steps exactly when the plan phase was skipped")
					}
					fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"reasoning\":\"r\",\"actions\":[],\"done\":true,`+
						`\"final_action\":{\"command\":\"true\"}}"}}]}`)
				}
			}))
			defer srv.Close()
			e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
			e.agent.cfg.Agent.CriteriaAudit = false
			e.agent.cfg.Agent.QuickPlan = tc.quick
			_ = e.agent.Run(context.Background())
			if planned != tc.planned {
				t.Errorf("plan phase called = %v, want %v", planned, tc.planned)
			}
		})
	}
}

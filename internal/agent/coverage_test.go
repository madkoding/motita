package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/task"
)

// execxRequest builds a shell request for the test sandbox.
func execxRequest(command string) execx.Request {
	return execx.Request{Command: "/bin/sh", Args: []string{"-c", command}, Timeout: 20 * time.Second}
}

// textTask wraps a text as a task (for the test sources).
func textTask(text string) task.Task {
	return task.Task{Description: text, Origin: "test"}
}

// phaseServer answers the three phases with JSON controlled by the test.
type phaseServer struct {
	analysis      string
	plan          string
	action        string
	analysisError bool
	// analysisUnclear makes the analysis report that it could not read the request, with a
	// question and an assumption — the shape the agent must turn into a question rather than a
	// refusal.
	analysisUnclear bool
	// analysisUnclearNoAssumption is the same but with nothing to fall back on, so there is no
	// reading to proceed with.
	analysisUnclearNoAssumption bool
	// analysisUnclearNoSummary is the same as analysisUnclear but with the summary left empty:
	// there is an assumption to act on, and nothing else to describe the work.
	analysisUnclearNoSummary bool
	// analysisAssumptionOnly proposes a reading but names no question.
	analysisAssumptionOnly bool
	planError              bool
	actionError            bool
	httpError              bool
	calls                  *int
}

func (s phaseServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.calls != nil {
			*s.calls++
		}
		text := fakeRequestText(r)

		if s.httpError {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"simulated failure"}}`)
			return
		}

		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = s.analysisContent()
		case strings.Contains(text, "## ACTION PLAN"):
			if s.planError {
				content = "not this either"
			} else {
				content = s.plan
			}
		default:
			if s.actionError {
				content = "nor this"
			} else {
				content = s.action
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}
}

// analysisContent is the analysis-phase answer the server was configured to give.
func (s phaseServer) analysisContent() string {
	switch {
	case s.analysisError:
		return "this is not JSON"
	case s.analysisUnclear:
		return `{"understandable": false, "summary": "", "success_criteria": [],
					"risks": ["two readings are possible"], "needs_subtasks": false,
					"question": "Do you want me to check the disk or the logs?",
					"assumption": "I assume you want the state of the disk"}`
	case s.analysisAssumptionOnly:
		return `{"understandable": false, "summary": "", "success_criteria": [],
					"risks": ["ambiguo"], "needs_subtasks": false,
					"question": "", "assumption": "I assume you want to check the disk"}`
	case s.analysisUnclearNoSummary:
		return `{"understandable": false, "summary": "", "success_criteria": [],
					"risks": ["ambiguo"], "needs_subtasks": false,
					"question": "what do you want?", "assumption": "I assume the state of the disk"}`
	case s.analysisUnclearNoAssumption:
		return `{"understandable": false, "summary": "", "success_criteria": [],
					"risks": ["nothing to go on"], "needs_subtasks": false,
					"question": "What do you want me to do?", "assumption": ""}`
	default:
		return s.analysis
	}
}

// TestAnalysisPhaseWithInvalidResponse: an analysis that is not valid JSON is OUR problem, not
// the user's, so the agent asks a question instead of reporting a parse error at them.
//
// The parse error goes to the LOG — it is what a developer needs — while the user gets a question
// they can answer. Telling a user "the LLM's analysis was not valid JSON" asks them to fix
// something they did not do.
func TestAnalysisPhaseWithInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(phaseServer{analysisError: true}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	// A malformed analysis produces a QUESTION, so the run does not fail: it is waiting for the
	// user, which is a different outcome and must not exit non-zero.
	_ = e.agent.Run(context.Background())
	if result == nil {
		t.Fatal("no result was reported")
	}
	// It asks rather than reporting a parse failure, and it is not a failure at all.
	if !result.NeedsInput {
		t.Errorf("an unparseable analysis must become a question, got %+v", result)
	}
	if result.Question == "" {
		t.Error("the user must be given something to answer")
	}
	if strings.Contains(result.Question, "JSON") {
		t.Errorf("the question must not talk about JSON: %q", result.Question)
	}
}

// TestPlanPhaseWithInvalidResponse: an unreadable plan does not abort: it carries
// on with an empty plan (the plan is guidance, not binding).
func TestPlanPhaseWithInvalidResponse(t *testing.T) {
	server := phaseServer{
		analysis:  `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		planError: true,
		action:    `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("an unreadable plan must not stop the work: %v", err)
	}
	if result == nil || !result.Pass {
		t.Errorf("result = %+v", result)
	}
}

// TestActionPhaseWithInvalidResponse: with no action there is nothing to run; the
// task fails with the reason.
func TestActionPhaseWithInvalidResponse(t *testing.T) {
	server := phaseServer{
		analysis:    `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:        `{"plan":[]}`,
		actionError: true,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err == nil {
		t.Error("it should fail")
	}
	if result.Pass {
		t.Error("it cannot pass without an action")
	}
	if !strings.Contains(result.Reason, "action") {
		t.Errorf("reason = %q", result.Reason)
	}
}

// TestActionPhaseWithoutActions: the JSON is valid, carries no actions, and reports the task
// finished (an absent "done" means finished). That is how a model reports work that is
// already complete, and it goes to the anchor like any other claim: the anchor, not the
// number of actions, decides.
func TestActionPhaseWithoutActions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		anchor string
		pass   bool
	}{
		{"the anchor accepts the claim", "exit 0", true},
		{"the anchor refuses the claim", "exit 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := phaseServer{
				analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
				plan:     `{"plan":[]}`,
				action:   `{"reasoning":"it is already done","actions":[]}`,
			}
			srv := httptest.NewServer(server.handler(t))
			defer srv.Close()

			e := mount(t, srv, config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", tc.anchor},
				Timeout: 5 * time.Second}, nil)
			var result *TaskResult
			e.agent.Observer = func(r TaskResult) { result = &r }

			e.agent.Run(context.Background())
			if result.Pass != tc.pass {
				t.Errorf("pass = %v, want %v: an empty claim of done is judged by the anchor (%s)",
					result.Pass, tc.pass, result.Reason)
			}
		})
	}
}

// TestNetworkFailureInAnalysis: an LLM failure in the first phase is recorded.
func TestNetworkFailureInAnalysis(t *testing.T) {
	srv := httptest.NewServer(phaseServer{httpError: true}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	e.agent.Run(context.Background())
	if result == nil || result.Pass {
		t.Fatalf("result = %+v", result)
	}
}

// TestPlanPhaseWithNoPlan: the plan phase's error path leaves an empty plan and
// the flow continues (the plan is guidance).
func TestPlanPhaseWithNoPlan(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":false}`,
		plan:     `{}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Pass {
		t.Errorf("an empty plan must not stop the work: %s", result.Reason)
	}
}

// TestSplitReachesTheLimit: when the analysis keeps asking for subtasks but the
// configured depth is exhausted, the task continues as a single one instead of
// recursing forever.
func TestSplitReachesTheLimit(t *testing.T) {
	server := phaseServer{
		analysis: `{"understandable":true,"summary":"x","needs_subtasks":true}`,
		plan:     `{"plan":[],"subtasks":["another subtask"]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		// Depth 0: the very first split is refused and the task runs as one.
		c.Agent.SubtaskDepth = 0
	})

	start := time.Now()
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("it took too long: runaway recursion?")
	}
}

// --- Test helpers ------------------------------------------------------------

var _ = logx.Global
var _ = execxRequest

// TestPlanPhaseWithNoAnalysisDetails: an analysis carrying neither summary nor
// criteria nor risks must still produce a usable plan request.
func TestPlanPhaseWithNoAnalysisDetails(t *testing.T) {
	server := phaseServer{
		// No summary, no success_criteria, no risks.
		analysis: `{"understandable":true,"needs_subtasks":false}`,
		plan:     `{"plan":[{"step":1,"action":"do it","command":"true"}]}`,
		action:   `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`,
	}
	srv := httptest.NewServer(server.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !result.Pass {
		t.Errorf("a bare analysis must not stop the work: %s", result.Reason)
	}
}

// TestActionPhaseReportsATransportFailure: when the LLM cannot be reached while
// asking for the action, the task must fail with that reason (and escalate).
func TestActionPhaseReportsATransportFailure(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if strings.Contains(text, "## ACTION") {
			// The analysis and the plan answer normally; the execute phase fails
			// every time, so the retries end up reporting the phase.
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"engine down"}}`)
			return
		}
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"x","needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[]}`
		case strings.Contains(text, "## ACTION"):
			content = `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`
		default:
			content = "{}"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.LLM.MaxAttempts = 1
	})
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	err := e.agent.Run(context.Background())
	if err == nil {
		t.Fatal("a failing engine must make the task fail")
	}
	if result == nil || result.Pass {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Reason, "could not obtain the action") {
		t.Errorf("the reason must name the failing phase: %q", result.Reason)
	}
}

// TestPlanPhaseReportsATransportFailure: a failure while planning leaves an empty
// plan and the flow continues (the plan is guidance, not a contract).
func TestPlanPhaseReportsATransportFailure(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if strings.Contains(text, "## ACTION PLAN") {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"planner down"}}`)
			return
		}
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"x","needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[]}`
		default:
			content = `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, func(c *config.Config) {
		c.LLM.MaxAttempts = 1
	})
	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("a failed plan must not abort the task: %v", err)
	}
	if !result.Pass {
		t.Errorf("the task should still pass: %s", result.Reason)
	}
}

// TestPlanWithSuccessCriteriaAndRisks: the criteria and risks collected during
// the analysis must reach the plan prompt (they are what makes the plan concrete).
func TestPlanWithSuccessCriteriaAndRisks(t *testing.T) {
	var planPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"the summary","success_criteria":["criterion-one"],` +
				`"risks":["risk-one"],"needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			planPrompt = text
			content = `{"plan":[]}`
		default:
			content = `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":""}}`
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"the summary", "criterion-one", "risk-one"} {
		if !strings.Contains(planPrompt, part) {
			t.Errorf("the plan prompt must carry %q", part)
		}
	}
}

// TestExecutePromptCarriesTheAttemptNumber: the phase that proposes the action
// must know which attempt it is on, because that changes the wording of the
// correction handed to the model.
func TestExecutePromptCarriesTheAttemptNumber(t *testing.T) {
	var (
		executePrompt string
		calls         int
		mu            sync.Mutex
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		mu.Lock()
		defer mu.Unlock()
		var content string
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			content = `{"understandable":true,"summary":"x","needs_subtasks":false}`
		case strings.Contains(text, "## ACTION PLAN"):
			content = `{"plan":[]}`
		case strings.Contains(text, "## CRITERIA AUDIT"):
			content = `{"criteria":[]}`
		case strings.Contains(text, "## FINAL ANSWER"):
			content = `{"summary":"test"}`
		default:
			calls++
			executePrompt = text
			// The command is a no-op that always satisfies the anchor: what is
			// checked here is the prompt, not the action.
			content = `{"reasoning":"try","actions":[{"command":"true"}],"final_action":{"command":""}}`
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": content}}},
		})
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(executePrompt, "Round: 1") {
		t.Errorf("the execute prompt must carry the round number")
	}
}

// TestAnUnclearRequestBecomesAQuestion: the agent must ASK when it cannot interpret the request,
// not refuse and throw the turn away.
//
// A user mistypes, abbreviates, and leaves out what they consider obvious. A request the model can
// only half interpret is incomplete information, not a failure — and there is a user on the other
// end who can complete it in three words. Refusing makes them write the whole thing again.
func TestAnUnclearRequestBecomesAQuestion(t *testing.T) {
	srv := httptest.NewServer(phaseServer{analysisUnclear: true}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	_ = e.agent.Run(context.Background())

	if result == nil {
		t.Fatal("no result was reported")
	}
	if !result.NeedsInput {
		t.Fatalf("an unclear request must ask, got %+v", result)
	}
	if result.Question == "" {
		t.Error("the user must be given something to answer")
	}
	if result.Assumption == "" {
		t.Error("the assumption must travel with the question, so the user can confirm in one word")
	}
	// The reason is NOT a failure message: the interface prints it as a question.
	if strings.HasPrefix(result.Reason, "the task was declared not understandable") {
		t.Errorf("the old refusal wording is back: %q", result.Reason)
	}
}

// TestWithNoUserTheAgentProceedsOnItsAssumption: a batch run has nobody to ask, so asking would
// stall forever. The agent acts on the reading it proposed, which is what an engineer does when
// the ticket is thin — and both are better than refusing.
func TestWithNoUserTheAgentProceedsOnItsAssumption(t *testing.T) {
	srv := httptest.NewServer(phaseServer{
		analysisUnclear: true,
		plan:            `{"plan":[],"expected_result":"st"}`,
		action:          `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":"true"}}`,
	}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = false // a piped task, a cron job: no user at the other end

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	_ = e.agent.Run(context.Background())

	if result == nil {
		t.Fatal("no result was reported")
	}
	// It did NOT stop to ask: it carried on through the loop with the assumption as its reading.
	if result.NeedsInput {
		t.Errorf("with nobody to ask the agent must proceed, not wait: %+v", result)
	}
	// And it says which reading it took. A result produced under an assumption must carry that
	// assumption: otherwise a reasonable reading looks like a wrong answer to whoever reads it
	// later, with nothing to explain the difference.
	if result.Assumption == "" {
		t.Errorf("a run that proceeded on an assumption must say so: %+v", result)
	}
}

// TestAnUnclearRequestWithNoAssumptionStillAsks: if the model cannot even propose a reading,
// there is nothing to proceed on, so the question is returned rather than a guess.
func TestAnUnclearRequestWithNoAssumptionStillAsks(t *testing.T) {
	srv := httptest.NewServer(phaseServer{analysisUnclearNoAssumption: true}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = false

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	_ = e.agent.Run(context.Background())

	if result == nil {
		t.Fatal("no result was reported")
	}
	if !result.NeedsInput {
		t.Errorf("with no assumption to proceed on, the agent must ask: %+v", result)
	}
}

// TestAnAssumptionWithNoQuestionStillAsks: the model may propose a reading without naming a
// question — it knows what it would do, and forgets to ask. Asking is still right: the user can
// confirm or correct that reading in one word, which is far cheaper than acting on a guess in
// silence.
func TestAnAssumptionWithNoQuestionStillAsks(t *testing.T) {
	srv := httptest.NewServer(phaseServer{
		analysisAssumptionOnly: true,
		plan:                   `{"plan":[],"expected_result":"st"}`,
		action:                 `{"reasoning":"r","actions":[{"command":"true"}],"final_action":{"command":"true"}}`,
	}.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	_ = e.agent.Run(context.Background())

	if result == nil {
		t.Fatal("no result was reported")
	}
	if !result.NeedsInput {
		t.Fatalf("an assumption with no question must still ask: %+v", result)
	}
	if result.Question == "" {
		t.Error("a question must be derived so the user has something to answer")
	}
	if result.Assumption == "" {
		t.Error("the assumption must travel with the question")
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/evals"
	"github.com/madkoding/motita/internal/logx"
)

// evalRunner is a model check runner that needs no model: it passes the tasks in pass, holds on
// gate when set, and fails every task with err when set.
type evalRunner struct {
	pass map[string]bool
	err  error
	gate chan struct{}
	cfg  config.Config
}

func (r *evalRunner) Run(ctx context.Context, c evals.Case, _ string) (evals.Outcome, error) {
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return evals.Outcome{}, ctx.Err()
		}
	}
	if r.err != nil {
		return evals.Outcome{}, r.err
	}
	return evals.Outcome{Pass: r.pass[c.ID], Steps: 1, Tokens: 10, CostUSD: 0.01, CostKnown: true}, nil
}

// evalServer is a gateway with a key for openai and the runner the test gives.
func evalServer(t *testing.T, r *evalRunner) *Server {
	t.Helper()
	path := llmEnv(t, openAIConfig)
	if err := config.SetCredential(path, "MOTITA_LLM_API_KEY", "sk-openai"); err != nil {
		t.Fatal(err)
	}
	oldRunner, oldSupported := newEvalRunner, evalsSupported
	t.Cleanup(func() { newEvalRunner, evalsSupported = oldRunner, oldSupported })
	newEvalRunner = func(cfg config.Config, _ *logx.Logger) evals.Runner {
		r.cfg = cfg
		return r
	}
	evalsSupported = func() bool { return true }
	svc := &modelsService{}
	svc.cfg = config.Default()
	svc.cfg.LLM.Provider, svc.cfg.LLM.Model = "openai", "gpt-5-mini"
	return newTestServer(t, svc, func(o *Options) {
		o.ConfigPath = path
		o.EvalsFile = filepath.Join(t.TempDir(), "evals.json")
	})
}

func viewOfEvals(t *testing.T, srv *Server) evalView {
	t.Helper()
	w := llmCall(t, srv, http.MethodGet, "/v1/evals", "")
	var v evalView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %s: %v", w.Code, w.Body.String(), err)
	}
	return v
}

func runEval(t *testing.T, srv *Server) {
	t.Helper()
	w := llmCall(t, srv, http.MethodPost, "/v1/evals/run", `{"confirm":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", w.Code, w.Body.String())
	}
	srv.evalJob.wg.Wait()
}

func allPass() map[string]bool {
	m := map[string]bool{}
	for _, c := range evals.Cases() {
		m[c.ID] = true
	}
	return m
}

func TestEvalsRunCompareAndGoStale(t *testing.T) {
	r := &evalRunner{pass: allPass()}
	srv := evalServer(t, r)
	v := viewOfEvals(t, srv)
	if !v.Supported || len(v.Cases) != 5 || v.Provider != "openai" {
		t.Fatalf("before any run: %+v", v)
	}
	if v.Stale != "never" || v.Last != nil {
		t.Fatalf("no run yet: %+v", v)
	}

	runEval(t, srv)
	v = viewOfEvals(t, srv)
	if v.Last == nil || v.Last.Summary.Passed != 5 || v.Verdict.Status != evals.StatusFirst || v.Stale != "" {
		t.Fatalf("after the first run: %+v", v)
	}
	if r.cfg.LLM.Model != "gpt-5-mini" || r.cfg.LLM.APIKey != "sk-openai" {
		t.Errorf("the check runs on the configured model with its key: %+v", r.cfg.LLM)
	}

	r.pass["fix-typo"] = false
	runEval(t, srv)
	v = viewOfEvals(t, srv)
	if v.Verdict.Status != evals.StatusWorse || len(v.Verdict.Regressed) != 1 || len(v.History) != 2 {
		t.Fatalf("a task that stopped passing: %+v", v.Verdict)
	}

	// Another model for the coding work makes the last run stale, and runs on that model.
	if w := llmCall(t, srv, http.MethodPut, "/v1/llm/tasks",
		`{"tasks":{"code":{"provider":"openai","model":"gpt-5"}}}`); w.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", w.Code, w.Body.String())
	}
	if v = viewOfEvals(t, srv); v.Stale != "model" || v.Model != "gpt-5" {
		t.Errorf("a new coding model: stale %q model %q", v.Stale, v.Model)
	}
	runEval(t, srv)
	if r.cfg.LLM.Model != "gpt-5" {
		t.Errorf("the model under test = %q", r.cfg.LLM.Model)
	}

	// Different instructions make it stale too.
	svc := srv.opts.Service.(*modelsService)
	svc.cfg.Prompts.Analyze.System += " more"
	if v = viewOfEvals(t, srv); v.Stale != "prompt" {
		t.Errorf("changed instructions: stale %q", v.Stale)
	}
}

func TestEvalsNeedAConfirmation(t *testing.T) {
	srv := evalServer(t, &evalRunner{pass: allPass()})
	for _, body := range []string{`{}`, `{"confirm":false}`} {
		if w := llmCall(t, srv, http.MethodPost, "/v1/evals/run", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/evals/run", `not json`); w.Code != http.StatusBadRequest {
		t.Errorf("bad body: %d", w.Code)
	}
	if viewOfEvals(t, srv).Last != nil || srv.evalJob.get().Running {
		t.Error("nothing should have run")
	}
}

func TestEvalsRefusals(t *testing.T) {
	srv := evalServer(t, &evalRunner{pass: allPass()})
	evalsSupported = func() bool { return false }
	if w := llmCall(t, srv, http.MethodPost, "/v1/evals/run", `{"confirm":true}`); w.Code != http.StatusConflict {
		t.Errorf("unsupported: %d", w.Code)
	}
	evalsSupported = func() bool { return true }
	// A coding model whose provider has no key cannot be checked.
	srv.opts.Service.(*modelsService).cfg.Models.Code = "gemini/gemini-2.5-pro"
	if err := config.SetTaskModels(srv.opts.ConfigPath, map[string]config.ModelChoice{
		"code": {Provider: "gemini", Model: "gemini-2.5-pro"}}); err != nil {
		t.Fatal(err)
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/evals/run", `{"confirm":true}`); w.Code != http.StatusConflict {
		t.Errorf("no key: %d %s", w.Code, w.Body.String())
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/evals/stop", ``); w.Code != http.StatusConflict {
		t.Errorf("stop with nothing running: %d", w.Code)
	}
}

func TestEvalsOneAtATimeAndStoppable(t *testing.T) {
	r := &evalRunner{pass: allPass(), gate: make(chan struct{})}
	srv := evalServer(t, r)
	if w := llmCall(t, srv, http.MethodPost, "/v1/evals/run", `{"confirm":true}`); w.Code != http.StatusAccepted {
		t.Fatalf("start: %d", w.Code)
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/evals/run", `{"confirm":true}`); w.Code != http.StatusConflict {
		t.Errorf("a second check while one runs: %d", w.Code)
	}
	if p := viewOfEvals(t, srv).Progress; !p.Running || p.Total != 5 {
		t.Errorf("progress = %+v", p)
	}
	if w := llmCall(t, srv, http.MethodPost, "/v1/evals/stop", ``); w.Code != http.StatusAccepted {
		t.Errorf("stop: %d", w.Code)
	}
	srv.evalJob.wg.Wait()
	v := viewOfEvals(t, srv)
	if v.Progress.Running || !v.Progress.Stopped || v.Progress.Error != "" || v.Last != nil {
		t.Errorf("a stopped check leaves no run and no error: %+v", v.Progress)
	}
}

func TestEvalsSayWhyThereIsNoResult(t *testing.T) {
	srv := evalServer(t, &evalRunner{err: errors.New("down")})
	runEval(t, srv)
	if p := viewOfEvals(t, srv).Progress; p.Error == "" || p.Running {
		t.Errorf("progress = %+v", p)
	}
}

func TestEvalsReportAResultThatCouldNotBeSaved(t *testing.T) {
	srv := evalServer(t, &evalRunner{pass: allPass()})
	srv.evalStore = evals.OpenStore(filepath.Join(srv.opts.ConfigPath, "x", "evals.json"))
	runEval(t, srv)
	v := viewOfEvals(t, srv)
	if v.Progress.Error == "" || v.Last == nil {
		t.Errorf("the run is kept in memory and the failure is told: %+v", v.Progress)
	}
}

func TestEvalsHistoryKeepsTheLastFive(t *testing.T) {
	srv := evalServer(t, &evalRunner{pass: allPass()})
	for i := 0; i < 7; i++ {
		if err := srv.evalStore.Add(evals.Run{ID: string(rune('a' + i)), CasesHash: evals.CasesHash()}); err != nil {
			t.Fatal(err)
		}
	}
	v := viewOfEvals(t, srv)
	if len(v.History) != 5 || v.History[0].ID != "g" {
		t.Errorf("history = %+v", v.History)
	}
}

func TestEvalsRunWithTheSessionsOwnProviderSettings(t *testing.T) {
	r := &evalRunner{pass: allPass()}
	srv := evalServer(t, r)
	cfg := srv.evalConfig("anthropic", "claude-opus-5-5")
	if cfg.LLM.Provider != "anthropic" || cfg.LLM.Model != "claude-opus-5-5" {
		t.Errorf("llm = %+v", cfg.LLM)
	}
}

func TestEvalRunnerIsTheRealAgent(t *testing.T) {
	r := newEvalRunnerFor(config.Default(), nil)
	if _, ok := r.(evals.AgentRunner); !ok {
		t.Errorf("runner = %T", r)
	}
}

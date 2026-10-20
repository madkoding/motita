package gateway

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/evals"
	"github.com/madkoding/motita/internal/logx"
)

// THE MODEL CHECK: a few fixed tasks run on the model that does the coding, in a temporary folder of
// their own, and compared with the run before. It spends the person's quota, so it only ever runs
// when asked for and confirmed; this file never starts one on its own.

// The seams: a test swaps the runner for one that needs no model.
var (
	newEvalRunner  = newEvalRunnerFor
	evalsSupported = evals.Supported
)

// newEvalRunnerFor runs each task through the real agent.
func newEvalRunnerFor(cfg config.Config, log *logx.Logger) evals.Runner {
	return evals.AgentRunner{Cfg: cfg, Log: log}
}

// evalProgress is how far the check running now got.
type evalProgress struct {
	Running bool `json:"running"`
	Done    int  `json:"done"`
	Total   int  `json:"total"`
	// Next is the task being run.
	Next string `json:"next,omitempty"`
	// Error is why the last check ended without a result; empty when it ended fine or none ran.
	Error string `json:"error,omitempty"`
	// Stopped says the person stopped the last check.
	Stopped bool `json:"stopped,omitempty"`
}

// evalTracker holds the progress of the one check that may run at a time.
type evalTracker struct {
	mu     sync.Mutex
	p      evalProgress
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (t *evalTracker) get() evalProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.p
}

func (t *evalTracker) update(fn func(p *evalProgress)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&t.p)
}

// start marks a check as running, unless one already is.
func (t *evalTracker) start(total int, cancel context.CancelFunc) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.p.Running {
		return false
	}
	t.p = evalProgress{Running: true, Total: total}
	t.cancel = cancel
	return true
}

// stop asks the running check to end; false when none runs.
func (t *evalTracker) stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.p.Running {
		return false
	}
	t.p.Stopped = true
	t.cancel()
	return true
}

// evalCase is a task as the page lists it; the page words it by id.
type evalCase struct {
	ID string `json:"id"`
}

// evalHistory is one earlier run, shortened.
type evalHistory struct {
	ID       string        `json:"id"`
	At       string        `json:"at"`
	Provider string        `json:"provider"`
	Model    string        `json:"model"`
	Summary  evals.Summary `json:"summary"`
}

// evalView is what GET /v1/evals answers.
type evalView struct {
	// Supported is false on a computer that cannot run the checks (they are shell commands).
	Supported bool       `json:"supported"`
	Cases     []evalCase `json:"cases"`
	// Provider and Model are what a check would run on now: the model that does the coding.
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Stale says why the last run does not answer "is it still fine?": never, model, prompt, cases;
	// empty when it does.
	Stale    string         `json:"stale"`
	Progress evalProgress   `json:"progress"`
	Last     *evals.Run     `json:"last"`
	Verdict  *evals.Verdict `json:"verdict"`
	History  []evalHistory  `json:"history"`
}

// evalRoutes registers the model check endpoints.
func (s *Server) evalRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/evals", plain(s.handleEvals))
	mux.Handle("POST /v1/evals/run", plain(s.handleEvalRun))
	mux.Handle("POST /v1/evals/stop", plain(s.handleEvalStop))
}

// evalModel is the provider and model a check runs on: the coding model, else the default.
func (s *Server) evalModel() (provider, model string) {
	if c := s.configuredModels().For(config.TaskCode); !c.Empty() {
		return c.Provider, c.Model
	}
	l := s.configuredLLM()
	return l.Provider, l.Model
}

// evalPromptHash fingerprints the instructions the agent runs with.
func (s *Server) evalPromptHash() string {
	return evals.PromptsHash(s.opts.Service.Config().Prompts)
}

func (s *Server) evalViewOf() evalView {
	provider, model := s.evalModel()
	v := evalView{
		Supported: evalsSupported(), Provider: provider, Model: model,
		Progress: s.evalJob.get(), Cases: []evalCase{}, History: []evalHistory{},
	}
	for _, c := range evals.Cases() {
		v.Cases = append(v.Cases, evalCase{ID: c.ID})
	}
	runs := s.evalStore.Runs()
	if n := len(runs); n > 0 {
		v.Last = &runs[n-1]
		var prev *evals.Run
		if n > 1 {
			prev = &runs[n-2]
		}
		verdict := evals.Compare(prev, runs[n-1])
		v.Verdict = &verdict
		for i := n - 1; i >= 0 && i >= n-5; i-- {
			r := runs[i]
			v.History = append(v.History, evalHistory{
				ID: r.ID, At: r.At.Format("2006-01-02T15:04:05Z"), Provider: r.Provider, Model: r.Model, Summary: r.Summary,
			})
		}
	}
	v.Stale = evals.Stale(v.Last, provider, model, s.evalPromptHash())
	return v
}

func (s *Server) handleEvals(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.evalViewOf())
}

// evalConfig is the configuration a check runs with: the session's, with the model under test and
// the key and endpoint it runs with.
func (s *Server) evalConfig(provider, model string) config.Config {
	cfg := s.opts.Service.Config()
	configured := s.configuredLLM()
	l := config.Default().LLM
	if strings.EqualFold(configured.Provider, provider) {
		l = configured
	}
	l.Provider, l.Model = provider, model
	l.BaseURL, l.APIKey = effectiveBase(configured, provider), effectiveKey(configured, provider)
	cfg.LLM = l
	return cfg
}

// handleEvalRun starts a check in the background. It needs {"confirm":true}: the check spends the
// person's quota, so a request that does not say it was confirmed starts nothing.
func (s *Server) handleEvalRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if !body.Confirm {
		writeRefusal(w, http.StatusBadRequest, "confirm_required",
			"the check uses part of your quota: confirm it to run")
		return
	}
	if !evalsSupported() {
		writeRefusal(w, http.StatusConflict, "unsupported", "this computer cannot run the check: it needs a shell")
		return
	}
	provider, model := s.evalModel()
	if config.KeySource(s.opts.ConfigPath, s.configuredLLM(), provider) == config.KeyMissing {
		writeRefusal(w, http.StatusConflict, "no_key", "connect "+provider+" first: it has no key")
		return
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	if !s.evalJob.start(len(evals.Cases()), cancel) {
		cancel()
		writeRefusal(w, http.StatusConflict, "running", "a check is already running")
		return
	}
	runner := newEvalRunner(s.evalConfig(provider, model), s.opts.Log)
	op := evals.Options{
		Provider: provider, Model: model, PromptHash: s.evalPromptHash(),
		Progress: func(done, total int, next string) {
			s.evalJob.update(func(p *evalProgress) { p.Done, p.Total, p.Next = done, total, next })
		},
	}
	s.evalJob.wg.Add(1)
	go func() {
		defer s.evalJob.wg.Done()
		defer cancel()
		s.finishEval(evals.Execute(ctx, runner, evals.Cases(), op))
	}()
	writeJSON(w, http.StatusAccepted, s.evalJob.get())
}

// finishEval keeps a run and clears the progress, or records why there is no run.
func (s *Server) finishEval(run evals.Run, err error) {
	if err == nil {
		err = s.evalStore.Add(run)
	}
	s.evalJob.update(func(p *evalProgress) {
		stopped := p.Stopped
		*p = evalProgress{Stopped: stopped}
		if err != nil && !stopped {
			p.Error = err.Error()
		}
	})
}

func (s *Server) handleEvalStop(w http.ResponseWriter, _ *http.Request) {
	if !s.evalJob.stop() {
		writeRefusal(w, http.StatusConflict, "idle", "no check is running")
		return
	}
	writeJSON(w, http.StatusAccepted, s.evalJob.get())
}

package tui

import (
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/onboard"
)

// THE MODEL OF EACH KIND OF WORK, for this session.
//
// The configuration's models block may give a kind of work (plan, code, review, research, summary)
// a model of its own; every kind it leaves empty runs on the session's model. The engines built
// for those entries live on this runner, so they belong to ONE session: two sessions never share
// one, and a background agent is handed an engine of the session that started it.

// dropEngines forgets every engine built from the settings, so the next call builds them again
// from the settings as they are now. Called with cfgMu held.
func (r *AppRunner) dropEngines() {
	r.Engine = nil
	r.taskEngines = nil
}

// SetTaskModels replaces the models block this session runs with. The engines built from the old
// one are dropped; a turn already running keeps the ones it was handed.
func (r *AppRunner) SetTaskModels(m config.Models) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	r.Cfg.Models = m
	r.taskEngines = nil
}

// endpointOf is the base URL and key a provider runs with in this session: the ones it was last
// used with, else the endpoint saved from the settings (or its default) and the key its own
// variable holds. Called with cfgMu held.
func (r *AppRunner) endpointOf(provider string) llmEndpoint {
	next := strings.ToLower(provider)
	if strings.EqualFold(next, r.Cfg.LLM.Provider) {
		return llmEndpoint{baseURL: r.Cfg.LLM.BaseURL, apiKey: r.Cfg.LLM.APIKey}
	}
	ep, ok := r.endpoints[next]
	if !ok {
		ep = llmEndpoint{baseURL: config.StoredBaseURL(next), apiKey: config.ProviderKeyFromEnv(next)}
		if ep.baseURL == "" {
			ep.baseURL = onboard.DefaultBaseURL(next)
		}
	}
	// openai and codex are the same account: the key carries over between them.
	if ep.apiKey == "" && sameOpenAIAccount(strings.ToLower(r.Cfg.LLM.Provider), next) {
		ep.apiKey = r.Cfg.LLM.APIKey
	}
	return ep
}

// engineFor is the configuration and engine a kind of work runs with: the session's own when the
// models block leaves the kind to the default (or names the model the session already runs), the
// assigned model otherwise. An assigned model that cannot be built - its provider lost its key -
// falls back to the session's model, and the log says so: the work still runs.
func (r *AppRunner) engineFor(kind string) (config.Config, *llm.Client, error) {
	r.cfgMu.Lock()
	choice := r.Cfg.Models.For(kind)
	if choice.Empty() || (strings.EqualFold(choice.Provider, r.Cfg.LLM.Provider) && choice.Model == r.Cfg.LLM.Model) {
		r.cfgMu.Unlock()
		return r.engine()
	}
	cfg := r.Cfg
	ep := r.endpointOf(choice.Provider)
	cfg.LLM.Provider, cfg.LLM.Model = choice.Provider, choice.Model
	cfg.LLM.BaseURL, cfg.LLM.APIKey = ep.baseURL, ep.apiKey
	if e := r.taskEngines[kind]; e != nil {
		r.cfgMu.Unlock()
		return cfg, e, nil
	}
	e, err := llm.New(cfg.LLM, r.Log)
	if err == nil {
		if r.taskEngines == nil {
			r.taskEngines = map[string]*llm.Client{}
		}
		r.taskEngines[kind] = e
	}
	r.cfgMu.Unlock()
	if err != nil {
		if r.Log != nil {
			r.Log.Warn("the model assigned to this kind of work cannot run; using the session's model",
				"kind", kind, "model", choice.String(), "error", err)
		}
		return r.engine()
	}
	return cfg, e, nil
}

// roleEngine is the engine a background agent of a role runs on, nil for the session's own.
func (r *AppRunner) roleEngine(role string) *llm.Client {
	_, e, err := r.engineFor(role)
	if err != nil {
		return nil
	}
	return e
}

// summaryEngine is the engine a conversation compacts with: the summary model when one is
// assigned and can run, else the turn's own engine.
func (r *AppRunner) summaryEngine(turn *llm.Client) *llm.Client {
	r.cfgMu.Lock()
	assigned := !r.Cfg.Models.For(config.TaskSummary).Empty()
	r.cfgMu.Unlock()
	if !assigned {
		return turn
	}
	if _, e, err := r.engineFor(config.TaskSummary); err == nil && e != nil {
		return e
	}
	return turn
}

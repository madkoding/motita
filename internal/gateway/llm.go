package gateway

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/onboard"
)

// THE LLM PROVIDERS, connected from the browser's settings.
//
// These are PROCESS-WIDE, like /v1/ui: a key is not a property of one conversation, and the
// default provider and model are what the next conversation starts with. A key is written to the
// credentials file beside the configuration (see config.SetCredential) and NEVER sent back: the
// list says where a provider's key comes from, not what it is. Every route sits behind the token.

// llmModelView is one model of the catalogue.
type llmModelView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
}

// llmProviderView is one provider as the settings draw it.
type llmProviderView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Access     string `json:"access"`
	ConsoleURL string `json:"console_url,omitempty"`
	// Key is where the key comes from: env, stored, config, login, not_needed or missing (see
	// config.KeySource). KeyVariable names the variable when it is env, so the page can say which.
	Key         string `json:"key"`
	KeyVariable string `json:"key_variable,omitempty"`
	// Login is the command that logs the provider's own tool in (claude-code). SupportsLogin says
	// the setup (motita config) can store a login for it instead of a key.
	Login         string `json:"login,omitempty"`
	SupportsLogin bool   `json:"supports_login,omitempty"`
	// AskBaseURL says the endpoint can be changed (OpenAI-compatible hosts, a self-hosted Ollama).
	AskBaseURL     bool           `json:"ask_base_url,omitempty"`
	BaseURL        string         `json:"base_url,omitempty"`
	DefaultBaseURL string         `json:"default_base_url,omitempty"`
	Models         []llmModelView `json:"models"`
	FetchModels    bool           `json:"fetch_models,omitempty"`
	Default        bool           `json:"default"`
}

// llmView is what GET /v1/llm answers.
type llmView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Writable is false when the gateway runs on the built-in defaults, with no file to save in.
	Writable  bool              `json:"writable"`
	Providers []llmProviderView `json:"providers"`
}

// providerRefresher is a service that can take a provider's new key and endpoint on its next turn.
type providerRefresher interface {
	RefreshProvider(provider, apiKey, baseURL string)
}

// listProviderModels asks a provider for its models, as a variable so a test needs no network.
var listProviderModels = func(ctx context.Context, l config.LLM) ([]string, error) {
	c, err := llm.New(l, nil)
	if err != nil {
		return nil, err
	}
	return c.ListModels(ctx)
}

// The writes, as variables so a test can make each one fail.
var (
	setCredential = config.SetCredential
	setLLMDefault = config.SetLLMDefault
)

// genericKey is the variable that belongs to whichever provider the configuration names.
const genericKey = "MOTITA_LLM_API_KEY"

// testTimeout bounds one connection test: a host that does not answer is the answer.
const testTimeout = 20 * time.Second

// llmRoutes registers the provider endpoints.
func (s *Server) llmRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/llm", plain(s.handleLLM))
	mux.Handle("PUT /v1/llm/default", plain(s.handleLLMDefault))
	mux.Handle("PUT /v1/llm/providers/{id}", plain(s.handleLLMSave))
	mux.Handle("DELETE /v1/llm/providers/{id}", plain(s.handleLLMForget))
	mux.Handle("POST /v1/llm/providers/{id}/test", plain(s.handleLLMTest))
}

// configuredLLM is the llm block the next conversation starts from: the file as it is now, or the
// first conversation's when there is no file (or it no longer loads).
func (s *Server) configuredLLM() config.LLM {
	if s.opts.ConfigPath != "" {
		if cfg, err := config.LoadWithoutKey(s.opts.ConfigPath); err == nil {
			return cfg.LLM
		}
	}
	return s.opts.Service.Config().LLM
}

// effectiveBase is the endpoint a provider runs with.
func effectiveBase(configured config.LLM, id string) string {
	if strings.EqualFold(configured.Provider, id) && configured.BaseURL != "" {
		return configured.BaseURL
	}
	if b := config.StoredBaseURL(id); b != "" {
		return b
	}
	return onboard.DefaultBaseURL(id)
}

// effectiveKey is the key a provider runs with.
func effectiveKey(configured config.LLM, id string) string {
	if strings.EqualFold(configured.Provider, id) {
		return configured.APIKey
	}
	return config.ProviderKeyFromEnv(id)
}

// canChangeBase says whether a provider's endpoint is the user's to choose.
func canChangeBase(p onboard.Provider) bool { return p.AskBaseURL || p.ID == "ollama" }

// llmViewOf builds the list.
func (s *Server) llmViewOf() llmView {
	cfg := s.configuredLLM()
	v := llmView{Provider: cfg.Provider, Model: cfg.Model, Writable: s.opts.ConfigPath != ""}
	for _, p := range onboard.Providers() {
		v.Providers = append(v.Providers, s.providerView(cfg, p))
	}
	return v
}

// providerView is one provider of the list.
func (s *Server) providerView(cfg config.LLM, p onboard.Provider) llmProviderView {
	pv := llmProviderView{
		ID: p.ID, Name: p.Name, Access: p.Access, ConsoleURL: p.ConsoleURL,
		Key:   config.KeySource(s.opts.ConfigPath, cfg, p.ID),
		Login: p.Login, SupportsLogin: p.SupportsDirectAuth,
		AskBaseURL: canChangeBase(p), BaseURL: publicURL(effectiveBase(cfg, p.ID)),
		DefaultBaseURL: p.DefaultBaseURL, FetchModels: p.FetchModels,
		Default: strings.EqualFold(cfg.Provider, p.ID),
		Models:  make([]llmModelView, 0, len(p.Models)),
	}
	if pv.Key == config.KeyEnv {
		// The generic variable is read only for the default provider; the OpenAI pair's other one
		// is OPENAI_API_KEY.
		pv.KeyVariable = config.ProviderKeyVariable(p.ID)
		if pv.KeyVariable == genericKey && (!pv.Default || os.Getenv(genericKey) == "") {
			pv.KeyVariable = "OPENAI_API_KEY"
		}
	}
	for _, m := range p.Models {
		pv.Models = append(pv.Models, llmModelView{ID: m.ID, Label: m.Label, Note: m.Note})
	}
	return pv
}

// handleLLM answers the providers, where each one's key comes from, and the default.
func (s *Server) handleLLM(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.llmViewOf())
}

// lookupProvider resolves the {id} of the path, answering 404 for one the client does not speak.
func lookupProvider(w http.ResponseWriter, r *http.Request) (onboard.Provider, bool) {
	p, ok := onboard.Lookup(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such provider: "+r.PathValue("id"))
	}
	return p, ok
}

// needFile answers 409 when there is no configuration file to write, and reports whether to go on.
func (s *Server) needFile(w http.ResponseWriter) bool {
	if s.opts.ConfigPath == "" {
		writeError(w, http.StatusConflict, "this gateway runs on the built-in defaults and has no configuration file "+
			"to save in: run the setup (motita config) or start it with -config")
		return false
	}
	return true
}

// writeRefusal is an error a client can tell apart by its code.
func writeRefusal(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// checkBaseURL is a usable endpoint, or the reason it is not.
func checkBaseURL(raw string) (string, string) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "the address must start with http:// or https:// and name a host"
	}
	return raw, ""
}

// handleLLMSave stores a provider's key and/or endpoint. A field left out is left as it is; an
// empty base_url goes back to the provider's default.
func (s *Server) handleLLMSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		APIKey  *string `json:"api_key"`
		BaseURL *string `json:"base_url"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	p, ok := lookupProvider(w, r)
	if !ok || !s.needFile(w) {
		return
	}
	cfg := s.configuredLLM()
	base := effectiveBase(cfg, p.ID)
	newBase := ""
	if body.BaseURL != nil {
		if !canChangeBase(p) {
			writeError(w, http.StatusBadRequest, p.Name+" has one endpoint: its address cannot be changed")
			return
		}
		base = p.DefaultBaseURL
		if strings.TrimSpace(*body.BaseURL) != "" {
			var why string
			if newBase, why = checkBaseURL(*body.BaseURL); why != "" {
				writeError(w, http.StatusBadRequest, why)
				return
			}
			base = newBase
		}
	}
	key := ""
	if body.APIKey != nil {
		key = strings.TrimSpace(*body.APIKey)
		if why := checkKey(p, key); why != "" {
			writeError(w, http.StatusBadRequest, why)
			return
		}
		if config.KeySource(s.opts.ConfigPath, cfg, p.ID) == config.KeyEnv {
			writeRefusal(w, http.StatusConflict, "env_key", "the key comes from an environment variable, which wins over "+
				"one saved here: unset it on the gateway's machine to manage the key from the settings")
			return
		}
	}
	hasKey := key != "" || effectiveKey(cfg, p.ID) != ""
	if hasKey && config.IsClearTextToRemote(base) {
		writeRefusal(w, http.StatusBadRequest, "cleartext", "plain http to a host outside your network would send the "+
			"key in the clear: use https")
		return
	}
	if err := s.saveProvider(cfg, p, body.APIKey != nil, key, body.BaseURL != nil, newBase); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.refreshSessions(p.ID)
	writeJSON(w, http.StatusOK, s.providerView(s.configuredLLM(), p))
}

// checkKey says why a key cannot be saved, or "" when it can.
func checkKey(p onboard.Provider, key string) string {
	switch {
	case !config.ProviderNeedsKey(p.ID):
		return p.Name + " takes no key: it signs in with `" + p.Login + "`"
	case key == "":
		return "the key is empty: to remove it, disconnect the provider"
	case len(key) > 4096 || strings.IndexFunc(key, unicode.IsControl) >= 0 || strings.ContainsRune(key, ' '):
		return "that does not look like a key: paste it again without spaces or line breaks"
	default:
		return ""
	}
}

// saveProvider writes what changed: the key, the endpoint, and the configuration's own endpoint
// when the provider is the default one.
func (s *Server) saveProvider(cfg config.LLM, p onboard.Provider, setKey bool, key string, setBase bool, base string) error {
	path := s.opts.ConfigPath
	if setKey {
		if err := setCredential(path, config.ProviderStoreVariable(p.ID), key); err != nil {
			return err
		}
		if err := s.dropSharedKey(cfg, p.ID); err != nil {
			return err
		}
	}
	if !setBase {
		return nil
	}
	if err := setCredential(path, config.ProviderBaseURLVariable(p.ID), base); err != nil {
		return err
	}
	if strings.EqualFold(cfg.Provider, p.ID) {
		if base == "" {
			base = p.DefaultBaseURL
		}
		return setLLMDefault(path, cfg.Provider, cfg.Model, base)
	}
	return nil
}

// dropSharedKey removes the generic MOTITA_LLM_API_KEY from the credentials file when the
// OpenAI pair is configured and a key of theirs was saved or removed: the loader reads the generic
// variable first for them, so the key the user just changed would otherwise never be the one used.
func (s *Server) dropSharedKey(cfg config.LLM, id string) error {
	openAI := func(p string) bool { return config.ProviderStoreVariable(p) == "OPENAI_API_KEY" }
	if !openAI(id) || !openAI(cfg.Provider) {
		return nil
	}
	return setCredential(s.opts.ConfigPath, genericKey, "")
}

// refreshSessions hands every live conversation the provider's key and endpoint as they are now.
func (s *Server) refreshSessions(id string) {
	cfg := s.configuredLLM()
	key, base := effectiveKey(cfg, id), effectiveBase(cfg, id)
	for _, c := range s.snapshot() {
		if rf, ok := c.svc.(providerRefresher); ok {
			rf.RefreshProvider(id, key, base)
		}
	}
}

// handleLLMForget removes a provider's stored key and endpoint.
func (s *Server) handleLLMForget(w http.ResponseWriter, r *http.Request) {
	p, ok := lookupProvider(w, r)
	if !ok || !s.needFile(w) {
		return
	}
	cfg := s.configuredLLM()
	setKey := config.ProviderNeedsKey(p.ID)
	if err := s.saveProvider(cfg, p, setKey, "", canChangeBase(p), ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.refreshSessions(p.ID)
	writeJSON(w, http.StatusOK, s.providerView(s.configuredLLM(), p))
}

// testResult is what a connection test answers: the models the provider listed, or why not.
type testResult struct {
	OK     bool     `json:"ok"`
	Models []string `json:"models,omitempty"`
	Error  string   `json:"error,omitempty"`
}

// handleLLMTest asks the provider for its models with the key and endpoint given, or the saved
// ones. The answer is 200 either way: the test ran, and its result is the body.
func (s *Server) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	p, ok := lookupProvider(w, r)
	if !ok {
		return
	}
	if !config.ProviderNeedsKey(p.ID) {
		// The claude CLI brings its own login and lists no catalogue: its models are the ones it takes.
		ids := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			ids = append(ids, m.ID)
		}
		writeJSON(w, http.StatusOK, testResult{OK: true, Models: ids})
		return
	}
	cfg := s.configuredLLM()
	l := config.Default().LLM
	if strings.EqualFold(cfg.Provider, p.ID) {
		l = cfg
	}
	l.Provider, l.BaseURL, l.APIKey = p.ID, effectiveBase(cfg, p.ID), effectiveKey(cfg, p.ID)
	if strings.TrimSpace(body.BaseURL) != "" {
		base, why := checkBaseURL(body.BaseURL)
		if why != "" {
			writeError(w, http.StatusBadRequest, why)
			return
		}
		// A saved key goes only to the address it was saved for: a test of another address
		// carries the key typed with it.
		if base != strings.TrimRight(l.BaseURL, "/") && strings.TrimSpace(body.APIKey) == "" && l.APIKey != "" {
			writeRefusal(w, http.StatusBadRequest, "key_for_new_address",
				"type the key again to try it on a new address: the saved one is only sent where it was saved for")
			return
		}
		l.BaseURL = base
	}
	if k := strings.TrimSpace(body.APIKey); k != "" {
		l.APIKey = k
	}
	if l.APIKey != "" && config.IsClearTextToRemote(l.BaseURL) {
		writeRefusal(w, http.StatusBadRequest, "cleartext", "plain http to a host outside your network would send the "+
			"key in the clear: use https")
		return
	}
	l.Timeout, l.MaxAttempts = testTimeout, 1
	ctx, cancel := context.WithTimeout(r.Context(), testTimeout)
	defer cancel()
	models, err := listProviderModels(ctx, l)
	if err != nil {
		writeJSON(w, http.StatusOK, testResult{Error: err.Error()})
		return
	}
	sort.Strings(models)
	writeJSON(w, http.StatusOK, testResult{OK: true, Models: models})
}

// handleLLMDefault makes a provider and model the ones new conversations start with, and saves
// them in the configuration. The conversations already open keep their own.
func (s *Server) handleLLMDefault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	p, ok := onboard.Lookup(strings.TrimSpace(body.Provider))
	if !ok {
		writeError(w, http.StatusBadRequest, "no such provider: "+body.Provider)
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" || len(model) > 200 || strings.IndexFunc(model, unicode.IsControl) >= 0 {
		writeError(w, http.StatusBadRequest, "name a model")
		return
	}
	if !s.needFile(w) {
		return
	}
	cfg := s.configuredLLM()
	same := strings.EqualFold(cfg.Provider, p.ID)
	if config.KeySource(s.opts.ConfigPath, cfg, p.ID) == config.KeyMissing {
		writeRefusal(w, http.StatusConflict, "no_key", "connect "+p.Name+" first: it has no key")
		return
	}
	if !same && config.YAMLAPIKey(s.opts.ConfigPath) != "" {
		writeRefusal(w, http.StatusConflict, "config_key", "the configuration file holds llm.api_key, which would be "+
			"sent to the new provider: remove it from the file (the settings can store the key instead)")
		return
	}
	if err := s.switchDefault(cfg, p, model); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.llmViewOf())
}

// switchDefault writes the new default. Moving away from a provider whose key sits under the
// generic variable first moves that key to the provider's own one: the generic variable belongs to
// whichever provider the file names, so it would otherwise be sent to the new one.
func (s *Server) switchDefault(cfg config.LLM, p onboard.Provider, model string) error {
	path := s.opts.ConfigPath
	base := effectiveBase(cfg, p.ID)
	if !strings.EqualFold(cfg.Provider, p.ID) {
		creds := config.StoredCredentials(path)
		if generic := creds[genericKey]; generic != "" {
			own := config.ProviderStoreVariable(cfg.Provider)
			if own != "" && creds[own] == "" {
				if err := setCredential(path, own, generic); err != nil {
					return err
				}
			}
			if err := setCredential(path, genericKey, ""); err != nil {
				return err
			}
		}
	}
	return setLLMDefault(path, p.ID, model, base)
}

// startingLLM points a new conversation at the default the file names now, with the key and
// endpoint as they are now: the conversation was built from the configuration the gateway started
// with, and the settings may have changed both since.
func (s *Server) startingLLM(svc Service) {
	if s.opts.ConfigPath == "" {
		return
	}
	cfg := s.configuredLLM()
	svc.SetLLM(cfg.Provider, cfg.Model)
	if rf, ok := svc.(providerRefresher); ok {
		rf.RefreshProvider(cfg.Provider, cfg.APIKey, effectiveBase(cfg, cfg.Provider))
	}
}

package gateway

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/inference"
	"github.com/madkoding/motita/internal/onboard"
)

// THE REGISTRY OF INFERENCE SERVERS, as Settings → Inference lists and changes it.
//
// A server is an address (llama.cpp, LM Studio, Ollama, any OpenAI-compatible endpoint) with an
// optional key and model. The list is process-wide like the providers. The key goes to the
// credentials file under inference.KeyVariable and is never returned: the page sees only whether
// there is one and its last four characters. The registry adds no protocol; the probe asks the
// server for its models through the provider that speaks its kind.

// inferenceView is one server as the page sees it.
type inferenceView struct {
	inference.Server
	// Provider is the LLM provider that speaks to the server.
	Provider string `json:"provider"`
	HasKey   bool   `json:"has_key"`
	// InUse says the server's address is the one its provider runs with now.
	InUse bool `json:"in_use"`
	// Managed marks Motita's own local model: listed so it shows beside the others, but started,
	// stopped and removed from Settings → Providers, so it has no key, no edit and no probe here.
	Managed bool `json:"managed,omitempty"`
	// KeyHint is the last four characters of a key of 16 or more.
	KeyHint string `json:"key_hint,omitempty"`
}

// inferenceList is what the list endpoints answer.
type inferenceList struct {
	// CanSave is false when there is no configuration file to keep the list beside.
	CanSave bool             `json:"can_save"`
	Max     int              `json:"max"`
	Kinds   []inference.Kind `json:"kinds"`
	Servers []inferenceView  `json:"servers"`
}

// probeResult is what a probe answers; the answer is 200 either way, the body says how it went.
type probeResult struct {
	OK        bool     `json:"ok"`
	LatencyMS int64    `json:"latency_ms"`
	Models    []string `json:"models,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// updateInference is the write of an edit, as a variable so a test can make it fail.
var updateInference = (*inference.Store).Update

// probeNow is the clock of a probe's latency; a variable so a test can fix it.
var probeNow = time.Now

func (s *Server) inferenceRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/inference-servers", plain(s.handleInferenceList))
	mux.Handle("POST /v1/inference-servers", plain(s.handleInferenceAdd))
	mux.Handle("PUT /v1/inference-servers/{id}", plain(s.handleInferenceEdit))
	mux.Handle("DELETE /v1/inference-servers/{id}", plain(s.handleInferenceRemove))
	mux.Handle("POST /v1/inference-servers/{id}/probe", plain(s.handleInferenceProbe))
	mux.Handle("POST /v1/inference-servers/{id}/use", plain(s.handleInferenceUse))
}

func (s *Server) inferenceStore() *inference.Store {
	return inference.NewStore(inference.PathBeside(s.opts.ConfigPath))
}

// inferenceSnapshot is the list as the page sees it.
func (s *Server) inferenceSnapshot() (inferenceList, error) {
	list, err := s.inferenceStore().List()
	if err != nil {
		return inferenceList{}, err
	}
	creds := config.StoredCredentials(s.opts.ConfigPath)
	cfg := s.configuredLLM()
	out := inferenceList{
		CanSave: s.opts.ConfigPath != "",
		Max:     inference.MaxServers,
		Kinds:   inference.Kinds,
		Servers: make([]inferenceView, 0, len(list)+1),
	}
	// Motita's own local model is an entry of the list while it has an address: it comes from
	// Settings → Providers, so it is shown and never stored here.
	if base := config.StoredBaseURL(config.ProviderLocal); base != "" {
		out.Servers = append(out.Servers, inferenceView{
			Server:   inference.Server{ID: managedLocalID, Name: "Motita local model", Kind: "llamacpp", BaseURL: base},
			Provider: config.ProviderLocal,
			InUse:    strings.EqualFold(cfg.Provider, config.ProviderLocal),
			Managed:  true,
		})
	}
	for _, v := range list {
		view := inferenceViewOf(v, creds[inference.KeyVariable(v.ID)])
		view.InUse = effectiveBase(cfg, view.Provider) == v.BaseURL
		out.Servers = append(out.Servers, view)
	}
	return out, nil
}

// managedLocalID is the id the managed local model has in the list; it is never a stored id.
const managedLocalID = "motita-local"

func inferenceViewOf(v inference.Server, key string) inferenceView {
	k, _ := inference.KindOf(v.Kind)
	return inferenceView{Server: v, Provider: k.Provider, HasKey: key != "", KeyHint: keyHint(key)}
}

func (s *Server) writeInferenceSnapshot(w http.ResponseWriter, status int) {
	snap, err := s.inferenceSnapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, snap)
}

func (s *Server) handleInferenceList(w http.ResponseWriter, _ *http.Request) {
	s.writeInferenceSnapshot(w, http.StatusOK)
}

// inferenceBody is what an add or an edit sends. A field left out stays as it was on an edit.
type inferenceBody struct {
	Name    *string `json:"name"`
	Kind    *string `json:"kind"`
	BaseURL *string `json:"base_url"`
	Model   *string `json:"model"`
	APIKey  *string `json:"api_key"`
}

// merged is the server the body asks for, on top of base: the fields it names replace the ones
// of base. The address is checked, and the kind's default stands in for an empty one.
func (b inferenceBody) merged(base inference.Server) (inference.Server, string) {
	if b.Name != nil {
		base.Name = strings.TrimSpace(*b.Name)
	}
	if b.Kind != nil {
		base.Kind = *b.Kind
	}
	if b.BaseURL != nil {
		base.BaseURL = *b.BaseURL
	}
	if b.Model != nil {
		base.Model = strings.TrimSpace(*b.Model)
	}
	if why := inference.Check(base); why != "" {
		return base, why
	}
	if strings.TrimSpace(base.BaseURL) == "" {
		k, _ := inference.KindOf(base.Kind)
		if k.DefaultBaseURL == "" {
			return base, "type the server's address: " + k.Name + " has no default one"
		}
		base.BaseURL = k.DefaultBaseURL
	}
	addr, why := checkBaseURL(base.BaseURL)
	base.BaseURL = addr
	return base, why
}

// checkInferenceKey is why a key cannot be kept for the server, or "".
func checkInferenceKey(key, base string) (string, string) {
	key = strings.TrimSpace(key)
	switch {
	case key == "":
		return "", ""
	case len(key) > 4096 || strings.IndexFunc(key, unicode.IsControl) >= 0 || strings.ContainsRune(key, ' '):
		return "", "that does not look like a key: paste it again without spaces or line breaks"
	case config.IsClearTextToRemote(base):
		return "", "plain http to a host outside your network would send the key in the clear: use https"
	default:
		return key, ""
	}
}

func (s *Server) handleInferenceAdd(w http.ResponseWriter, r *http.Request) {
	var body inferenceBody
	if !s.decodeBody(w, r, &body) || !s.needFile(w) {
		return
	}
	v, why := body.merged(inference.Server{})
	if why != "" {
		writeError(w, http.StatusBadRequest, why)
		return
	}
	key := ""
	if body.APIKey != nil {
		if key, why = checkInferenceKey(*body.APIKey, v.BaseURL); why != "" {
			writeError(w, http.StatusBadRequest, why)
			return
		}
	}
	st := s.inferenceStore()
	s.inference.Lock()
	defer s.inference.Unlock()
	added, err := st.Add(v)
	if err != nil {
		writeInferenceError(w, err)
		return
	}
	if key != "" {
		if err := setCredential(s.opts.ConfigPath, inference.KeyVariable(added.ID), key); err != nil {
			_ = st.Remove(added.ID)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.writeInferenceSnapshot(w, http.StatusCreated)
}

// writeInferenceError answers a store's refusal with the status it deserves.
func writeInferenceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, inference.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, inference.ErrDuplicate), errors.Is(err, inference.ErrFull):
		writeRefusal(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) handleInferenceEdit(w http.ResponseWriter, r *http.Request) {
	var body inferenceBody
	if !s.decodeBody(w, r, &body) || !s.needFile(w) {
		return
	}
	id := r.PathValue("id")
	if !inference.ValidID(id) {
		writeError(w, http.StatusNotFound, inference.ErrNotFound.Error())
		return
	}
	st := s.inferenceStore()
	s.inference.Lock()
	defer s.inference.Unlock()
	cur, err := st.Get(id)
	if err != nil {
		writeInferenceError(w, err)
		return
	}
	v, why := body.merged(cur)
	if why != "" {
		writeError(w, http.StatusBadRequest, why)
		return
	}
	creds := config.StoredCredentials(s.opts.ConfigPath)
	key, set := creds[inference.KeyVariable(id)], false
	if body.APIKey != nil {
		set = true
		if key, why = checkInferenceKey(*body.APIKey, v.BaseURL); why != "" {
			writeError(w, http.StatusBadRequest, why)
			return
		}
	}
	// The key kept is only sent where it was kept for: moving a server to a plain-http host
	// outside the network would send it in the clear.
	if key != "" && config.IsClearTextToRemote(v.BaseURL) {
		writeError(w, http.StatusBadRequest, "plain http to a host outside your network would send the key in the "+
			"clear: use https, or remove the key")
		return
	}
	if _, err := updateInference(st, id, v); err != nil {
		writeInferenceError(w, err)
		return
	}
	if set {
		if err := setCredential(s.opts.ConfigPath, inference.KeyVariable(id), key); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.writeInferenceSnapshot(w, http.StatusOK)
}

func (s *Server) handleInferenceRemove(w http.ResponseWriter, r *http.Request) {
	if !s.needFile(w) {
		return
	}
	id := r.PathValue("id")
	if !inference.ValidID(id) {
		writeError(w, http.StatusNotFound, inference.ErrNotFound.Error())
		return
	}
	s.inference.Lock()
	defer s.inference.Unlock()
	if err := s.inferenceStore().Remove(id); err != nil {
		writeInferenceError(w, err)
		return
	}
	if err := setCredential(s.opts.ConfigPath, inference.KeyVariable(id), ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeInferenceSnapshot(w, http.StatusOK)
}

// handleInferenceProbe asks the server for its models: the answer says whether it is up, how long
// it took and what it serves.
func (s *Server) handleInferenceProbe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !inference.ValidID(id) {
		writeError(w, http.StatusNotFound, inference.ErrNotFound.Error())
		return
	}
	v, err := s.inferenceStore().Get(id)
	if err != nil {
		writeInferenceError(w, err)
		return
	}
	k, _ := inference.KindOf(v.Kind)
	l := config.Default().LLM
	l.Provider, l.BaseURL, l.Timeout, l.MaxAttempts = k.Provider, v.BaseURL, testTimeout, 1
	l.APIKey = config.StoredCredentials(s.opts.ConfigPath)[inference.KeyVariable(id)]
	if l.APIKey == "" {
		// Most servers on a private network take no key, the client wants one to send.
		l.APIKey = "none"
	}
	ctx, cancel := context.WithTimeout(r.Context(), testTimeout)
	defer cancel()
	start := probeNow()
	models, err := listProviderModels(ctx, l)
	took := probeNow().Sub(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, probeResult{LatencyMS: took, Error: err.Error()})
		return
	}
	sort.Strings(models)
	writeJSON(w, http.StatusOK, probeResult{OK: true, LatencyMS: took, Models: models})
}

// handleInferenceUse makes a server the endpoint of the provider that speaks its kind: its address
// and key are saved where that provider keeps them, and the open conversations get them at once.
// Nothing else moves: the default model and the other providers stay as they were, so using a
// server is as undoable as saving a provider.
func (s *Server) handleInferenceUse(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !inference.ValidID(id) {
		writeError(w, http.StatusNotFound, inference.ErrNotFound.Error())
		return
	}
	if !s.needFile(w) {
		return
	}
	v, err := s.inferenceStore().Get(id)
	if err != nil {
		writeInferenceError(w, err)
		return
	}
	k, _ := inference.KindOf(v.Kind)
	p, _ := onboard.Lookup(k.Provider)
	key := config.StoredCredentials(s.opts.ConfigPath)[inference.KeyVariable(id)]
	if key == "" && k.Provider == config.ProviderCustom {
		// The custom provider always sends a token, and a server that takes none ignores it.
		key = "none"
	}
	cfg := s.configuredLLM()
	if err := s.saveProvider(cfg, p, true, key, true, v.BaseURL); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if v.Model != "" && k.Provider == config.ProviderCustom {
		custom := s.configuredCustom()
		custom.Model = v.Model
		if err := setCustom(s.opts.ConfigPath, custom); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, c := range s.snapshot() {
			if cs, ok := c.svc.(customModelSetter); ok {
				cs.SetCustomModel(custom)
			}
		}
	}
	s.refreshSessions(p.ID)
	s.writeInferenceSnapshot(w, http.StatusOK)
}

package gateway

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/localmodel"
	"github.com/madkoding/motita/internal/onboard"
)

// MOTITA'S OWN LOCAL MODEL, from Settings → Providers.
//
// PROCESS-WIDE like the providers: one GPU, one server. Every step is the user's: GET only reads
// (the GPU, the folder, the process); install, start, stop and delete are each a POST or DELETE
// from a button. When the server answers, its address, its per-start key and its context are
// written where every provider's are (the credentials file) and handed to the open
// conversations, so "local" is then a provider like any other.

// localDir is where the runtime and the model live; a variable so a test can use its own folder.
var localDir = func() string { return filepath.Join(config.Dir(), "local") }

// localModeler is what the endpoints ask of the local model (localmodel.Manager).
type localModeler interface {
	Status(ctx context.Context) localmodel.Status
	Install() error
	Cancel()
	Start(ctx context.Context) error
	Stop()
	Delete() error
	Close()
}

// newLocalModel makes the manager; a variable so a test can hand in one with a GPU.
var newLocalModel = func(dir string, onReady func(localmodel.Ready) error) localModeler {
	return localmodel.New(dir, onReady)
}

// localModelRoutes registers the endpoints.
func (s *Server) localModelRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/local-model", plain(s.handleLocalStatus))
	mux.Handle("POST /v1/local-model/install", plain(s.handleLocalInstall))
	mux.Handle("POST /v1/local-model/cancel", plain(s.handleLocalCancel))
	mux.Handle("POST /v1/local-model/start", plain(s.handleLocalStart))
	mux.Handle("POST /v1/local-model/stop", plain(s.handleLocalStop))
	mux.Handle("DELETE /v1/local-model", plain(s.handleLocalDelete))
}

// localSlot holds the manager once made.
type localSlot struct {
	mu sync.Mutex
	m  localModeler
}

// localModel is the manager, made on first use so a gateway that never opens the page reads
// nothing and starts nothing.
func (s *Server) localModel() localModeler {
	s.local.mu.Lock()
	defer s.local.mu.Unlock()
	if s.local.m == nil {
		s.local.m = newLocalModel(localDir(), s.localReady)
	}
	return s.local.m
}

// closeLocalModel stops the server with the gateway.
func (s *Server) closeLocalModel() {
	s.local.mu.Lock()
	m := s.local.m
	s.local.mu.Unlock()
	if m != nil {
		m.Close()
	}
}

// localReady stores the running server's address and key and hands them to every conversation.
// Start needs the configuration file, so there is one to write to.
func (s *Server) localReady(r localmodel.Ready) error {
	p, _ := onboard.Lookup(config.ProviderLocal)
	cfg := s.configuredLLM()
	err := s.saveProvider(cfg, p, true, r.APIKey, true, r.BaseURL)
	if err == nil {
		err = setCredential(s.opts.ConfigPath, config.LocalContextVariable, strconv.Itoa(r.Context))
	}
	if err != nil {
		return err
	}
	s.refreshSessions(config.ProviderLocal)
	return nil
}

func (s *Server) handleLocalStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.localModel().Status(r.Context()))
}

func (s *Server) handleLocalInstall(w http.ResponseWriter, r *http.Request) {
	m := s.localModel()
	if st := m.Status(r.Context()); !st.Eligible {
		writeRefusal(w, http.StatusConflict, "not_eligible", localmodel.ErrNotEligible.Error())
		return
	}
	if err := m.Install(); err != nil {
		writeRefusal(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, m.Status(r.Context()))
}

func (s *Server) handleLocalCancel(w http.ResponseWriter, r *http.Request) {
	m := s.localModel()
	m.Cancel()
	writeJSON(w, http.StatusOK, m.Status(r.Context()))
}

func (s *Server) handleLocalStart(w http.ResponseWriter, r *http.Request) {
	if !s.needFile(w) {
		return
	}
	m := s.localModel()
	if err := m.Start(r.Context()); err != nil {
		code := "start_failed"
		switch {
		case errors.Is(err, localmodel.ErrNotEligible):
			code = "not_eligible"
		case errors.Is(err, localmodel.ErrBusy):
			code = "busy"
		default:
			// Any other failure keeps start_failed.
		}
		writeRefusal(w, http.StatusConflict, code, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, m.Status(r.Context()))
}

func (s *Server) handleLocalStop(w http.ResponseWriter, r *http.Request) {
	m := s.localModel()
	m.Stop()
	writeJSON(w, http.StatusOK, m.Status(r.Context()))
}

// handleLocalDelete stops the server, removes the runtime and the model, and disconnects the
// provider.
func (s *Server) handleLocalDelete(w http.ResponseWriter, r *http.Request) {
	m := s.localModel()
	if err := m.Delete(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.opts.ConfigPath != "" {
		p, _ := onboard.Lookup(config.ProviderLocal)
		cfg := s.configuredLLM()
		err := s.saveProvider(cfg, p, true, "", true, "")
		if err == nil {
			err = setCredential(s.opts.ConfigPath, config.LocalContextVariable, "")
		}
		if err == nil {
			err = s.forgetEverywhere(cfg, p)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.refreshSessions(config.ProviderLocal)
	}
	writeJSON(w, http.StatusOK, m.Status(r.Context()))
}
